package sessionmaster

import (
	"context"
	"encoding/binary"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/nativeproxy"
)

// Generic create/join return their business object inside the reliable 984E
// completion, unlike FreeRide's independent D572 result callbacks.
func (s *Service) handleGeneric(ctx context.Context, info nativeproxy.SessionInfo, f ednet.Frame, code uint16) ([][]byte, error) {
	owner := OwnerFromInfo(info)
	var action []byte
	var e error
	if code == GenericUpdate {
		r := reader{b: f.Action}
		r.u16()
		d, err := ParseBaseSession(r.blob(1024))
		if err != nil || !r.done() {
			return nil, ErrMalformed
		}
		if s.opts.Admit != nil {
			if e = s.opts.Admit(ctx, info, s.store.OwnerConnectionID(owner)); e != nil {
				return nil, e
			}
		}
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		event, err := s.store.UpdateGeneric(owner, d)
		if err != nil {
			return nil, err
		}
		var ref uint32
		var port uint16
		if actual, ok := s.store.Get(d.ID); ok {
			ref = actual.Reference
			port = uint16(actual.Connection[0])
		}
		action = marshalNotify(d.ID, event, ref, port)
	} else {
		if f.Kind != ednet.Call || f.RequestID == 0 {
			return nil, ErrMalformed
		}
		q, err := ParseGenericRequest(f.Action)
		if err != nil {
			return nil, err
		}
		claimed := q.Client.ConnectionID
		if claimed == 0 {
			claimed = q.Session.Connection[3]
		}
		if claimed == 0 {
			claimed = s.store.OwnerConnectionID(owner)
		}
		if s.opts.Admit != nil {
			if e = s.opts.Admit(ctx, info, claimed); e != nil {
				return nil, e
			}
		}
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if code == GenericCreate {
			result, err := s.store.CreateGeneric(owner, q)
			if err != nil {
				if err != ErrCapacity {
					return nil, err
				}
				w := writer{}
				w.u16(GenericCreateReply)
				w.byte(0)
				w.u32(0)
				action = w.b
			} else {
				action, e = marshalGenericCreated(result)
			}
		} else {
			result, err := s.store.JoinGeneric(owner, q)
			if err != nil {
				return nil, err
			}
			action, e = marshalGenericJoined(result)
		}
	}
	if e != nil {
		return nil, e
	}
	kind := ednet.CallUnreliable
	id := uint32(0)
	if code != GenericUpdate {
		kind = ednet.Reply
		id = f.RequestID
	}
	encoded, e := (ednet.Frame{Kind: kind, RequestID: id, Action: action}).MarshalBinary()
	if e != nil {
		return nil, e
	}
	frames := [][]byte{encoded}
	if code == GenericUpdate && f.Kind == ednet.Call && f.RequestID != 0 {
		completion, err := (ednet.Frame{Kind: ednet.Reply, RequestID: f.RequestID}).MarshalBinary()
		if err != nil {
			return nil, err
		}
		frames = append(frames, completion)
	}
	return frames, nil
}
func marshalSnapshotSearchReply(r Snapshot) ([]byte, error) {
	b, e := r.BaseSession().MarshalBinary()
	if e != nil {
		return nil, e
	}
	w := writer{}
	w.u16(SearchReply)
	w.u32(r.ID)
	w.blob(b)
	return w.b, nil
}

// ParseGenericResult exposes only proven reliable result fields. A successful
// create contains BaseSessionV3; a successful join contains FreeRideV3 followed
// by an edNetClientV2 source endpoint object. Callers choose the matching codec.
func ParseGenericResult(b []byte) (code uint16, result byte, id uint32, objects [][]byte, err error) {
	r := reader{b: b}
	code = r.u16()
	if code != GenericCreateReply && code != GenericJoinReply {
		return 0, 0, 0, nil, ErrMalformed
	}
	result = r.byte()
	id = r.u32()
	for r.pos < len(r.b) && r.err == nil {
		if len(objects) == 2 {
			return 0, 0, 0, nil, ErrMalformed
		}
		objects = append(objects, append([]byte(nil), r.blob(1024)...))
	}
	if !r.done() || result > 1 || (result == 1 && (id == 0 || len(objects) == 0)) || (result == 0 && (id != 0 || len(objects) != 0)) {
		err = ErrMalformed
	}
	if err == nil && result == 1 {
		if code == GenericCreateReply {
			if len(objects) != 1 {
				err = ErrMalformed
			} else {
				_, err = ParseBaseSession(objects[0])
			}
		} else {
			if len(objects) != 2 || len(objects[1]) < 10 || binary.BigEndian.Uint16(objects[1]) != 0x269e {
				err = ErrMalformed
			} else {
				_, err = ParseFreeRide(objects[0])
			}
		}
	}
	return
}
