package presence

import (
	"context"
	"encoding/binary"
	"math"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/nativeproxy"
)

type Owner struct {
	Remote     netip.AddrPort
	Generation uint64
}
type Record struct {
	Owner     Owner
	Update    Update
	NATType   byte
	ExpiresAt time.Time
}
type Options struct {
	DensityPort, UserMapPort uint16
	TTL                      time.Duration
	MaxPlayers               int
	Now                      func() time.Time
	// Zero selects the dimensions recovered from the US PS2 world constructor.
	WorldWidth, WorldDepth float32
}
type Service struct {
	mu      sync.Mutex
	opts    Options
	players map[Owner]Record
}

func New(o Options) (*Service, error) {
	if o.DensityPort == 0 || o.UserMapPort == 0 || o.DensityPort == o.UserMapPort {
		return nil, ErrMalformed
	}
	if o.TTL == 0 {
		o.TTL = 30 * time.Second
	}
	if o.MaxPlayers == 0 {
		o.MaxPlayers = 2048
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.WorldWidth == 0 {
		o.WorldWidth = StockWidth
	}
	if o.WorldDepth == 0 {
		o.WorldDepth = StockDepth
	}
	if o.TTL < time.Second || o.TTL > 10*time.Minute || o.MaxPlayers < 1 || o.MaxPlayers > 65536 || !validPosition(Position{X: o.WorldWidth, Z: o.WorldDepth}) || o.WorldWidth <= 0 || o.WorldDepth <= 0 {
		return nil, ErrMalformed
	}
	return &Service{opts: o, players: make(map[Owner]Record)}, nil
}
func (s *Service) Handlers() map[uint16]nativeproxy.DataHandler {
	return map[uint16]nativeproxy.DataHandler{s.opts.DensityPort: s.Handle, s.opts.UserMapPort: s.Handle}
}
func (s *Service) Registrations() map[uint16]uint16 {
	return map[uint16]uint16{TrackRequestID: s.opts.DensityPort, MapRequestID: s.opts.DensityPort, UpdateID: s.opts.UserMapPort}
}
func (s *Service) expire(now time.Time) {
	for k, v := range s.players {
		if !now.Before(v.ExpiresAt) {
			delete(s.players, k)
		}
	}
}
func (s *Service) DropOwner(info nativeproxy.SessionInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.players, Owner{info.Remote, info.Generation})
}
func (s *Service) OnDisconnect(info nativeproxy.SessionInfo) { s.DropOwner(info) }
func ownerLess(a, b Owner) bool {
	if c := a.Remote.Compare(b.Remote); c != 0 {
		return c < 0
	}
	return a.Generation < b.Generation
}
func (s *Service) Snapshot() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(s.opts.Now())
	v := make([]Record, 0, len(s.players))
	for _, r := range s.players {
		v = append(v, r)
	}
	sort.Slice(v, func(i, j int) bool { return ownerLess(v[i].Owner, v[j].Owner) })
	return v
}
func (s *Service) Handle(ctx context.Context, info nativeproxy.SessionInfo, dest netip.AddrPort, wire []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owner := Owner{info.Remote, info.Generation}
	if !info.TransportActivated || !owner.Remote.IsValid() || owner.Generation == 0 {
		return nil, ErrOwnership
	}
	f, err := ednet.ParseFrame(wire)
	if err != nil {
		return nil, err
	}
	if f.Kind == ednet.Ack {
		return nil, nil
	}
	if f.Kind != ednet.CallUnreliable {
		return nil, ErrUnsupported
	}
	code := binary.BigEndian.Uint16(f.Action)
	var update Update
	var track TrackRequest
	var level, clientType byte
	switch code {
	case UpdateID:
		if dest.Port() != s.opts.UserMapPort {
			return nil, ErrUnsupported
		}
		update, err = ParseUpdate(f.Action)
	case TrackRequestID:
		if dest.Port() != s.opts.DensityPort {
			return nil, ErrUnsupported
		}
		track, err = ParseTrackRequest(f.Action)
	case MapRequestID:
		if dest.Port() != s.opts.DensityPort {
			return nil, ErrUnsupported
		}
		level, clientType, err = ParseMapRequest(f.Action)
	default:
		return nil, ErrUnsupported
	}
	if err != nil {
		return nil, err
	}
	// Native sends level zero. Other map levels have no recovered geometry.
	if code == MapRequestID && level != 0 {
		return nil, ErrUnsupported
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	s.expire(now)
	var action []byte
	switch code {
	case UpdateID:
		old, exists := s.players[owner]
		if !exists && len(s.players) >= s.opts.MaxPlayers {
			return nil, ErrCapacity
		}
		nat := byte(3)
		if exists {
			nat = old.NATType
		}
		s.players[owner] = Record{owner, update, nat, now.Add(s.opts.TTL)}
		return nil, nil
	case TrackRequestID:
		if old, ok := s.players[owner]; ok {
			old.NATType = byte(track.NATType)
			s.players[owner] = old
		}
		type candidate struct {
			record   Record
			distance float64
		}
		candidates := make([]candidate, 0, len(s.players))
		for key, r := range s.players {
			if key == owner || uint32(r.Update.ClientType) != track.ClientType {
				continue
			}
			dx := float64(r.Update.Position.X) - float64(track.Position.X)
			dz := float64(r.Update.Position.Z) - float64(track.Position.Z)
			candidates = append(candidates, candidate{r, dx*dx + dz*dz})
		}
		sort.Slice(candidates, func(i, j int) bool {
			a, b := candidates[i], candidates[j]
			if a.distance != b.distance {
				return a.distance < b.distance
			}
			return ownerLess(a.record.Owner, b.record.Owner)
		})
		total := uint32(len(candidates))
		n := min(len(candidates), int(track.Limit))
		rows := make([]Nearby, 0, n)
		for _, c := range candidates[:n] {
			v := c.record.Update
			rows = append(rows, Nearby{v.Position, v.Name, v.GameID, v.GameType, v.ClientType, byte(v.Level), v.CarMask, byte(v.CarType), c.record.NATType})
		}
		action, err = MarshalNearby(rows, total)
	case MapRequestID:
		var grid [Cells]byte
		var total uint32
		for _, r := range s.players {
			v := r.Update
			if v.ClientType != uint16(clientType) {
				continue
			}
			x, z := v.Position.X, v.Position.Z
			if x < 0 || z < 0 || x >= s.opts.WorldWidth || z >= s.opts.WorldDepth {
				continue
			}
			col := int(math.Floor(float64(x) / float64(s.opts.WorldWidth) * Columns))
			row := int(math.Floor(float64(z) / float64(s.opts.WorldDepth) * Rows))
			i := col*Rows + row
			if grid[i] < 255 {
				grid[i]++
			}
			total++
		}
		action = MarshalMap(grid, total)
	}
	if err != nil {
		return nil, err
	}
	return (ednet.Frame{Kind: ednet.CallUnreliable, Action: action}).MarshalBinary()
}
