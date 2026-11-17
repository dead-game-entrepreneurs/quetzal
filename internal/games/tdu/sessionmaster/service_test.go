package sessionmaster

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/nativeproxy"
)

func service(t *testing.T, opts Options) *Service {
	t.Helper()
	if opts.Port == 0 {
		opts.Port = 9002
	}
	s, e := New(opts)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func info(n uint64) nativeproxy.SessionInfo {
	return nativeproxy.SessionInfo{Remote: owner(n).Remote, Generation: n, ID: uint16(n), TransportActivated: true}
}
func call(t *testing.T, s *Service, kind uint16, id uint32, who nativeproxy.SessionInfo, action []byte) [][]byte {
	t.Helper()
	wire, e := (ednet.Frame{Kind: kind, RequestID: id, Timeout: 1000, Action: action}).MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	frames, e := s.HandleFrames(context.Background(), who, netip.MustParseAddrPort("192.0.2.254:9002"), wire)
	if e != nil {
		t.Fatal(e)
	}
	return frames
}
func action(t *testing.T, wire []byte) []byte {
	t.Helper()
	f, e := ednet.ParseFrame(wire)
	if e != nil {
		t.Fatal(e)
	}
	if f.Kind != ednet.CallUnreliable {
		t.Fatalf("business reply must be D572: %+v", f)
	}
	return f.Action
}
func TestUnreliableCreateReliableGetUpdateDelete(t *testing.T) {
	s := service(t, Options{})
	q := request(11, 0)
	b, _ := q.MarshalBinary()
	frames := call(t, s, ednet.CallUnreliable, 0, info(1), b)
	if len(frames) != 1 {
		t.Fatal("wrong create frame count")
	}
	a := action(t, frames[0])
	if binary.BigEndian.Uint16(a) != CreateOrJoinReply || a[2] != 1 {
		t.Fatalf("create %x", a)
	}
	r := reader{b: a[3:]}
	d, e := ParseFreeRide(r.blob(512))
	if e != nil || !r.done() {
		t.Fatal("bad create descriptor")
	}
	retry := call(t, s, ednet.CallUnreliable, 0, info(1), b)
	if !bytes.Equal(frames[0], retry[0]) {
		t.Fatal("create retry changed result")
	}
	frames = call(t, s, ednet.Call, 0x11223344, info(2), MarshalID(GetInfo, d.ID))
	if len(frames) != 2 {
		t.Fatal("GetInfo requires completion and business datagrams")
	}
	complete, e := ednet.ParseFrame(frames[1])
	if e != nil || complete.Kind != ednet.Reply || complete.RequestID != 0x11223344 || len(complete.Action) != 0 {
		t.Fatalf("completion %+v %v", complete, e)
	}
	a = action(t, frames[0])
	if binary.BigEndian.Uint16(a) != GetInfoReply || binary.BigEndian.Uint32(a[2:]) != d.ID {
		t.Fatalf("GetInfo %x", a)
	}
	d.Connection = [4]uint32{8889, 0xc0000202, 8889, 11}
	update, _ := MarshalUpdate(d)
	frames = call(t, s, ednet.CallUnreliable, 0, info(1), update)
	a = action(t, frames[0])
	if len(a) != 13 || binary.BigEndian.Uint16(a) != Notify || a[6] != 0 || binary.BigEndian.Uint32(a[7:]) != 0xc0000201 || binary.BigEndian.Uint16(a[11:]) != 8889 {
		t.Fatalf("update notification %x", a)
	}
	frames = call(t, s, ednet.CallUnreliable, 0, info(2), update)
	a = action(t, frames[0])
	if a[6] != 0xff {
		t.Fatal("foreign update accepted")
	}
	frames = call(t, s, ednet.Call, 8, info(1), MarshalID(Delete, d.ID))
	if len(frames) != 1 {
		t.Fatal("delete should have only reliable completion")
	}
	frames = call(t, s, ednet.Call, 9, info(2), MarshalID(GetInfo, d.ID))
	a = action(t, frames[0])
	if !bytes.Equal(a, []byte{0x78, 0x48, 0, 0, 0, 0}) {
		t.Fatalf("missing GetInfo %x", a)
	}
}
func TestAdmissionAndUnsupportedDoNotMutate(t *testing.T) {
	denied := errors.New("verified native binding required")
	s := service(t, Options{Admit: func(context.Context, nativeproxy.SessionInfo, uint32) error { return denied }})
	b, _ := request(11, 0).MarshalBinary()
	wire, _ := (ednet.Frame{Kind: ednet.CallUnreliable, Action: b}).MarshalBinary()
	if _, e := s.HandleFrames(context.Background(), info(1), netip.AddrPort{}, wire); !errors.Is(e, denied) {
		t.Fatal(e)
	}
	if len(s.Snapshot()) != 0 {
		t.Fatal("admission rejection mutated registry")
	}
	s = service(t, Options{})
	inactive := info(1)
	inactive.TransportActivated = false
	if _, e := s.HandleFrames(context.Background(), inactive, netip.AddrPort{}, wire); !errors.Is(e, ErrOwnership) {
		t.Fatal(e)
	}
	missingGeneration := info(1)
	missingGeneration.Generation = 0
	if _, e := s.HandleFrames(context.Background(), missingGeneration, netip.AddrPort{}, wire); !errors.Is(e, ErrOwnership) {
		t.Fatal(e)
	}
	bad, _ := (ednet.Frame{Kind: ednet.CallUnreliable, Action: []byte{0xff, 0xff}}).MarshalBinary()
	if _, e := s.HandleFrames(context.Background(), info(1), netip.AddrPort{}, bad); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
	ack, _ := (ednet.Frame{Kind: ednet.Ack, RequestID: 1}).MarshalBinary()
	if frames, e := s.HandleFrames(context.Background(), info(1), netip.AddrPort{}, ack); e != nil || len(frames) != 0 {
		t.Fatal("replied to acknowledgement")
	}
	if len(s.Snapshot()) != 0 {
		t.Fatal("rejected operation created session")
	}
}
func TestMembershipAndNativeGenerationCleanup(t *testing.T) {
	s := service(t, Options{})
	_, host, _ := s.store.Create(owner(1), request(11, 0))
	publish(t, s.store, owner(1), host)
	_, _, e := s.store.Create(owner(2), request(22, 100))
	if e != nil {
		t.Fatal(e)
	}
	m, ok := s.Membership(22, 11)
	if !ok || m.SessionID != host.ID || m.CallerOwner != owner(2) || m.TargetOwner != owner(1) || m.TargetHost == nil || uint16(m.TargetHost.Connection[0]) != 8889 {
		t.Fatalf("membership %+v %v", m, ok)
	}
	reverse, ok := s.Membership(11, 22)
	if !ok || reverse.TargetHost != nil {
		t.Fatal("invented joining driver's game endpoint")
	}
	if _, ok := s.Membership(33, 11); ok {
		t.Fatal("unclaimed caller admitted")
	}
	s.OnDisconnect(info(99))
	if _, ok := s.Membership(22, 11); !ok {
		t.Fatal("different generation removed membership")
	}
	s.OnDisconnect(info(1))
	if len(s.Snapshot()) != 0 {
		t.Fatal("host disconnect retained group")
	}
	if _, ok := s.Membership(22, 11); ok {
		t.Fatal("deleted group retained membership")
	}
}
func TestSearchReliableCompletionAndSeparateResults(t *testing.T) {
	s := service(t, Options{})
	for i := uint64(1); i <= 4; i++ {
		_, d, _ := s.store.Create(owner(i), request(uint32(i), float32(i)*10000))
		publish(t, s.store, owner(i), d)
	}
	w := writer{}
	w.u16(Search)
	w.blob([]byte("FreeRideV5N37-8max"))
	w.u32(10)
	w.u32(0)
	frames := call(t, s, ednet.Call, 42, info(99), w.b)
	if len(frames) != 4 {
		t.Fatalf("bounded search datagrams%d", len(frames))
	}
	for _, frame := range frames[:len(frames)-1] {
		a := action(t, frame)
		if binary.BigEndian.Uint16(a) != SearchReply {
			t.Fatal("wrong search reply")
		}
		r := reader{b: a[6:]}
		base := r.blob(512)
		if !r.done() || len(base) < 2 || binary.BigEndian.Uint16(base) != 0x7eca {
			t.Fatal("Search must use older base V3 descriptor")
		}
	}
}

func TestClockDiscoveryHasRealClockResponse(t *testing.T) {
	s := service(t, Options{Clock: func() uint32 { return 0x65544321 }})
	frames := call(t, s, ednet.Call, 55, info(1), []byte{0x74, 0x26})
	if len(frames) != 1 {
		t.Fatal("clock response count")
	}
	f, e := ednet.ParseFrame(frames[0])
	if e != nil || f.Kind != ednet.Reply || f.RequestID != 55 || !bytes.Equal(f.Action, []byte{0x9f, 0x27, 0x65, 0x54, 0x43, 0x21}) {
		t.Fatalf("clock reply %+v %v", f, e)
	}
	frames = call(t, s, ednet.CallUnreliable, 0, info(1), []byte{0x74, 0x26})
	if !bytes.Equal(action(t, frames[0]), []byte{0x9f, 0x27, 0x65, 0x54, 0x43, 0x21}) {
		t.Fatal("unreliable clock value")
	}
	wire, _ := (ednet.Frame{Kind: ednet.Call, RequestID: 1, Action: []byte{0x74, 0x26, 0}}).MarshalBinary()
	if _, e := s.HandleFrames(context.Background(), info(1), netip.AddrPort{}, wire); !errors.Is(e, ErrMalformed) {
		t.Fatal("clock accepted arguments")
	}
	found := false
	for _, a := range SupportedActions() {
		if a == GetTime {
			found = true
		}
	}
	if !found {
		t.Fatal("clock discovery ID missing")
	}
	if len(s.Snapshot()) != 0 {
		t.Fatal("clock request mutated sessions")
	}
}
