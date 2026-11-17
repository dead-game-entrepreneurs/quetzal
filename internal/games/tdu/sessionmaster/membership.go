package sessionmaster

import "time"

// Membership relates only unverified, transport-owned FreeRide claims. Callers
// must independently authenticate any Quazal CID and check endpoint/admission
// policy; this result grants no account or arbitrary network forwarding right.
type Membership struct {
	SessionID                uint32
	CallerOwner, TargetOwner Owner
	// TargetHost is present only when the target owns and has published the
	// selected session. Joined clients' SessionClient ports are not peer endpoints.
	TargetHost             *FreeRide
	CallerReservationUntil time.Time
}

func (s *Service) Membership(callerCID, targetCID uint32) (Membership, bool) {
	return s.store.Membership(callerCID, targetCID)
}
func (s *Store) Membership(callerCID, targetCID uint32) (Membership, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.expire(now)
	if callerCID == 0 || targetCID == 0 || callerCID == targetCID {
		return Membership{}, false
	}
	caller, ok := s.connectionIDs[callerCID]
	if !ok {
		return Membership{}, false
	}
	target, ok := s.connectionIDs[targetCID]
	if !ok {
		return Membership{}, false
	}
	cc, tc := s.owners[caller], s.owners[target]
	if cc.session == 0 || cc.session != tc.session || s.sessions[cc.session] == nil {
		return Membership{}, false
	}
	// A temporary reservation permits initial introductions only while live.
	// Retaining the owner's CID claim is not evidence of successful peer join.
	if (!cc.created && !now.Before(cc.reservedUntil)) || (!tc.created && !now.Before(tc.reservedUntil)) {
		return Membership{}, false
	}
	out := Membership{SessionID: cc.session, CallerOwner: caller, TargetOwner: target, CallerReservationUntil: cc.reservedUntil}
	r := s.sessions[cc.session]
	if tc.created && r.snapshot.Published {
		d := s.copyRecord(cc.session, now).Descriptor
		out.TargetHost = &d
	}
	return out, true
}
