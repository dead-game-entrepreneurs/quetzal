package wcc

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func testRegistry() (*registry, *time.Time) {
	now := time.Unix(1700000000, 0)
	s := newRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)), func(pid uint32) string { return "player" })
	s.now = func() time.Time { return now }
	return s, &now
}

func player(s *registry, id uint64, pid uint32) prudp.Peer {
	p := prudp.Peer{ID: id, LocalPort: SecurePort}
	s.registered(p, pid, "prudp:/address=192.0.2.1;port=3658;stream=3;sid=15;type=2;PID="+itoa(pid)+";CID=1")
	return p
}

func itoa(v uint32) string { return strconv.FormatUint(uint64(v), 10) }

func call(t *testing.T, s *registry, p prudp.Peer, pid uint32, protocol byte, method uint32, args []byte) []byte {
	t.Helper()
	var out []byte
	var err error
	if protocol == 50 {
		out, err = s.extension(p, pid, quazal.Request{Protocol: 50, Method: method, Args: args})
	} else {
		out, err = s.matchmaking(p, pid, quazal.Request{Protocol: 21, Method: method, Args: args})
	}
	if err != nil {
		t.Fatalf("%d/%d: %v", protocol, method, err)
	}
	return out
}

func TestOwnerDepartureMigrationAndExpiry(t *testing.T) {
	s, now := testRegistry()
	host, a, b := player(s, 1, 1000), player(s, 2, 1001), player(s, 3, 1002)
	gid := binary.LittleEndian.Uint32(call(t, s, host, 1000, 21, 1, wccGathering(0, 4, false)))
	for _, p := range []struct {
		peer prudp.Peer
		pid  uint32
	}{{host, 1000}, {a, 1001}, {b, 1002}} {
		call(t, s, p.peer, p.pid, 21, 11, cat(u32(gid), str("")))
	}
	// Only the owner may hand off while present.
	if !bytes.Equal(call(t, s, a, 1001, 21, 36, cat(u32(gid), u32(1), u32(1001))), []byte{0}) {
		t.Fatal("participant migrated a hosted gathering")
	}
	s.disconnect(host, 1000)
	if n := binary.LittleEndian.Uint32(call(t, s, b, 1002, 21, 17, cat(str("WCCGathering"), u32(0), u32(10)))); n != 0 {
		t.Fatal("orphaned gathering listed publicly")
	}
	if call(t, s, b, 1002, 21, 21, u32(gid))[0] != 1 {
		t.Fatal("participant lost access during migration")
	}
	// Candidates are taken in order; the departed owner is skipped.
	if !bytes.Equal(call(t, s, b, 1002, 21, 36, cat(u32(gid), u32(2), u32(1000), u32(1001))), []byte{1}) {
		t.Fatal("participant could not claim orphaned gathering")
	}
	found := call(t, s, b, 1002, 21, 21, u32(gid))
	r := reader{b: found[1:]}
	data := r.any(maxGatheringPayload)
	if binary.LittleEndian.Uint32(data.Payload[offsetOwner:]) != 1001 || binary.LittleEndian.Uint32(data.Payload[offsetHost:]) != 1001 {
		t.Fatal("migration did not restamp owner and host")
	}
	if !bytes.Equal(call(t, s, a, 1001, 21, 30, cat(u32(gid), u32(1))), []byte{1}) {
		t.Fatal("new owner lacks authority")
	}

	// A second gathering whose owner leaves is closed when nobody migrates it.
	gid2 := binary.LittleEndian.Uint32(call(t, s, b, 1002, 21, 1, wccGathering(0, 4, false)))
	call(t, s, a, 1001, 21, 11, cat(u32(gid2), str("")))
	s.disconnect(b, 1002)
	*now = now.Add(orphanGrace)
	if call(t, s, a, 1001, 21, 21, u32(gid2))[0] != 0 {
		t.Fatal("orphan survived its migration window")
	}
	// An owner leaving an empty gathering closes it at once.
	gid3 := binary.LittleEndian.Uint32(call(t, s, a, 1001, 21, 1, wccGathering(0, 4, false)))
	s.disconnect(a, 1001)
	if s.gatherings[gid3] != nil || s.gatherings[gid] != nil {
		t.Fatal("gatherings outlived their last connection")
	}
}

func TestCapacityAndValidation(t *testing.T) {
	s, _ := testRegistry()
	host, a, b := player(s, 1, 1000), player(s, 2, 1001), player(s, 3, 1002)
	gid := binary.LittleEndian.Uint32(call(t, s, host, 1000, 21, 1, wccGathering(0, 2, false)))
	call(t, s, host, 1000, 21, 11, cat(u32(gid), str("")))
	call(t, s, a, 1001, 21, 11, cat(u32(gid), str("")))
	if !bytes.Equal(call(t, s, b, 1002, 21, 11, cat(u32(gid), str(""))), []byte{0}) {
		t.Fatal("full gathering accepted a participant")
	}
	for name, payload := range map[string][]byte{
		"unknown type":  putAny(nil, quazal.AnyData{Type: "TTLSession", Payload: wccPayload()}),
		"bad body size": putAny(nil, quazal.AnyData{Type: "WCCGathering", Payload: append(wccPayload(), 0)}),
		"short":         putAny(nil, quazal.AnyData{Type: "WCCGathering", Payload: u32(0)}),
	} {
		if _, err := s.matchmaking(host, 1000, quazal.Request{Protocol: 21, Method: 1, Args: payload}); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	for range maxOwnedPerConnection - 1 {
		call(t, s, b, 1002, 21, 1, wccGathering(0, 2, false))
	}
	call(t, s, b, 1002, 21, 1, wccGathering(0, 2, false))
	if _, err := s.matchmaking(b, 1002, quazal.Request{Protocol: 21, Method: 1, Args: wccGathering(0, 2, false)}); err == nil {
		t.Fatal("connection exceeded its gathering limit")
	}
	unregistered := prudp.Peer{ID: 9, LocalPort: SecurePort}
	if _, err := s.matchmaking(unregistered, 1009, quazal.Request{Protocol: 21, Method: 17, Args: cat(str("WCCGathering"), u32(0), u32(1))}); err == nil {
		t.Fatal("unregistered connection reached matchmaking")
	}
}

func wccPayload() []byte {
	r := reader{b: wccGathering(0, 4, false)}
	return r.any(maxGatheringPayload).Payload
}

func TestProbesAndConnectionLookupRequireSharedGathering(t *testing.T) {
	s, _ := testRegistry()
	host, a, stranger := player(s, 1, 1000), player(s, 2, 1001), player(s, 3, 1002)
	gid := binary.LittleEndian.Uint32(call(t, s, host, 1000, 21, 1, wccGathering(0, 4, false)))
	call(t, s, a, 1001, 21, 11, cat(u32(gid), str("")))
	hostURL := "prudp:/address=192.0.2.1;port=3658;PID=1000"
	targets, _ := s.probeTargets(a, 1001, []string{hostURL})
	if len(targets) != 1 || targets[0] != host {
		t.Fatalf("participant probe targets %v", targets)
	}
	// Retail joiners look up and probe a listed host before participating.
	if targets, _ := s.probeTargets(stranger, 1002, []string{hostURL}); len(targets) != 1 {
		t.Fatal("prospective joiner cannot probe a listed host")
	}
	if urls, _ := s.connectionURLs(stranger, 1002, 0, 1000); len(urls) != 1 {
		t.Fatal("prospective joiner cannot look up a listed host")
	}
	// With three or more players the newcomer also asks for probes from the
	// players already seated (observed 3/1 toward a non-host participant).
	aURL := "prudp:/address=192.0.2.1;port=3658;PID=1001"
	if targets, _ := s.probeTargets(stranger, 1002, []string{aURL}); len(targets) != 1 || targets[0] != a {
		t.Fatal("prospective joiner cannot probe a seated participant")
	}
	if urls, _ := s.connectionURLs(stranger, 1002, 0, 1001); len(urls) != 1 {
		t.Fatal("prospective joiner cannot look up a seated participant")
	}
	// Players who are not at any table stay private.
	idle := player(s, 4, 1003)
	idleURL := "prudp:/address=192.0.2.1;port=3658;PID=1003"
	if targets, _ := s.probeTargets(stranger, 1002, []string{idleURL}); len(targets) != 0 {
		t.Fatal("probe toward a player at no table authorized")
	}
	if urls, _ := s.connectionURLs(stranger, 1002, 0, 1003); len(urls) != 0 {
		t.Fatal("lookup of a player at no table")
	}
	_ = idle
	if urls, _ := s.connectionURLs(host, 1000, 0, 1001); len(urls) != 1 {
		t.Fatal("host cannot look up its participant")
	}
	// 50/4 returns grouped URLs; 50/1 ends participation.
	out := call(t, s, a, 1001, 50, 4, cat(u32(1), u32(gid)))
	if binary.LittleEndian.Uint32(out) != 1 || binary.LittleEndian.Uint32(out[4:]) != gid {
		t.Fatalf("grouped URLs %x", out)
	}
	if !bytes.Equal(call(t, s, a, 1001, 50, 1, cat(u32(gid), str(""))), []byte{1}) {
		t.Fatal("EndParticipation")
	}
	if targets, _ := s.probeTargets(host, 1000, []string{aURL}); len(targets) != 0 {
		t.Fatal("probe toward a departed participant authorized")
	}
	// Once the table closes, its host is no longer reachable by strangers.
	call(t, s, host, 1000, 21, 2, u32(gid))
	if urls, _ := s.connectionURLs(stranger, 1002, 0, 1000); len(urls) != 0 {
		t.Fatal("lookup survived the gathering")
	}
}

func FuzzMatchmakingArguments(f *testing.F) {
	f.Add(uint32(1), wccGathering(0, 4, false))
	f.Add(uint32(17), cat(str("WCCGathering"), u32(0), u32(5)))
	f.Add(uint32(36), cat(u32(1000), u32(3), u32(1)))
	f.Fuzz(func(t *testing.T, method uint32, args []byte) {
		s, _ := testRegistry()
		p := player(s, 1, 1000)
		_, _ = s.matchmaking(p, 1000, quazal.Request{Protocol: 21, Method: method % 40, Args: args})
		_, _ = s.extension(p, 1000, quazal.Request{Protocol: 50, Method: method % 6, Args: args})
	})
}

// Observed retail end of game: host 21/36 → joiner, host 21/12, joiner 21/12.
func TestEndOfGameHandoffClosesTable(t *testing.T) {
	s, _ := testRegistry()
	host, joiner := player(s, 1, 1001), player(s, 2, 1000)
	gid := binary.LittleEndian.Uint32(call(t, s, host, 1001, 21, 1, wccGathering(0, 2, false)))
	// Freshly registered, the gathering has no participants and stays listed.
	if n := binary.LittleEndian.Uint32(call(t, s, joiner, 1000, 21, 17, cat(str("WCCGathering"), u32(0), u32(10)))); n != 1 {
		t.Fatal("new gathering not listed before its host participates")
	}
	call(t, s, host, 1001, 21, 11, cat(u32(gid), str("")))
	call(t, s, joiner, 1000, 21, 11, cat(u32(gid), str("")))
	if !bytes.Equal(call(t, s, host, 1001, 21, 36, cat(u32(gid), u32(1), u32(1000))), []byte{1}) {
		t.Fatal("end-of-game handoff")
	}
	call(t, s, host, 1001, 21, 12, cat(u32(gid), str("")))
	if n := binary.LittleEndian.Uint32(call(t, s, joiner, 1000, 21, 17, cat(str("WCCGathering"), u32(0), u32(10)))); n != 1 {
		t.Fatal("table closed while its new owner still participates")
	}
	call(t, s, joiner, 1000, 21, 12, cat(u32(gid), str("")))
	if n := binary.LittleEndian.Uint32(call(t, s, joiner, 1000, 21, 17, cat(str("WCCGathering"), u32(0), u32(10)))); n != 0 {
		t.Fatal("empty table still listed after the game ended")
	}
}

func TestHostLookupQueuesRateLimitedProbe(t *testing.T) {
	s, now := testRegistry()
	host, joiner := player(s, 1, 1000), player(s, 2, 1001)
	call(t, s, host, 1000, 21, 1, wccGathering(0, 4, false))
	if urls, _ := s.connectionURLs(joiner, 1001, 0, 1000); len(urls) != 1 {
		t.Fatal("lookup")
	}
	got := s.probeInstructions()
	want := s.live[connectionOf(joiner, 1001)].url + ";RVCID=1"
	if len(got) != 1 || got[0].Peer != host || got[0].URL != want {
		t.Fatalf("probe instructions %+v", got)
	}
	// Retries inside the interval do not flood the host; later ones do.
	s.connectionURLs(joiner, 1001, 0, 1000)
	if len(s.probeInstructions()) != 0 {
		t.Fatal("probe not rate limited")
	}
	*now = now.Add(probeInterval)
	s.connectionURLs(joiner, 1001, 0, 1000)
	if len(s.probeInstructions()) != 1 {
		t.Fatal("probe not repeated after the interval")
	}
	// A refused lookup queues nothing.
	stranger := player(s, 3, 1002)
	s.connectionURLs(stranger, 1002, 0, 1001)
	if len(s.probeInstructions()) != 0 {
		t.Fatal("probe queued for a refused lookup")
	}
}

// Retail WCCGathering bytes captured from PCSX2 (description "Player",
// numPlayers 1, max 2), with the server-owned ID/owner/host zeroed.
const retailTable = "43000000000000000000000000000000020002000100000000000000100000000000000007" +
	"00506c61796572000000010200000100000000000000000000000000000000000000"

func retailGathering(t *testing.T, players byte) []byte {
	t.Helper()
	payload, err := hex.DecodeString(retailTable)
	if err != nil || len(payload) != 0x47 {
		t.Fatal("fixture", err, len(payload))
	}
	payload[len(payload)-cardTableTail+cardTableNumPlayers] = players
	return putAny(nil, quazal.AnyData{Type: "WCCGathering", Payload: payload})
}

func listedPlayers(t *testing.T, s *registry, p prudp.Peer, pid uint32) (owner uint32, players uint16, n uint32) {
	t.Helper()
	r := reader{b: call(t, s, p, pid, 21, 17, cat(str("WCCGathering"), u32(0), u32(10)))}
	n = r.u32()
	if n == 0 {
		return 0, 0, 0
	}
	data := r.any(maxGatheringPayload)
	owner = binary.LittleEndian.Uint32(data.Payload[offsetOwner:])
	players = binary.LittleEndian.Uint16(data.Payload[len(data.Payload)-cardTableTail+cardTableNumPlayers:])
	return owner, players, n
}

// Observed retail sequence: host leaves an open table with 21/36 then 21/12;
// the successor sends no 21/4, so the server must correct the player count.
func TestHostLeavingKeepsTableJoinable(t *testing.T) {
	s, _ := testRegistry()
	host, joiner := player(s, 1, 1000), player(s, 2, 1001)
	gid := binary.LittleEndian.Uint32(call(t, s, host, 1000, 21, 1, retailGathering(t, 1)))
	call(t, s, host, 1000, 21, 11, cat(u32(gid), str("")))
	call(t, s, joiner, 1001, 21, 11, cat(u32(gid), str("")))
	update := retailGathering(t, 2)
	r := reader{b: update}
	data := r.any(maxGatheringPayload)
	binary.LittleEndian.PutUint32(data.Payload[offsetID:], gid)
	if !bytes.Equal(call(t, s, host, 1000, 21, 4, putAny(nil, data)), []byte{1}) {
		t.Fatal("host update")
	}
	if _, players, _ := listedPlayers(t, s, host, 1000); players != 2 {
		t.Fatalf("host-maintained count %d", players)
	}
	call(t, s, host, 1000, 21, 36, cat(u32(gid), u32(1), u32(1001)))
	// The successor is told it now owns the table (14/1, type 4000).
	calls := s.serverCalls()
	want := cat(u32(1000), u32(ownershipChanged), u32(gid), u32(1001), str(""))
	if len(calls) != 1 || calls[0].Peer != joiner || calls[0].Protocol != 14 || calls[0].Method != 1 || !bytes.Equal(calls[0].Args, want) {
		t.Fatalf("ownership notification %+v", calls)
	}
	call(t, s, host, 1000, 21, 12, cat(u32(gid), str("")))
	owner, players, n := listedPlayers(t, s, host, 1000)
	if n != 1 || owner != 1001 || players != 1 {
		t.Fatalf("after host left: n=%d owner=%d players=%d", n, owner, players)
	}
	// The former host can resolve the new host and rejoin.
	if urls, _ := s.connectionURLs(host, 1000, 0, 1001); len(urls) != 1 {
		t.Fatal("former host cannot look up the new host")
	}
	if got := s.probeInstructions(); len(got) != 1 || got[0].Peer != joiner {
		t.Fatalf("new host not asked to probe %+v", got)
	}
	if !bytes.Equal(call(t, s, host, 1000, 21, 11, cat(u32(gid), str(""))), []byte{1}) {
		t.Fatal("former host could not rejoin")
	}
	// A participant whose connection drops is removed from the count too.
	s.disconnect(host, 1000)
	if _, players, _ := listedPlayers(t, s, joiner, 1001); players != 1 {
		t.Fatalf("count after disconnect %d", players)
	}
}
