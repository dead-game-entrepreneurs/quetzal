package ttl25

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func TestLobbyParticipationHostURLsInvitationsAndCleanup(t *testing.T) {
	f := newCommunityFixture(t)
	s := f.c.sessions
	url := func(pid uint32, port int) string {
		return fmt.Sprintf("prudps:/address=127.0.0.1;port=%d;stream=3;sid=15;PID=%d;CID=1;type=2", port, pid)
	}
	s.registered(f.pa, f.a.PID, url(f.a.PID, 40000))
	s.registered(f.pb, f.b.PID, url(f.b.PID, 40001))
	b, err := s.register(f.pa, f.a.PID, sessionArgs("Private", 1))
	if err != nil {
		t.Fatal(err)
	}
	gid := binary.LittleEndian.Uint32(b)
	op := func(pid uint32, method uint32, args []byte) []byte {
		t.Helper()
		peer := f.pa
		if pid == f.b.PID {
			peer = f.pb
		}
		b, err := s.operation(peer, pid, quazal.Request{Protocol: 21, Method: method, Args: args})
		if err != nil {
			t.Fatalf("lobby %d: %v", method, err)
		}
		return b
	}
	wantBool(t, op(f.a.PID, 11, rankingString(rankingU32(nil, gid), "")), true)
	wantBool(t, op(f.b.PID, 11, rankingString(rankingU32(nil, gid), "")), false)
	invite := rankingU32(rankingU32(rankingU32(nil, gid), 1), f.b.PID)
	invite = rankingString(invite, "")
	wantBool(t, op(f.a.PID, 5, invite), true)
	b = op(f.b.PID, 24, append(rankingU32(nil, 0), rankingU32(nil, 25)...))
	r := rankingReader{b: b}
	if r.u32() != 1 || r.str() != "TTLSession" {
		t.Fatal("invited gathering list")
	}
	wantBool(t, op(f.b.PID, 6, rankingString(rankingU32(nil, gid), "")), true)
	wantBool(t, op(f.b.PID, 26, rankingString(rankingU32(nil, gid), url(f.b.PID, 40001))), false)
	hostURL := url(f.a.PID, 40000) + ";RVCID=1"
	wantBool(t, op(f.a.PID, 26, rankingString(rankingU32(nil, gid), hostURL)), true)
	r = rankingReader{b: op(f.b.PID, 28, rankingU32(nil, gid))}
	if !r.boolean() || r.strLimit(512) != hostURL || !r.done() {
		t.Fatal("host URL reply")
	}
	r = rankingReader{b: op(f.b.PID, 13, rankingU32(nil, gid))}
	if r.u32() != 2 || r.u32() != f.a.PID || r.u32() != f.b.PID || !r.done() {
		t.Fatal("participants")
	}
	r = rankingReader{b: op(f.b.PID, 15, rankingU32(nil, gid))}
	if r.u32() != 2 || r.u32() != f.a.PID || r.str() != "Alice" || r.str() != "" || r.u32() != f.b.PID || r.str() != "Bob" || r.str() != "" || !r.done() {
		t.Fatal("detailed participants")
	}
	// Hosts only receive an ID on registration, so updates may retain zero owner fields.
	update := sessionArgs("Private updated", 1)
	binary.LittleEndian.PutUint32(update[21:], gid)
	wantBool(t, op(f.a.PID, 4, update), true)
	updated := s.rooms[gid].data.Payload
	if binary.LittleEndian.Uint32(updated[8:]) != f.a.PID || binary.LittleEndian.Uint32(updated[12:]) != f.a.PID {
		t.Fatal("update lost authenticated owner/host")
	}
	if s.rooms[gid].fields["totalConnectedPlayers"] != "1" || binary.LittleEndian.Uint32(updated[len(updated)-13:]) != 1 {
		t.Fatal("native host count overwritten by stale backend memberships")
	}
	wantBool(t, op(f.b.PID, 4, update), false)
	wantBool(t, op(f.b.PID, 12, rankingString(rankingU32(nil, gid), "")), true)
	s.disconnect(f.pa, f.a.PID)
	if len(s.rooms) != 0 || len(s.invites) != 0 {
		t.Fatal("host disconnect left lobby state")
	}
}

func TestRoundSchemaAndURLIdentityValidation(t *testing.T) {
	f := newCommunityFixture(t)
	var body []byte
	for range 8 {
		body = rankingU32(body, 0)
	}
	body = rankingString(body, "")
	body = append(body, 0)
	for _, v := range []uint32{13, 0, 240} {
		body = rankingU32(body, v)
	}
	body = rankingString(body, "Test Level")
	body = rankingU32(body, 1)
	args := rankingAny(nil, quazal.AnyData{Type: "TTLRound", Payload: append(rankingU32(nil, uint32(len(body))), body...)})
	b, err := f.c.sessions.register(f.pa, f.a.PID, args)
	if err != nil || len(b) != 4 {
		t.Fatal("round registration", err)
	}
	gid := binary.LittleEndian.Uint32(b)
	b, err = f.c.sessions.lobby(f.pa, f.a.PID, quazal.Request{Method: 11, Args: rankingString(rankingU32(nil, gid), "")})
	if err != nil || !bytes.Equal(b, []byte{1}) {
		t.Fatal("round participation", err)
	}
	for _, url := range []string{
		"http://example.com", "prudps:/address=127.0.0.1;port=1;PID=1001",
		"prudps:/address=0.0.0.0;port=1;PID=1000", "prudps:/address=127.0.0.1;port=0;PID=1000",
		"prudps:/address=127.0.0.1;port=1;PID=1000;PID=1000",
	} {
		if validSessionURL(url, f.a.PID) {
			t.Fatal("invalid URL accepted")
		}
	}
}
