package commandos

import (
	"bytes"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func args(build func(*quazal.Writer)) []byte {
	var w quazal.Writer
	build(&w)
	b, err := w.Bytes()
	if err != nil {
		panic(err)
	}
	return b
}

func TestParticipantURLsCarryTheProvenRendezvousID(t *testing.T) {
	s := newSessions()
	host, joiner := testPeer(1), testPeer(2)
	id := hostRoom(t, s, host, 1000)
	joinerURL := strings.Replace(testURL(1001), "CID=1", "CID=2", 1)
	s.registered(joiner, 1001, joinerURL)
	call(t, s, joiner, 1001, 21, 11, messageArgs(id, ""))
	r := quazal.NewReader(call(t, s, joiner, 1001, 21, 16, roomArgs(id)))
	if r.U32() != 2 {
		t.Fatal("missing probe URLs")
	}
	seen := map[uint32]string{}
	for range 2 {
		url := r.String(512)
		endpoint, err := quazal.ParseStationURL(url)
		if err != nil {
			t.Fatal(err)
		}
		seen[endpoint.PID] = endpoint.Fields["RVCID"]
	}
	if !r.Done() || seen[1000] != "1" || seen[1001] != "2" {
		t.Fatalf("RVCID omitted or mixed: %+v", seen)
	}
	if targets, err := s.probe(joiner, 1001, []string{testURL(1000), quazal.WithRVCID(testURL(1000))}); err != nil || len(targets) != 1 || targets[0] != host {
		t.Fatal("probe aliases changed target authority", err)
	}
	for _, url := range []string{testURL(1000) + ";RVCID=999", strings.Replace(testURL(1000), "CID=1", "CID=999", 1)} {
		if !bytes.Equal(call(t, s, host, 1000, 21, 26, messageArgs(id, url)), []byte{0}) {
			t.Fatal("host claimed a different rendezvous identity")
		}
	}
}
func roomArgs(id uint32) []byte { return args(func(w *quazal.Writer) { w.U32(id) }) }
func messageArgs(id uint32, text string) []byte {
	return args(func(w *quazal.Writer) { w.U32(id); w.String(text) })
}
func testPeer(id uint64) prudp.Peer {
	return prudp.Peer{ID: id, LocalPort: 30061, Addr: netip.AddrPortFrom(netip.MustParseAddr("192.0.2.10"), uint16(45000+id))}
}
func testURL(pid uint32) string {
	return "prudp:/address=192.0.2.10;port=45001;stream=3;sid=1;PID=" + strconv.FormatUint(uint64(pid), 10) + ";CID=1;type=2"
}
func sampleRoom() gathering {
	return gathering{Gathering: quazal.Gathering{MinParticipants: 1, MaxParticipants: 1, Description: "WADF2Synthetic room", Flags: 16}, PlayerCount: 1}
}
func call(t *testing.T, s *sessions, peer prudp.Peer, pid uint32, protocol byte, method uint32, b []byte) []byte {
	t.Helper()
	reply, err := s.handle(peer, pid, quazal.Request{Protocol: protocol, Method: method, Args: b})
	if err != nil {
		t.Fatal(err)
	}
	return reply
}
func hostRoom(t *testing.T, s *sessions, peer prudp.Peer, pid uint32) uint32 {
	t.Helper()
	s.registered(peer, pid, testURL(pid))
	g := sampleRoom()
	b, _ := g.encode()
	reply := call(t, s, peer, pid, 21, 1, b)
	r := quazal.NewReader(reply)
	id := r.U32()
	if !r.Done() || id == 0 {
		t.Fatal("invalid register reply")
	}
	for _, op := range []struct {
		method uint32
		b      []byte
	}{{11, messageArgs(id, "")}, {26, messageArgs(id, "")}, {30, args(func(w *quazal.Writer) { w.U32(id); w.U32(2) })}} {
		if !bytes.Equal(call(t, s, peer, pid, 21, op.method, op.b), []byte{1}) {
			t.Fatalf("host step %d failed", op.method)
		}
	}
	return id
}
func TestLobbyLifecycleSearchAndHostAuthority(t *testing.T) {
	s := newSessions()
	host, joiner := testPeer(1), testPeer(2)
	id := hostRoom(t, s, host, 1000)
	s.registered(joiner, 1001, testURL(1001))
	query := func(offset uint32) []byte {
		return args(func(w *quazal.Writer) { w.String("^W...."); w.U32(offset); w.U32(10) })
	}
	for _, offset := range []uint32{0, 0xffffffff} {
		r := quazal.NewReader(call(t, s, joiner, 1001, 21, 19, query(offset)))
		if r.U32() != 1 {
			t.Fatal("initial page sentinel failed")
		}
		any := r.Any(2048)
		if !r.Done() {
			t.Fatal("search shape")
		}
		var w quazal.Writer
		w.Any(any)
		b, _ := w.Bytes()
		g, err := decodeGathering(b)
		if err != nil || g.ID != id || g.OwnerPID != 1000 || g.HostPID != 1000 || g.State != 2 || g.PlayerCount != 1 || g.MaxParticipants != 1 {
			t.Fatalf("search body %+v %v", g, err)
		}
	}
	if !bytes.Equal(call(t, s, joiner, 1001, 21, 11, messageArgs(id, "join")), []byte{1}) {
		t.Fatal("join denied")
	}
	for _, request := range []quazal.Request{{Protocol: 21, Method: 13, Args: roomArgs(id)}, {Protocol: 50, Method: 2, Args: append(roomArgs(id), 1)}} {
		b, err := s.handle(joiner, 1001, request)
		if err != nil {
			t.Fatal(err)
		}
		r := quazal.NewReader(b)
		if r.U32() != 2 || r.U32() != 1000 || r.U32() != 1001 || !r.Done() {
			t.Fatal("participants not stable")
		}
	}
	r := quazal.NewReader(call(t, s, joiner, 1001, 21, 16, roomArgs(id)))
	if r.U32() != 2 {
		t.Fatal("participant URLs missing")
	}
	if r.String(512) == "" || r.String(512) == "" || !r.Done() {
		t.Fatal("URL list shape")
	}
	for _, method := range []uint32{26, 27, 30} {
		b := messageArgs(id, testURL(1001))
		if method == 30 {
			b = args(func(w *quazal.Writer) { w.U32(id); w.U32(3) })
		}
		if !bytes.Equal(call(t, s, joiner, 1001, 21, method, b), []byte{0}) {
			t.Fatal("member changed host state")
		}
	}
	g := s.rooms[id].data
	g.Description = "WATX2Updated"
	g.Mode = 1
	g.PlayerCount = 2
	g.MaxParticipants = 2
	b, _ := g.encode()
	if _, err := s.handle(joiner, 1001, quazal.Request{Protocol: 21, Method: 4, Args: b}); err == nil {
		t.Fatal("foreign update accepted")
	}
	if !bytes.Equal(call(t, s, host, 1000, 21, 4, b), []byte{1}) {
		t.Fatal("host update denied")
	}
	if s.rooms[id].data.Description != g.Description {
		t.Fatal("metadata not updated")
	}
	call(t, s, joiner, 1001, 21, 12, messageArgs(id, "leave"))
	if len(s.rooms[id].members) != 1 {
		t.Fatal("leave kept member")
	}
	s.disconnect(host, 1000)
	if s.count() != 0 {
		t.Fatal("host disconnect left stale room")
	}
}
func TestAdmissionBoundsMalformedRequestsAndProbeTargets(t *testing.T) {
	s := newSessions()
	host := testPeer(1)
	id := hostRoom(t, s, host, 1000)
	for i := uint32(1); i <= 8; i++ {
		peer := testPeer(uint64(i + 1))
		pid := 1000 + i
		s.registered(peer, pid, testURL(pid))
		b := call(t, s, peer, pid, 21, 11, messageArgs(id, ""))
		want := byte(1)
		if i == 8 {
			want = 0
		}
		if !bytes.Equal(b, []byte{want}) {
			t.Fatalf("admission %d: %x", i, b)
		}
	}
	joiner := testPeer(2)
	targets, err := s.probe(joiner, 1001, []string{testURL(1000)})
	if err != nil || len(targets) != 1 || targets[0] != host {
		t.Fatalf("probe identity: %+v %v", targets, err)
	}
	if _, err = s.probe(joiner, 1001, []string{"prudp:/address=203.0.113.9;port=7;PID=1000"}); err == nil {
		t.Fatal("arbitrary probe target accepted")
	}
	for _, url := range []string{"prudp:/address=203.0.113.9;port=45001;PID=1000", testURL(1001)} {
		if !bytes.Equal(call(t, s, host, 1000, 21, 26, messageArgs(id, url)), []byte{0}) {
			t.Fatal("unproven host endpoint accepted")
		}
	}
	before := s.count()
	for _, request := range []quazal.Request{{Protocol: 21, Method: 12, Args: append(messageArgs(id, ""), 0)}, {Protocol: 50, Method: 2, Args: append(roomArgs(id), 2)}, {Protocol: 21, Method: 19, Args: args(func(w *quazal.Writer) { w.String("^W...."); w.U32(0); w.U32(0xffffffff) })}} {
		if _, err = s.handle(host, 1000, request); err == nil {
			t.Fatal("malformed request accepted")
		}
	}
	if s.count() != before {
		t.Fatal("malformed request mutated rooms")
	}
	if _, err = s.handle(testPeer(50), 1000, quazal.Request{Protocol: 21, Method: 13, Args: roomArgs(id)}); err == nil {
		t.Fatal("unregistered generation used PID authority")
	}
	if _, err = s.handle(host, quazal.GuestPID, quazal.Request{Protocol: 21, Method: 13, Args: roomArgs(id)}); err == nil {
		t.Fatal("guest used matchmaking")
	}
	s.disconnect(joiner, 1001)
	if len(s.rooms[id].members) != 7 {
		t.Fatal("disconnect kept participant")
	}
	call(t, s, host, 1000, 21, 12, messageArgs(id, ""))
	if s.count() != 0 {
		t.Fatal("host cancellation left stale room")
	}
}
