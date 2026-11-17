package sessionmaster

import "time"

const maxBrowseTargets = 32

type browseGrant struct {
	target     Owner
	descriptor FreeRide
	expires    time.Time
	fields     [5]uint32
	opaque     uint64
	extra      string
}

// RememberBrowse arms only the exact published generic rows returned to this
// live caller transport. A listing is not a room reservation or a CID claim.
func (s *Store) RememberBrowse(owner Owner, rows []Snapshot) error {
	if !owner.valid() {
		return ErrOwnership
	}
	if len(rows) > 3 {
		return ErrCapacity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.expire(now)
	var valid []Snapshot
	for _, row := range rows {
		r := s.sessions[row.ID]
		if row.Kind == GenericSession && row.Published && r != nil && r.snapshot.Kind == GenericSession && r.snapshot.Owner == row.Owner && r.snapshot.Published && sameBrowseContact(row.Descriptor, r.snapshot.Descriptor) {
			valid = append(valid, row)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	grants := s.browse[owner]
	if grants == nil {
		if len(s.browse) >= s.opts.MaxOwners {
			return ErrCapacity
		}
		grants = make(map[uint32]browseGrant)
		s.browse[owner] = grants
	}
	for _, row := range valid {
		if _, ok := grants[row.ID]; !ok && len(grants) >= maxBrowseTargets {
			var oldestID uint32
			var deadline time.Time
			for id, g := range grants {
				if oldestID == 0 || g.expires.Before(deadline) || (g.expires.Equal(deadline) && id < oldestID) {
					oldestID = id
					deadline = g.expires
				}
			}
			delete(grants, oldestID)
		}
		base := row.BaseSession()
		grants[row.ID] = browseGrant{target: row.Owner, descriptor: row.Descriptor, expires: now.Add(10 * time.Second), fields: base.Fields, opaque: base.Opaque, extra: string(base.Extra)}
	}
	return nil
}
func sameBrowseContact(a, b FreeRide) bool {
	return a.ID == b.ID && a.Identity == b.Identity && a.Reference == b.Reference && a.Connection == b.Connection && a.NAT == b.NAT && a.Game == b.Game && a.Name == b.Name
}
func (s *Service) BrowseIntroduction(owner Owner, targetCID uint32) (Membership, bool) {
	return s.store.BrowseIntroduction(owner, targetCID)
}
func (s *Store) BrowseIntroduction(owner Owner, targetCID uint32) (Membership, bool) {
	if !owner.valid() || targetCID == 0 {
		return Membership{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.expire(now)
	for id, g := range s.browse[owner] {
		if g.descriptor.Connection[3] != targetCID {
			continue
		}
		r := s.sessions[id]
		if r == nil || r.snapshot.Kind != GenericSession || !r.snapshot.Published || r.snapshot.Owner != g.target || r.snapshot.Owner == owner || !sameBrowseContact(g.descriptor, r.snapshot.Descriptor) || !sameBrowseMetadata(g, s.copyRecord(id, now)) {
			delete(s.browse[owner], id)
			continue
		}
		d := s.copyRecord(id, now).Descriptor
		return Membership{SessionID: id, CallerOwner: owner, TargetOwner: g.target, TargetHost: &d}, true
	}
	return Membership{}, false
}
func (s *Store) expireBrowse(now time.Time) {
	for owner, grants := range s.browse {
		for id, g := range grants {
			if !now.Before(g.expires) || s.sessions[id] == nil {
				delete(grants, id)
			}
		}
		if len(grants) == 0 {
			delete(s.browse, owner)
		}
	}
}

func sameBrowseMetadata(g browseGrant, r Snapshot) bool {
	b := r.BaseSession()
	return g.fields == b.Fields && g.opaque == b.Opaque && g.extra == string(b.Extra)
}
