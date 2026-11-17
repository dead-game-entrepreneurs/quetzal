package sessionmaster

// MarshalBaseSession encodes the older edNetSessionV3 representation used by
// plain Search replies. FreeRide's extra data is not a base-session extra object.
// This convenience projection has no legacy metadata; service listings use
// Snapshot.BaseSession to retain actual restriction fields and optional objects.
func MarshalBaseSession(d FreeRide) ([]byte, error) {
	return marshalBaseSession(d, Restrictions{})
}
func marshalBaseSession(d FreeRide, restrictions Restrictions) ([]byte, error) {
	if e := d.validate(); e != nil {
		return nil, e
	}
	w := writer{}
	w.u16(0x7eca)
	w.u64(d.Identity)
	w.u32(d.Reference)
	for _, v := range d.Connection {
		w.u32(v)
	}
	w.u64(0)
	w.byte(d.NAT)
	w.blob([]byte(d.Game))
	w.blob([]byte(d.Name))
	w.byte(d.Occupancy)
	w.byte(d.Capacity)
	w.u32(0)
	w.u32(uint32(restrictions.Opaque70))
	w.u32(uint32(int32(restrictions.Signed)))
	w.u32(uint32(restrictions.ID))
	w.u32(uint32(restrictions.Category))
	w.u32(d.ID)
	return w.b, nil
}
func ParseSearch(b []byte) (name string, limit uint32, err error) {
	r := reader{b: b}
	if r.u16() != Search {
		return "", 0, ErrMalformed
	}
	name = string(r.blob(32))
	limit = r.u32()
	if r.pos < len(r.b) {
		r.u32()
	} // Builder12f5e0 includes one trailing signed scalar; dispatcher ignores it.
	if !r.done() || !validString(name, 32) {
		return "", 0, ErrMalformed
	}
	return name, limit, nil
}
func (s *Store) Search(name string, limit uint32) []FreeRide {
	snapshots := s.Snapshot()
	if limit > 3 {
		limit = 3
	}
	out := make([]FreeRide, 0, int(limit))
	for _, r := range snapshots {
		if uint32(len(out)) >= limit {
			break
		}
		if r.Published && r.Descriptor.Name == name {
			out = append(out, r.Descriptor)
		}
	}
	return out
}
func marshalSearchReply(d FreeRide) ([]byte, error) {
	return marshalRestrictedSearchReply(d, Restrictions{})
}
func marshalRestrictedSearchReply(d FreeRide, restrictions Restrictions) ([]byte, error) {
	b, e := marshalBaseSession(d, restrictions)
	if e != nil {
		return nil, e
	}
	w := writer{}
	w.u16(SearchReply)
	w.u32(d.ID)
	w.blob(b)
	return w.b, nil
}
