package sessionmaster

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/nativeproxy"
)

type Options struct {
	// Clock supplies the real server clock for core get-time discovery requests.
	// Unix seconds is replacement-server policy; an active retail epoch reader
	// has not been recovered. It is not an identity or admission value.
	Clock           func() uint32
	Port            uint16
	ActivityCatalog map[string]uint16
	Store           *Store
	// Admit is optional extra policy for the unverified claimed CID. It may bind
	// that claim to independently verified Quazal/native proof once available.
	// A nil hook permits only transport-owned ephemeral registry operations.
	Admit func(context.Context, nativeproxy.SessionInfo, uint32) error
}
type Service struct {
	opts  Options
	store *Store
}

func New(o Options) (*Service, error) {
	if o.Port == 0 {
		return nil, ErrMalformed
	}
	if o.ActivityCatalog != nil {
		catalog := make(map[string]uint16, len(o.ActivityCatalog))
		if len(o.ActivityCatalog) > stockAggregateIndex {
			return nil, ErrMalformed
		}
		for name, index := range o.ActivityCatalog {
			if !validString(name, 32) || index >= stockAggregateIndex || name == "DriveIn_PS2" {
				return nil, ErrMalformed
			}
			catalog[name] = index
		}
		o.ActivityCatalog = catalog
	}
	if o.Clock == nil {
		o.Clock = func() uint32 { return uint32(time.Now().Unix()) }
	}
	if o.Store == nil {
		var e error
		o.Store, e = NewStore(StoreOptions{})
		if e != nil {
			return nil, e
		}
	}
	return &Service{opts: o, store: o.Store}, nil
}
func (s *Service) Snapshot() []Snapshot { return s.store.Snapshot() }
func (s *Service) DropOwner(o Owner)    { s.store.DropOwner(o) }
func OwnerFromInfo(info nativeproxy.SessionInfo) Owner {
	return Owner{Remote: info.Remote, Generation: info.Generation}
}
func (s *Service) OnDisconnect(info nativeproxy.SessionInfo) { s.DropOwner(OwnerFromInfo(info)) }
func (s *Service) BatchHandlers() map[uint16]nativeproxy.BatchDataHandler {
	return map[uint16]nativeproxy.BatchDataHandler{s.opts.Port: s.HandleFrames}
}

// SupportedActions are the recovered requests accepted by this service.
// Requests are registered only when their actual handler is implemented.
func SupportedActions() []uint16 {
	return []uint16{GetTime, GenericCreate, GenericJoin, GenericUpdate, CreateOrJoin, Update, GetInfo, Delete, Search, SessionDensity, FilteredSearch, FilteredSearchV2, UpdateRestrictions, UpdateRestrictionsV2}
}

// HandleFrames keeps generic reliable completion separate from the asynchronous
// D572 business response used by the stock SessionMaster dispatcher. Never join
// the returned frames into one UDP payload.
func (s *Service) HandleFrames(ctx context.Context, info nativeproxy.SessionInfo, dest netip.AddrPort, packet []byte) ([][]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	owner := OwnerFromInfo(info)
	if !info.TransportActivated || !owner.valid() {
		return nil, ErrOwnership
	}
	f, e := ednet.ParseFrame(packet)
	if e != nil {
		return nil, e
	}
	if f.Kind == ednet.Ack {
		return nil, nil
	}
	if f.Kind != ednet.Call && f.Kind != ednet.CallUnreliable || len(f.Action) < 2 {
		return nil, ErrUnsupported
	}
	code := binary.BigEndian.Uint16(f.Action)
	if code == GenericCreate || code == GenericJoin || code == GenericUpdate {
		return s.handleGeneric(ctx, info, f, code)
	}
	if code == SessionDensity {
		if len(f.Action) != 6 {
			return nil, ErrMalformed
		}
		if binary.BigEndian.Uint32(f.Action[2:]) != 2 {
			return nil, ErrUnsupported
		}
		business, err := (ednet.Frame{Kind: ednet.CallUnreliable, Action: s.SessionDensity()}).MarshalBinary()
		if err != nil {
			return nil, err
		}
		frames := [][]byte{business}
		if f.Kind == ednet.Call && f.RequestID != 0 {
			completion, err := (ednet.Frame{Kind: ednet.Reply, RequestID: f.RequestID}).MarshalBinary()
			if err != nil {
				return nil, err
			}
			frames = append(frames, completion)
		}
		return frames, nil
	}
	if code == GetTime {
		if len(f.Action) != 2 {
			return nil, ErrMalformed
		}
		action := MarshalID(GetTimeReply, s.opts.Clock())
		var reply ednet.Frame
		if f.Kind == ednet.Call {
			if f.RequestID == 0 {
				return nil, nil
			}
			reply = ednet.Frame{Kind: ednet.Reply, RequestID: f.RequestID, Action: action}
		} else {
			reply = ednet.Frame{Kind: ednet.CallUnreliable, Action: action}
		}
		b, e := reply.MarshalBinary()
		if e != nil {
			return nil, e
		}
		return [][]byte{b}, nil
	}
	var reply []byte
	var businessActions [][]byte
	var searchName string
	var searchLimit uint32
	var claimedCID uint32
	var restriction RestrictionUpdate
	var filter FilterQuery
	var create CreateRequest
	var update FreeRide
	switch code {
	case CreateOrJoin:
		create, e = ParseCreateRequest(f.Action)
		if e != nil {
			return nil, e
		}
		claimedCID = create.Client.ConnectionID
	case Update:
		r := reader{b: f.Action}
		r.u16()
		update, e = ParseFreeRide(r.blob(512))
		if e != nil || !r.done() {
			return nil, ErrMalformed
		}
		claimedCID = s.store.OwnerConnectionID(owner)
	case UpdateRestrictions, UpdateRestrictionsV2:
		restriction, e = ParseRestrictionUpdate(f.Action)
		if e != nil {
			return nil, e
		}
		claimedCID = s.store.OwnerConnectionID(owner)
	case FilteredSearch, FilteredSearchV2:
		filter, e = ParseFilteredSearch(f.Action)
		if e != nil {
			return nil, e
		}
	case Search:
		searchName, searchLimit, e = ParseSearch(f.Action)
		if e != nil {
			return nil, e
		}
	case GetInfo, Delete:
		if len(f.Action) != 6 || binary.BigEndian.Uint32(f.Action[2:]) == 0 {
			return nil, ErrMalformed
		}
		claimedCID = s.store.OwnerConnectionID(owner)
	default:
		return nil, ErrUnsupported
	}
	if code != GetInfo && code != Search && code != FilteredSearch && code != FilteredSearchV2 && s.opts.Admit != nil {
		if e := s.opts.Admit(ctx, info, claimedCID); e != nil {
			return nil, e
		}
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	switch code {
	case CreateOrJoin:
		result, d, err := s.store.Create(owner, create)
		if err != nil {
			if errors.Is(err, ErrCapacity) {
				reply, e = MarshalCreateReply(0, nil)
			} else {
				return nil, err
			}
		} else {
			reply, e = MarshalCreateReply(result, &d)
		}
	case Update:
		event, err := s.store.Update(owner, update)
		if err != nil {
			return nil, err
		}
		var reference uint32
		var port uint16
		if d, exists := s.store.Get(update.ID); exists {
			reference = d.Reference
			port = uint16(d.Connection[0])
		}
		reply = marshalNotify(update.ID, event, reference, port)
	case GetInfo:
		d, exists := s.store.Get(binary.BigEndian.Uint32(f.Action[2:]))
		if exists {
			reply, e = MarshalGetReply(&d)
		} else {
			reply, e = MarshalGetReply(nil)
		}
	case Search:
		rows := s.store.Filter(owner, FilterQuery{Name: searchName, Limit: searchLimit, Signed: -1})
		if e = s.store.RememberBrowse(owner, rows); e != nil {
			return nil, e
		}
		for _, r := range rows {
			a, err := marshalSnapshotSearchReply(r)
			if err != nil {
				return nil, err
			}
			businessActions = append(businessActions, a)
		}
	case UpdateRestrictions, UpdateRestrictionsV2:
		if e = s.store.SetRestrictions(owner, restriction); e != nil {
			return nil, e
		}
	case FilteredSearch, FilteredSearchV2:
		rows := s.store.Filter(owner, filter)
		if e = s.store.RememberBrowse(owner, rows); e != nil {
			return nil, e
		}
		for _, r := range rows {
			a, err := marshalSnapshotSearchReply(r)
			if err != nil {
				return nil, err
			}
			businessActions = append(businessActions, a)
		}
	case Delete:
		if e = s.store.Delete(owner, binary.BigEndian.Uint32(f.Action[2:])); e != nil {
			return nil, e
		}
	}
	if e != nil {
		return nil, e
	}
	frames := make([][]byte, 0, 2)
	if len(reply) > 0 {
		businessActions = append(businessActions, reply)
	}
	for _, action := range businessActions {
		business, e := (ednet.Frame{Kind: ednet.CallUnreliable, Action: action}).MarshalBinary()
		if e != nil {
			return nil, e
		}
		frames = append(frames, business)
	}
	if f.Kind == ednet.Call && f.RequestID != 0 {
		complete, e := (ednet.Frame{Kind: ednet.Reply, RequestID: f.RequestID}).MarshalBinary()
		if e != nil {
			return nil, e
		}
		frames = append(frames, complete)
	}
	return frames, nil
}
