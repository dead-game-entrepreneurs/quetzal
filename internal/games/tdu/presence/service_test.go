package presence

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/nativeproxy"
)

func info(port uint16, generation uint64) nativeproxy.SessionInfo {
	return nativeproxy.SessionInfo{Remote: netip.AddrPortFrom(netip.MustParseAddr("192.0.2.10"), port), ID: 1, Generation: generation, TransportActivated: true}
}
func call(t *testing.T, s *Service, i nativeproxy.SessionInfo, port uint16, a []byte) ([]byte, error) {
	t.Helper()
	b, e := (ednet.Frame{Kind: ednet.CallUnreliable, Action: a}).MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.Handle(context.Background(), i, netip.AddrPortFrom(netip.MustParseAddr("192.0.2.1"), port), b)
	if e != nil || r == nil {
		return r, e
	}
	f, e := ednet.ParseFrame(r)
	if e != nil {
		t.Fatal(e)
	}
	if f.Kind != ednet.CallUnreliable {
		t.Fatalf("wrong response envelope %x", f.Kind)
	}
	return f.Action, nil
}
func publish(t *testing.T, s *Service, i nativeproxy.SessionInfo, v Update) {
	t.Helper()
	a, e := MarshalUpdate(v)
	if e != nil {
		t.Fatal(e)
	}
	r, e := call(t, s, i, 9004, a)
	if e != nil || r != nil {
		t.Fatalf("publish reply %x %v", r, e)
	}
}
func service(t *testing.T, now *time.Time, max int) *Service {
	t.Helper()
	s, e := New(Options{DensityPort: 9003, UserMapPort: 9004, TTL: time.Second, MaxPlayers: max, Now: func() time.Time { return *now }})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestOwnershipExpiryAndBounds(t *testing.T) {
	now := time.Unix(100, 0)
	s := service(t, &now, 2)
	a, b := info(10, 1), info(11, 2)
	v := Update{UserID: 7, Name: "first", ClientType: 2, Position: Position{10, 3, 10}}
	publish(t, s, a, v)
	v.Name = "second"
	v.Position.X = 20
	publish(t, s, b, v)
	records := s.Snapshot()
	if len(records) != 2 || records[0].Update.Name != "first" {
		t.Fatalf("claimed PID overwrote owner: %+v", records)
	}
	changedGeneration := a
	changedGeneration.Generation = 3
	s.DropOwner(changedGeneration)
	if len(s.Snapshot()) != 2 {
		t.Fatal("generation collision")
	}
	c := info(12, 3)
	action, _ := MarshalUpdate(v)
	if _, e := call(t, s, c, 9004, action); !errors.Is(e, ErrCapacity) {
		t.Fatalf("capacity %v", e)
	}
	q, _ := MarshalTrackRequest(TrackRequest{ClientType: 2, Limit: 16, Position: Position{}, NATType: 2})
	now = now.Add(500 * time.Millisecond)
	if _, e := call(t, s, a, 9003, q); e != nil {
		t.Fatal(e)
	}
	if s.Snapshot()[0].NATType != 2 {
		t.Fatal("NAT metadata not retained")
	}
	now = now.Add(500 * time.Millisecond)
	if len(s.Snapshot()) != 0 {
		t.Fatal("queries extended publication expiry")
	}
	publish(t, s, c, v)
	s.OnDisconnect(c)
	if len(s.Snapshot()) != 0 {
		t.Fatal("disconnect retained presence")
	}
	inactive := info(13, 4)
	inactive.TransportActivated = false
	if _, e := call(t, s, inactive, 9004, action); !errors.Is(e, ErrOwnership) {
		t.Fatalf("unactivated transport %v", e)
	}
}
func TestNearbySortingAndMap(t *testing.T) {
	now := time.Unix(100, 0)
	s := service(t, &now, 8)
	owner := info(10, 1)
	publish(t, s, owner, Update{Name: "self", ClientType: 2, Position: Position{1, 0, 1}})
	publish(t, s, info(11, 2), Update{Name: "far", ClientType: 2, GameID: 30, Position: Position{StockWidth - 1, 7, StockDepth - 1}})
	publish(t, s, info(12, 3), Update{Name: "near", ClientType: 2, GameID: 20, Position: Position{100, 4, 100}})
	publish(t, s, info(13, 4), Update{Name: "othertype", ClientType: 3, Position: Position{50, 0, 50}})
	publish(t, s, info(14, 5), Update{Name: "offmap", ClientType: 2, Position: Position{-1, 0, 100}})
	q, _ := MarshalTrackRequest(TrackRequest{ClientType: 2, Limit: 1, Position: Position{100, 0, 100}})
	reply, e := call(t, s, owner, 9003, q)
	if e != nil {
		t.Fatal(e)
	}
	if binary.BigEndian.Uint16(reply) != TrackReplyID || reply[2] != 1 || string(reply[17:21]) != "near" || binary.BigEndian.Uint32(reply[len(reply)-4:]) != 3 {
		t.Fatalf("nearby %x", reply)
	}
	reply, e = call(t, s, owner, 9003, []byte{0x55, 0xae, 0, 2})
	if e != nil {
		t.Fatal(e)
	}
	if len(reply) != Cells+8 || binary.BigEndian.Uint16(reply) != MapReplyID || binary.BigEndian.Uint16(reply[2:]) != Cells {
		t.Fatalf("map size/header %x", reply[:4])
	}
	grid := reply[4 : 4+Cells]
	if grid[0] != 2 || grid[Cells-1] != 1 || binary.BigEndian.Uint32(reply[len(reply)-4:]) != 3 {
		t.Fatalf("map content first=%d last=%d total=%d", grid[0], grid[Cells-1], binary.BigEndian.Uint32(reply[len(reply)-4:]))
	}
	// A level with no recovered geometry is explicitly unsupported.
	if _, e = call(t, s, owner, 9003, []byte{0x55, 0xae, 1, 2}); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
	regs := s.Registrations()
	if regs[MapRequestID] != 9003 || regs[UpdateID] != 9004 || len(regs) != 3 {
		t.Fatal(regs)
	}
}
func TestWrongPortAndWrapper(t *testing.T) {
	now := time.Now()
	s := service(t, &now, 1)
	a := info(1, 1)
	action, _ := MarshalUpdate(Update{})
	if _, e := call(t, s, a, 9003, action); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
	b, _ := (ednet.Frame{Kind: ednet.Call, RequestID: 1, Action: action}).MarshalBinary()
	if _, e := s.Handle(context.Background(), a, netip.MustParseAddrPort("192.0.2.1:9004"), b); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
	if len(s.Snapshot()) != 0 {
		t.Fatal("invalid request mutated state")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.Handle(ctx, a, netip.MustParseAddrPort("192.0.2.1:9004"), b); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
