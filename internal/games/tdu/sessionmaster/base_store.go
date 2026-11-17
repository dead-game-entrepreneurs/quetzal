package sessionmaster

func (s *Store) genericCID(owner Owner, q GenericRequest) (uint32, error) {
	cid := q.Client.ConnectionID
	if cid != 0 && q.Session.Connection[3] != 0 && cid != q.Session.Connection[3] {
		return 0, ErrIdentity
	}
	if cid == 0 {
		cid = q.Session.Connection[3]
	}
	if old, ok := s.owners[owner]; ok && old.connectionID != 0 {
		if cid != 0 && cid != old.connectionID {
			return 0, ErrIdentity
		}
		cid = old.connectionID
	}
	if cid != 0 {
		if other, ok := s.connectionIDs[cid]; ok && other != owner {
			return 0, ErrIdentity
		}
	}
	return cid, nil
}
func (s *Store) CreateGeneric(owner Owner, q GenericRequest) (Snapshot, error) {
	if !owner.valid() || q.Session.validate() != nil {
		return Snapshot{}, ErrMalformed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.expire(now)
	cid, e := s.genericCID(owner, q)
	if e != nil {
		return Snapshot{}, e
	}
	old, known := s.owners[owner]
	if q.Session.ID != 0 && (!known || q.Session.ID != old.session) {
		return Snapshot{}, ErrOwnership
	}
	if known && old.created {
		r := s.sessions[old.session]
		if r != nil && r.snapshot.Kind == GenericSession && r.snapshot.Descriptor.Game == q.Session.Game && r.snapshot.Descriptor.Name == q.Session.Name {
			r.expires = now.Add(s.opts.TTL)
			return s.copyRecord(old.session, now), nil
		}
	}
	if (!known && len(s.owners) >= s.opts.MaxOwners) || (len(s.sessions) >= s.opts.MaxSessions && !(known && old.created)) {
		return Snapshot{}, ErrCapacity
	}
	if known && old.created {
		s.remove(old.session)
	} else {
		s.removeClaim(owner)
	}
	b := q.Session
	b.ID = s.nextID()
	b.Reference = ipv4(owner.Remote.Addr())
	b.Occupancy = 1
	b.Extra = append([]byte(nil), b.Extra...)
	d := b.freeRide()
	restr := Restrictions{Opaque70: b.Fields[1], Signed: int32(b.Fields[2]), ID: b.Fields[3], Category: b.Fields[4]}
	published := uint16(d.Connection[0]) != 0 && d.Connection[3] != 0
	client := q.Client
	client.ConnectionID = cid
	client.Extension = append([]byte(nil), client.Extension...)
	s.sessions[d.ID] = &record{snapshot: Snapshot{ID: d.ID, Owner: owner, ClaimedConnectionID: cid, Descriptor: d, Kind: GenericSession, Base: &b, Restrictions: restr, Published: published}, client: client, expires: now.Add(s.opts.TTL)}
	s.owners[owner] = claim{connectionID: cid, session: d.ID, created: true}
	if cid != 0 {
		s.connectionIDs[cid] = owner
	}
	return s.copyRecord(d.ID, now), nil
}
func (s *Store) UpdateGeneric(owner Owner, b BaseSession) (byte, error) {
	if !owner.valid() || b.validate() != nil || b.ID == 0 {
		return 0, ErrMalformed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.expire(now)
	r := s.sessions[b.ID]
	if r == nil {
		return 0xfe, nil
	}
	if r.snapshot.Owner != owner {
		return 0xff, nil
	}
	// A source-bound record may change browser fields but not take another CID.
	cid, e := s.genericCID(owner, GenericRequest{Session: b})
	if e != nil {
		return 0xff, nil
	}
	if b.Connection[3] != 0 && cid != b.Connection[3] {
		return 0xff, nil
	}
	b.Identity = r.snapshot.Descriptor.Identity
	if r.snapshot.Base != nil {
		b.Opaque = r.snapshot.Base.Opaque
	}
	b.Reference = ipv4(owner.Remote.Addr())
	if b.Occupancy == 0 {
		b.Occupancy = 1
	}
	b.Extra = append([]byte(nil), b.Extra...)
	d := b.freeRide()
	d.Bounds = r.snapshot.Descriptor.Bounds
	d.Flags = r.snapshot.Descriptor.Flags
	d.Extra = r.snapshot.Descriptor.Extra
	r.snapshot.Descriptor = d
	r.snapshot.Base = &b
	r.snapshot.Kind = GenericSession
	r.snapshot.ClaimedConnectionID = cid
	r.client.ConnectionID = cid
	r.snapshot.Restrictions = Restrictions{Opaque70: b.Fields[1], Signed: int32(b.Fields[2]), ID: b.Fields[3], Category: b.Fields[4]}
	r.snapshot.Published = uint16(b.Connection[0]) != 0 && b.Connection[3] != 0
	r.expires = now.Add(s.opts.TTL)
	c := s.owners[owner]
	c.connectionID = cid
	s.owners[owner] = c
	if cid != 0 {
		s.connectionIDs[cid] = owner
	}
	return 0, nil
}
func (s *Store) JoinGeneric(owner Owner, q GenericRequest) (*Snapshot, error) {
	if !owner.valid() || q.Session.validate() != nil {
		return nil, ErrMalformed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.expire(now)
	claimRequest := q
	claimRequest.Session.Connection[3] = 0
	cid, e := s.genericCID(owner, claimRequest)
	if e != nil {
		return nil, e
	}
	old, known := s.owners[owner]
	if !known && len(s.owners) >= s.opts.MaxOwners {
		return nil, ErrCapacity
	}
	var selected uint32
	for id, r := range s.sessions {
		d := r.snapshot.Descriptor
		if !r.snapshot.Published || r.snapshot.Owner == owner || d.Game != q.Session.Game || d.Name != q.Session.Name || (q.Session.ID != 0 && q.Session.ID != id) || (q.Session.Fields[3] != 0 && q.Session.Fields[3] != uint32(r.snapshot.Restrictions.ID)) || (q.Session.Fields[4] != 0 && q.Session.Fields[4] != uint32(r.snapshot.Restrictions.Category)) {
			continue
		}
		blocked := false
		for _, x := range q.Blacklist {
			if x.ID == id && x.Seconds > 0 {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		capacity := min(d.Capacity, q.Session.Capacity)
		used := s.occupancy(id, now)
		retry := known && !old.created && old.session == id && now.Before(old.reservedUntil)
		if !retry && used >= capacity {
			continue
		}
		if retry {
			selected = id
			break
		}
		if selected == 0 || id < selected {
			selected = id
		}
	}
	if selected == 0 {
		return nil, nil
	}
	if known && old.created {
		s.remove(old.session)
	} else {
		s.removeClaim(owner)
	}
	s.owners[owner] = claim{connectionID: cid, session: selected, reservedUntil: now.Add(s.opts.ReservationTTL)}
	if cid != 0 {
		s.connectionIDs[cid] = owner
	}
	r := s.copyRecord(selected, now)
	return &r, nil
}
