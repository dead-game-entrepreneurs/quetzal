package ttl25

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

type communityFixture struct {
	c          *community
	store      *quazal.Store
	a, b, d    quazal.Account
	pa, pb, pd prudp.Peer
}

func newCommunityFixture(t *testing.T) *communityFixture {
	t.Helper()
	dir := t.TempDir()
	store, err := quazal.OpenStore(filepath.Join(dir, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := &communityFixture{store: store}
	f.a, err = store.CreateWithContact("Alice", quazalcrypto.Key{1}, "alice@example.test")
	if err != nil {
		t.Fatal(err)
	}
	f.b, err = store.Create("Bob", quazalcrypto.Key{2})
	if err != nil {
		t.Fatal(err)
	}
	f.d, err = store.Create("David", quazalcrypto.Key{3})
	if err != nil {
		t.Fatal(err)
	}
	f.pa = prudp.Peer{ID: 1, LocalPort: 30121}
	f.pb = prudp.Peer{ID: 2, LocalPort: 30121}
	f.pd = prudp.Peer{ID: 3, LocalPort: 30121}
	f.c, err = openCommunity(filepath.Join(dir, "community.json"), store, newSessions())
	if err != nil {
		t.Fatal(err)
	}
	f.c.sessions.name = f.c.accountName
	return f
}
func communityCall(t *testing.T, c *community, peer prudp.Peer, pid, m uint32, args []byte) []byte {
	t.Helper()
	b, err := c.handle(peer, pid, quazal.Request{Protocol: 101, Method: m, Args: args})
	if err != nil {
		t.Fatalf("method %d: %v", m, err)
	}
	return b
}
func wantBool(t *testing.T, b []byte, want bool) {
	t.Helper()
	if !bytes.Equal(b, boolReply(want)) {
		t.Fatalf("bool reply %x, want %v", b, want)
	}
}
func wantStatus(t *testing.T, b []byte, want uint32) {
	t.Helper()
	if len(b) < 4 || binary.LittleEndian.Uint32(b) != want {
		t.Fatalf("status reply %x, want %d", b, want)
	}
}

func TestFriendInvitationBlockAcceptDeleteAndRestart(t *testing.T) {
	f := newCommunityFixture(t)
	c := f.c
	wantBool(t, communityCall(t, c, f.pa, f.a.PID, 11, rankingString(nil, "Bob")), true)
	for _, viewer := range []struct {
		peer         prudp.Peer
		pid, target  uint32
		relationship byte
	}{{f.pa, f.a.PID, f.b.PID, 1}, {f.pb, f.b.PID, f.a.PID, 0}} {
		r := rankingReader{b: communityCall(t, c, viewer.peer, viewer.pid, 16, rankingString(nil, "English"))}
		if r.u32() != 1 || r.u32() != viewer.target {
			t.Fatal("pending friendship identity")
		}
		r.str()
		if got := r.take(1); len(got) != 1 || got[0] != viewer.relationship {
			t.Fatal("pending friendship wire value does not select correct UI tab")
		}
	}
	alerts := rankingReader{b: communityCall(t, c, f.pb, f.b.PID, 8, nil)}
	if alerts.u32() != 1 || alerts.u32() != f.a.PID || alerts.u32() != 0 || alerts.u32() != 0 || !alerts.done() {
		t.Fatal("incoming friendship alert")
	}
	wantBool(t, communityCall(t, c, f.pb, f.b.PID, 14, append(rankingU32(nil, f.a.PID), 1)), true)
	wantBool(t, communityCall(t, c, f.pa, f.a.PID, 12, rankingU32(nil, f.b.PID)), false)
	wantBool(t, communityCall(t, c, f.pb, f.b.PID, 15, rankingU32(nil, f.a.PID)), true)
	wantBool(t, communityCall(t, c, f.pa, f.a.PID, 12, rankingU32(nil, f.b.PID)), true)
	wantBool(t, communityCall(t, c, f.pb, f.b.PID, 13, rankingU32(nil, f.a.PID)), true)
	b := communityCall(t, c, f.pa, f.a.PID, 16, rankingString(nil, "English"))
	r := rankingReader{b: b}
	if r.u32() != 1 || r.u32() != f.b.PID || r.str() != "Bob" || !bytes.Equal(r.take(1), []byte{2}) {
		t.Fatal("accepted friendship record")
	}
	r.u32()
	r.str()
	r.str()
	if r.u32() != 0 || !bytes.Equal(r.take(1), []byte{0}) || !r.done() {
		t.Fatal("friend extension layout")
	}
	reloaded, err := openCommunity(c.path, f.store, newSessions())
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.state.Friends[friendship(f.a.PID, f.b.PID)] {
		t.Fatal("friendship did not persist")
	}
	wantBool(t, communityCall(t, reloaded, f.pa, f.a.PID, 15, rankingU32(nil, f.b.PID)), true)
	if len(reloaded.state.Friends) != 0 {
		t.Fatal("friend deletion")
	}
}

func TestClanRolesInvitationsNoticesAndLeadership(t *testing.T) {
	f := newCommunityFixture(t)
	c := f.c
	create := append(rankingString(nil, "Alpha"), rankingString(nil, "ALP")...)
	wantStatus(t, communityCall(t, c, f.pa, f.a.PID, 18, create), 0)
	wantStatus(t, communityCall(t, c, f.pb, f.b.PID, 18, append(rankingString(nil, "Other"), rankingString(nil, "alp")...)), 6)
	wantStatus(t, communityCall(t, c, f.pa, f.a.PID, 23, rankingString(nil, "Bob")), 0)
	wantStatus(t, communityCall(t, c, f.pb, f.b.PID, 25, rankingString(rankingU32(nil, 1), "Alice")), 0)
	wantStatus(t, communityCall(t, c, f.pb, f.b.PID, 29, rankingU32(nil, f.a.PID)), 2)
	wantStatus(t, communityCall(t, c, f.pa, f.a.PID, 30, nil), 12)
	wantStatus(t, communityCall(t, c, f.pa, f.a.PID, 31, rankingU32(nil, f.b.PID)), 0)
	wantStatus(t, communityCall(t, c, f.pa, f.a.PID, 33, rankingU32(nil, f.b.PID)), 0)
	wantStatus(t, communityCall(t, c, f.pb, f.b.PID, 34, rankingString(nil, "Practice tonight")), 0)
	r := rankingReader{b: communityCall(t, c, f.pa, f.a.PID, 35, nil)}
	if r.u32() != 0 || r.u32() != 1 || r.str() != "Bob" || r.str() != "Practice tonight" || !r.done() {
		t.Fatal("notice layout")
	}
	wantStatus(t, communityCall(t, c, f.pb, f.b.PID, 33, rankingU32(nil, f.a.PID)), 0)
	wantStatus(t, communityCall(t, c, f.pa, f.a.PID, 29, rankingU32(nil, f.b.PID)), 0)
	if !c.state.Kicked[f.b.PID] {
		t.Fatal("kick alert state")
	}
	wantStatus(t, communityCall(t, c, f.pb, f.b.PID, 36, nil), 0)
	wantStatus(t, communityCall(t, c, f.pd, f.d.PID, 28, []byte{0}), 0)
	wantStatus(t, communityCall(t, c, f.pa, f.a.PID, 23, rankingString(nil, "David")), 10)
	wantStatus(t, communityCall(t, c, f.pa, f.a.PID, 30, nil), 0)
	if len(c.state.Clans) != 0 {
		t.Fatal("empty clan not deleted")
	}
	if err := validateCommunity(c.state); err != nil {
		t.Fatal(err)
	}
}

func reportArgs(gid uint32, ids []uint32, values [][]float32) []byte {
	b := rankingU32(nil, gid)
	b = rankingU32(b, uint32(len(ids)))
	for i, id := range ids {
		b = rankingU32(b, id)
		b = rankingU32(b, 1)
		b = rankingU32(b, uint32(len(values[i])))
		b = append(b, floatBytes(values[i])...)
	}
	return b
}
func TestStatisticsSnapshotScopesPersistenceAndReplay(t *testing.T) {
	f := newCommunityFixture(t)
	c := f.c
	s := c.sessions
	b, err := s.register(f.pa, f.a.PID, sessionArgs("Stats room", 0))
	if err != nil {
		t.Fatal(err)
	}
	gid := binary.LittleEndian.Uint32(b)
	for _, p := range []struct {
		peer prudp.Peer
		pid  uint32
	}{{f.pa, f.a.PID}, {f.pb, f.b.PID}} {
		b, err = s.lobby(p.peer, p.pid, quazal.Request{Method: 11, Args: rankingString(rankingU32(nil, gid), "")})
		if err != nil {
			t.Fatal(err)
		}
		wantBool(t, b, true)
	}
	aValues, bValues := make([]float32, 15), make([]float32, 15)
	aValues[0] = 50
	bValues[0] = 10
	args := reportArgs(gid, []uint32{f.a.PID, f.b.PID}, [][]float32{aValues, bValues})
	for range 2 {
		b, err = c.gatheringReport(f.pa, f.a.PID, args)
		if err != nil {
			t.Fatal(err)
		}
		wantBool(t, b, true)
	}
	b, err = c.gatheringReport(f.pb, f.b.PID, args)
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, true)
	personal := make([]float32, 87)
	personal[0] = 1.5
	personal[86] = 17
	pArgs := append(rankingU32(nil, 87), floatBytes(personal)...)
	for range 2 {
		if b := communityCall(t, c, f.pa, f.a.PID, 9, pArgs); len(b) != 0 {
			t.Fatal("personal report has output fields")
		}
	}
	r := rankingReader{b: communityCall(t, c, f.pa, f.a.PID, 6, append(rankingU32(nil, 1), rankingU32(nil, f.a.PID)...))}
	if r.u32() != 1 || r.str() != "Alice" || r.str() != "" {
		t.Fatal("statistics identity")
	}
	r.u32()
	r.u32()
	if r.u32() != f.a.PID {
		t.Fatal("statistics PID")
	}
	v := r.floats(102)
	if !r.done() || len(v) != 102 || v[0] != 50 || v[15] != 1.5 || v[101] != 17 {
		t.Fatal("15+87 snapshot composition")
	}
	changed := append(rankingU32(nil, 87), floatBytes(append([]float32{2}, personal[1:]...))...)
	if _, err := c.handle(f.pa, f.a.PID, quazal.Request{Protocol: 101, Method: 9, Args: changed}); err == nil {
		t.Fatal("conflicting report accepted")
	}
	if _, err := c.handle(f.pd, f.d.PID, quazal.Request{Protocol: 101, Method: 9, Args: pArgs}); err == nil {
		t.Fatal("unassociated report accepted")
	}
	personal[0] = float32(math.NaN())
	if _, err := c.handle(f.pa, f.a.PID, quazal.Request{Protocol: 101, Method: 9, Args: append(rankingU32(nil, 87), floatBytes(personal)...)}); err == nil {
		t.Fatal("nonfinite report accepted")
	}
	wantStatus(t, communityCall(t, c, f.pa, f.a.PID, 4, rankingU32(nil, f.a.PID)), 1)
	loaded, err := openCommunity(c.path, f.store, newSessions())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.state.Stats[f.a.PID].Values[101] != 17 {
		t.Fatal("statistics reload")
	}
}

func TestCommunityFailedWritesAndCorruption(t *testing.T) {
	f := newCommunityFixture(t)
	c := f.c
	if err := os.Chmod(filepath.Dir(c.path), 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Dir(c.path), 0700)
	if _, err := c.handle(f.pa, f.a.PID, quazal.Request{Protocol: 101, Method: 11, Args: rankingString(nil, "Bob")}); err == nil {
		t.Fatal("write succeeded in read-only state directory")
	}
	if len(c.state.Invites) != 0 {
		t.Fatal("failed write published invitation")
	}
	if err := os.Chmod(filepath.Dir(c.path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{}`, `{"version":2}`, `[]`, `{"version":1} {}`} {
		if err := os.WriteFile(c.path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := openCommunity(c.path, f.store, newSessions()); err == nil {
			t.Fatal("loaded invalid community state")
		}
	}
}

func TestCommunityNoClanAndAccountDataWire(t *testing.T) {
	f := newCommunityFixture(t)
	want, err := socialBootstrap(quazal.Request{Protocol: 101, Method: 17})
	if err != nil {
		t.Fatal(err)
	}
	if got := communityCall(t, f.c, f.pa, f.a.PID, 17, nil); !bytes.Equal(got, want) {
		t.Fatal("no-clan regression")
	}
	r := rankingReader{b: communityCall(t, f.c, f.pa, f.a.PID, 39, nil)}
	privateLen := r.u32()
	if privateLen != 12 {
		t.Fatal("default private record size")
	}
	r.take(int(privateLen))
	if r.u32() != 4 || r.u32() != 0 || r.str() != "alice@example.test" || !r.done() {
		t.Fatal("account-info record layout/contact")
	}
}

func TestAllDocumentedTitleMethodShapes(t *testing.T) {
	f := newCommunityFixture(t)
	c := f.c
	s := c.sessions
	b, err := s.register(f.pa, f.a.PID, sessionArgs("Protocol coverage", 0))
	if err != nil {
		t.Fatal(err)
	}
	gid := binary.LittleEndian.Uint32(b)
	b, err = s.lobby(f.pa, f.a.PID, quazal.Request{Method: 11, Args: rankingString(rankingU32(nil, gid), "")})
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, true)
	u := func(v ...uint32) []byte {
		var b []byte
		for _, n := range v {
			b = rankingU32(b, n)
		}
		return b
	}
	rangeArgs := u(0, 25)
	args := map[uint32][]byte{
		1: append(rankingString(nil, "Test Level"), rangeArgs...),
		2: sessionSearch(publicSessionQuery, 0, 25),
		3: u(0, 100, 0, 25), 4: u(f.a.PID), 5: u(25), 6: u(1, f.a.PID), 7: u(f.a.PID, 25), 8: nil,
		9: append(u(87), floatBytes(make([]float32, 87))...), 10: rankingString(nil, "Room name"),
		11: rankingString(nil, "Bob"), 12: u(f.b.PID), 13: u(f.b.PID), 14: append(u(f.b.PID), 0), 15: u(f.b.PID), 16: rankingString(nil, "English"), 17: nil,
		18: append(rankingString(nil, "Coverage Clan"), rankingString(nil, "COV")...), 19: nil, 20: u(f.a.PID), 21: u(1), 22: nil, 23: rankingString(nil, "Bob"), 24: rankingString(nil, "Bob"),
		25: rankingString(u(1), "Alice"), 26: rankingString(u(1), "Alice"), 27: rankingString(u(1), "Alice"), 28: {1}, 29: u(f.b.PID), 30: nil, 31: u(f.b.PID), 32: u(f.b.PID), 33: u(f.b.PID), 34: rankingString(nil, "Notice"), 35: nil, 36: nil, 37: u(25), 38: rankingString(nil, "English"), 39: nil,
	}
	for method := uint32(1); method <= 39; method++ {
		request := quazal.Request{Protocol: 101, Method: method, CallID: method, Args: args[method]}
		wire, err := quazal.EncodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := quazal.ParseRequest(wire)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := c.handle(f.pa, f.a.PID, parsed)
		if err != nil {
			t.Fatalf("documented method %d unavailable: %v", method, err)
		}
		reply, err := quazal.SuccessReply(parsed, payload)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := quazal.ParseResponse(reply)
		if err != nil || !decoded.Success || decoded.Method != method || decoded.CallID != method {
			t.Fatalf("method %d response envelope", method)
		}
		bad := request
		bad.Args = append(append([]byte(nil), request.Args...), 0xff)
		if _, err := c.handle(f.pa, f.a.PID, bad); err == nil {
			t.Fatalf("method %d accepted trailing data", method)
		}
	}
}
