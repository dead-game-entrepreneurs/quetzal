package sessionmaster

import (
	"net/netip"
	"sort"
	"sync"
	"time"
)

// Owner is an exact live transport generation, never a claimed account PID.
type Owner struct {
	Remote     netip.AddrPort
	Generation uint64
}

func (o Owner) valid() bool {
	return o.Remote.IsValid() && o.Remote.Addr().Is4() && !o.Remote.Addr().IsUnspecified() && !o.Remote.Addr().IsMulticast() && o.Generation != 0
}

type Snapshot struct {
	Kind                SessionKind
	Base                *BaseSession
	ID                  uint32
	Owner               Owner
	ClaimedConnectionID uint32
	Descriptor          FreeRide
	Occupancy, Capacity uint8
	Restrictions        Restrictions
	Published           bool
	ExpiresAt           time.Time
}
type StoreOptions struct {
	MaxSessions, MaxOwners int
	TTL, ReservationTTL    time.Duration
	Now                    func() time.Time
}
type record struct {
	snapshot Snapshot
	client   Client
	expires  time.Time
}
type claim struct {
	connectionID, session uint32
	created               bool
	reservedUntil         time.Time
}

// Store serializes ownership, matching and reservation changes. All state is
// ephemeral; no account, progression or statistics records are changed.
type Store struct {
	mu            sync.Mutex
	opts          StoreOptions
	sessions      map[uint32]*record
	owners        map[Owner]claim
	connectionIDs map[uint32]Owner
	next          uint32
	browse        map[Owner]map[uint32]browseGrant
}

func NewStore(o StoreOptions) (*Store, error) {
	if o.MaxSessions == 0 {
		o.MaxSessions = 128
	}
	if o.MaxOwners == 0 {
		o.MaxOwners = o.MaxSessions * 8
	}
	if o.TTL == 0 {
		o.TTL = 15 * time.Second
	}
	if o.ReservationTTL == 0 {
		o.ReservationTTL = min(5*time.Second, o.TTL)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxSessions < 1 || o.MaxSessions > 4096 || o.MaxOwners < 1 || o.MaxOwners > 32768 || o.TTL < time.Millisecond || o.ReservationTTL < time.Millisecond || o.ReservationTTL > o.TTL {
		return nil, ErrMalformed
	}
	return &Store{opts: o, sessions: make(map[uint32]*record), owners: make(map[Owner]claim), connectionIDs: make(map[uint32]Owner), next: 1, browse: make(map[Owner]map[uint32]browseGrant)}, nil
}
func (s *Store) removeClaim(owner Owner) {
	if c, ok := s.owners[owner]; ok {
		if s.connectionIDs[c.connectionID] == owner {
			delete(s.connectionIDs, c.connectionID)
		}
		delete(s.owners, owner)
	}
}
func (s *Store) remove(id uint32) {
	delete(s.sessions, id)
	for owner, grants := range s.browse {
		delete(grants, id)
		if len(grants) == 0 {
			delete(s.browse, owner)
		}
	}
	for owner, c := range s.owners {
		if c.session == id {
			s.removeClaim(owner)
		}
	}
}
func (s *Store) expire(now time.Time) {
	for id, r := range s.sessions {
		if !now.Before(r.expires) {
			s.remove(id)
		}
	}
	s.expireBrowse(now)
}
func (s *Store) occupancy(id uint32, now time.Time) byte {
	r := s.sessions[id]
	n := 1
	for _, c := range s.owners {
		if c.session == id && !c.created && now.Before(c.reservedUntil) {
			n++
		}
	}
	if int(r.snapshot.Descriptor.Occupancy) > n {
		n = int(r.snapshot.Descriptor.Occupancy)
	}
	if n > int(r.snapshot.Descriptor.Capacity) {
		n = int(r.snapshot.Descriptor.Capacity)
	}
	return byte(n)
}
func (s *Store) copyRecord(id uint32, now time.Time) Snapshot {
	r := s.sessions[id]
	out := r.snapshot
	if out.Base != nil {
		b := *out.Base
		b.Extra = append([]byte(nil), b.Extra...)
		out.Base = &b
	}
	out.Descriptor.Occupancy = s.occupancy(id, now)
	out.Occupancy = out.Descriptor.Occupancy
	out.Capacity = out.Descriptor.Capacity
	out.ExpiresAt = r.expires
	return out
}
func (s *Store) Snapshot() []Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.expire(now)
	out := make([]Snapshot, 0, len(s.sessions))
	for id := range s.sessions {
		out = append(out, s.copyRecord(id, now))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Store) DropOwner(owner Owner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.browse, owner)
	if c, ok := s.owners[owner]; ok {
		if c.created {
			s.remove(c.session)
		} else {
			s.removeClaim(owner)
		}
	}
}
func (s *Store) OwnerConnectionID(owner Owner) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(s.opts.Now())
	return s.owners[owner].connectionID
}
func (s *Store) Get(id uint32) (FreeRide, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.expire(now)
	if s.sessions[id] == nil {
		return FreeRide{}, false
	}
	return s.copyRecord(id, now).Descriptor, true
}
func (s *Store) nextID() uint32 {
	for {
		v := s.next
		s.next++
		if v != 0 && s.sessions[v] == nil {
			return v
		}
	}
}
func excluded(q CreateRequest, id uint32) bool {
	for _, x := range q.Blacklist {
		if x.ID == id && x.Seconds > 0 {
			return true
		}
	}
	return false
}

// Create implements transport-owned create/join. Matching overlap and the
// reservation timeout are explicit server policies, not recovered retail-server
// internals. Returned peer endpoints come only from the record owner's updates.
func (s *Store) Create(owner Owner, q CreateRequest) (byte, FreeRide, error) {
	if !owner.valid() || q.Client.ConnectionID == 0 || q.Session.validate() != nil {
		return 0, FreeRide{}, ErrMalformed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.expire(now)
	if other, ok := s.connectionIDs[q.Client.ConnectionID]; ok && other != owner {
		return 0, FreeRide{}, ErrIdentity
	}
	old, known := s.owners[owner]
	if known && old.connectionID != 0 && old.connectionID != q.Client.ConnectionID {
		return 0, FreeRide{}, ErrIdentity
	}
	// An explicit existing ID may only name this transport's selected record.
	// The active FreeRide builders normally send zero; never adopt another
	// owner's ID from an unverified descriptor.
	if q.Session.ID != 0 && (!known || q.Session.ID != old.session) {
		return 0, FreeRide{}, ErrOwnership
	}
	if known && s.sessions[old.session] != nil && !excluded(q, old.session) {
		r := s.sessions[old.session]
		d := r.snapshot.Descriptor
		if r.snapshot.Kind == FreeRideSession && q.Session.Game == d.Game && q.Session.Name == d.Name && q.Session.Bounds.Overlaps(d.Bounds) && (old.created || now.Before(old.reservedUntil) || s.occupancy(old.session, now) < d.Capacity) {
			if old.created {
				r.expires = now.Add(s.opts.TTL)
				return 1, s.copyRecord(old.session, now).Descriptor, nil
			}
			old.reservedUntil = now.Add(s.opts.ReservationTTL)
			s.owners[owner] = old
			return 2, s.copyRecord(old.session, now).Descriptor, nil
		}
	}
	if !known && len(s.owners) >= s.opts.MaxOwners {
		return 0, FreeRide{}, ErrCapacity
	}
	ids := make([]uint32, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		r := s.sessions[id]
		d := r.snapshot.Descriptor
		if r.snapshot.Kind != FreeRideSession || (known && id == old.session) || !r.snapshot.Published || excluded(q, id) || d.Game != q.Session.Game || d.Name != q.Session.Name || !d.Bounds.Overlaps(q.Session.Bounds) || s.occupancy(id, now) >= d.Capacity {
			continue
		}
		if known && old.created {
			s.remove(old.session)
		} else {
			s.removeClaim(owner)
		}
		s.owners[owner] = claim{connectionID: q.Client.ConnectionID, session: id, reservedUntil: now.Add(s.opts.ReservationTTL)}
		s.connectionIDs[q.Client.ConnectionID] = owner
		return 2, s.copyRecord(id, now).Descriptor, nil
	}
	if len(s.sessions) >= s.opts.MaxSessions && !(known && old.created) {
		return 0, FreeRide{}, ErrCapacity
	}
	if known && old.created {
		s.remove(old.session)
	} else {
		s.removeClaim(owner)
	}
	d := q.Session
	d.ID = s.nextID()
	d.Reference = ipv4(owner.Remote.Addr())
	d.Occupancy = 1
	c := q.Client
	c.Extension = append([]byte(nil), c.Extension...)
	s.sessions[d.ID] = &record{snapshot: Snapshot{ID: d.ID, Owner: owner, ClaimedConnectionID: c.ConnectionID, Descriptor: d}, client: c, expires: now.Add(s.opts.TTL)}
	s.owners[owner] = claim{connectionID: c.ConnectionID, session: d.ID, created: true}
	s.connectionIDs[c.ConnectionID] = owner
	return 1, d, nil
}
func ipv4(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// Update returns the observed event byte: FE unknown, FF wrong owner, zero
// accepted. Reference always reflects the owner's observed transport address.
func (s *Store) Update(owner Owner, d FreeRide) (byte, error) {
	if !owner.valid() || d.validate() != nil || d.ID == 0 {
		return 0, ErrMalformed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.expire(now)
	r := s.sessions[d.ID]
	if r == nil {
		return 0xfe, nil
	}
	if r.snapshot.Owner != owner {
		return 0xff, nil
	}
	if d.Identity != r.snapshot.Descriptor.Identity || d.Game != r.snapshot.Descriptor.Game || d.Name != r.snapshot.Descriptor.Name {
		return 0xff, nil
	}
	// These fields introduce another driver to the host. Keep the advertised CID
	// consistent with the owner's original self-claim; this is consistency, not proof.
	if d.Connection[3] != 0 && d.Connection[3] != r.client.ConnectionID {
		return 0xff, nil
	}
	d.Reference = ipv4(owner.Remote.Addr())
	if d.Occupancy == 0 {
		d.Occupancy = 1
	}
	r.snapshot.Descriptor = d
	r.snapshot.Published = d.Reference != 0 && uint16(d.Connection[0]) != 0 && d.Connection[3] != 0
	r.expires = now.Add(s.opts.TTL)
	return 0, nil
}
func (s *Store) Delete(owner Owner, id uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(s.opts.Now())
	r := s.sessions[id]
	if r == nil {
		return nil
	}
	if r.snapshot.Owner != owner {
		return ErrOwnership
	}
	s.remove(id)
	return nil
}
