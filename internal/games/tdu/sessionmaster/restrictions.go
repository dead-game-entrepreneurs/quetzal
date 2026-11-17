package sessionmaster

import "sort"

const (
	FilteredSearch       uint16 = 0x3e43
	FilteredSearchV2     uint16 = 0x7ae3
	UpdateRestrictions   uint16 = 0xa9e5
	UpdateRestrictionsV2 uint16 = 0x31e0
)

// Restrictions names only established storage/compare semantics. The meanings
// of the browser's opaque selection numbers have not been recovered.
type Restrictions struct {
	Opaque70 uint32
	Signed   int32
	ID       uint32
	Category uint32
}
type RestrictionUpdate struct {
	SessionID    uint32
	Capacity     uint16
	Restrictions Restrictions
	Reserved     [3]uint32 // V2 dispatcher consumes and discards these.
	Extra        []byte    // Native dispatcher does not apply this optional buffer.
}
type FilterQuery struct {
	Name     string
	Limit    uint32
	ID       uint16
	Signed   int16     // -1 is wildcard.
	Category byte      // zero is wildcard.
	Reserved [3]uint32 // V2 builder's two zero scalars and opaque trailing scalar.
}

func ParseRestrictionUpdate(b []byte) (RestrictionUpdate, error) {
	r := reader{b: b}
	q := RestrictionUpdate{}
	code := r.u16()
	if code != UpdateRestrictions && code != UpdateRestrictionsV2 {
		return q, ErrMalformed
	}
	q.SessionID = r.u32()
	q.Capacity = r.u16()
	q.Restrictions.Opaque70 = uint32(r.u16())
	if code == UpdateRestrictions {
		packed := r.u16()
		q.Restrictions.Signed = int32(int8(packed))
		q.Restrictions.Category = uint32(packed >> 8)
		q.Restrictions.ID = uint32(r.u16())
	} else {
		q.Restrictions.Signed = int32(int16(r.u16()))
		q.Restrictions.Category = uint32(r.byte())
		q.Restrictions.ID = uint32(r.u16())
		for i := range q.Reserved {
			q.Reserved[i] = r.u32()
		}
		if r.pos < len(r.b) {
			q.Extra = append([]byte(nil), r.blob(128)...)
		}
	}
	if !r.done() || q.SessionID == 0 || q.Capacity < 1 || q.Capacity > 8 {
		return RestrictionUpdate{}, ErrMalformed
	}
	return q, nil
}
func (q RestrictionUpdate) MarshalBinary() ([]byte, error) {
	if q.SessionID == 0 || q.Capacity < 1 || q.Capacity > 8 || len(q.Extra) > 128 || !q.Restrictions.wireValid() {
		return nil, ErrMalformed
	}
	w := writer{}
	w.u16(UpdateRestrictionsV2)
	w.u32(q.SessionID)
	w.u16(q.Capacity)
	w.u16(uint16(q.Restrictions.Opaque70))
	w.u16(uint16(q.Restrictions.Signed))
	w.byte(byte(q.Restrictions.Category))
	w.u16(uint16(q.Restrictions.ID))
	for _, v := range q.Reserved {
		w.u32(v)
	}
	if len(q.Extra) > 0 {
		w.blob(q.Extra)
	}
	return w.b, nil
}
func ParseFilteredSearch(b []byte) (FilterQuery, error) {
	r := reader{b: b}
	q := FilterQuery{}
	code := r.u16()
	if code != FilteredSearch && code != FilteredSearchV2 {
		return q, ErrMalformed
	}
	q.Name = string(r.blob(32))
	q.Limit = r.u32()
	q.ID = r.u16()
	if code == FilteredSearch {
		packed := r.u16()
		q.Signed = int16(int8(packed))
		q.Category = byte(packed >> 8)
		if r.pos < len(r.b) {
			q.Reserved[2] = r.u32()
		}
	} else {
		q.Signed = int16(r.u16())
		q.Category = r.byte()
		q.Reserved[0] = r.u32()
		q.Reserved[1] = r.u32()
		if r.pos < len(r.b) {
			q.Reserved[2] = r.u32()
		}
	}
	if !r.done() || !validString(q.Name, 32) {
		return FilterQuery{}, ErrMalformed
	}
	return q, nil
}
func (q FilterQuery) MarshalBinary() ([]byte, error) {
	if !validString(q.Name, 32) {
		return nil, ErrMalformed
	}
	w := writer{}
	w.u16(FilteredSearchV2)
	w.blob([]byte(q.Name))
	w.u32(q.Limit)
	w.u16(q.ID)
	w.u16(uint16(q.Signed))
	w.byte(q.Category)
	for _, v := range q.Reserved {
		w.u32(v)
	}
	return w.b, nil
}

// SetRestrictions applies exactly the recovered owner-only scalar changes.
// Native unknown/foreign IDs do not receive a separate business response.
func (s *Store) SetRestrictions(owner Owner, q RestrictionUpdate) error {
	if !owner.valid() || q.SessionID == 0 || q.Capacity < 1 || q.Capacity > 8 || !q.Restrictions.wireValid() {
		return ErrMalformed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.expire(now)
	r := s.sessions[q.SessionID]
	if r == nil {
		return nil
	}
	if r.snapshot.Owner != owner {
		return ErrOwnership
	}
	r.snapshot.Descriptor.Capacity = byte(q.Capacity)
	if r.snapshot.Descriptor.Occupancy > byte(q.Capacity) {
		r.snapshot.Descriptor.Occupancy = byte(q.Capacity)
	}
	r.snapshot.Restrictions = q.Restrictions
	r.expires = now.Add(s.opts.TTL)
	return nil
}
func (s *Store) Filter(owner Owner, q FilterQuery) []Snapshot {
	snapshots := s.Snapshot()
	limit := min(q.Limit, uint32(3))
	out := make([]Snapshot, 0, int(limit))
	for _, r := range snapshots {
		if uint32(len(out)) >= limit {
			break
		}
		f := r.Restrictions
		if !r.Published || r.Owner == owner || r.Descriptor.Name != q.Name || (q.ID != 0 && uint32(q.ID) != f.ID) || (q.Signed != -1 && int32(q.Signed) != f.Signed) || (q.Category != 0 && uint32(q.Category) != f.Category) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r Restrictions) wireValid() bool {
	return r.Opaque70 <= 65535 && r.Signed >= -32768 && r.Signed <= 32767 && r.ID <= 65535 && r.Category <= 255
}
