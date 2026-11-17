package ttl25

import (
	"encoding/binary"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func TestMatchmakingExtensionQueriesAndParticipationCleanup(t *testing.T) {
	f := newCommunityFixture(t)
	s := f.c.sessions
	hostURL := "prudps:/address=127.0.0.1;port=40000;stream=3;sid=15;PID=1000;CID=1;type=2;RVCID=1"
	guestURL := "prudps:/address=127.0.0.1;port=40001;stream=3;sid=15;PID=1001;CID=2;type=2"
	s.registered(f.pa, f.a.PID, hostURL)
	s.registered(f.pb, f.b.PID, guestURL)
	input := sessionArgs("Extension Room", 0)
	b, err := s.register(f.pa, f.a.PID, input)
	if err != nil {
		t.Fatal(err)
	}
	gid := binary.LittleEndian.Uint32(b)
	for _, who := range []struct {
		host bool
		pid  uint32
	}{{true, f.a.PID}, {false, f.b.PID}} {
		peer := f.pa
		if !who.host {
			peer = f.pb
		}
		b, err = s.lobby(peer, who.pid, quazal.Request{Method: 11, Args: rankingString(rankingU32(nil, gid), "joined")})
		if err != nil || len(b) != 1 || b[0] != 1 {
			t.Fatal("participate", err)
		}
	}
	b, err = s.lobby(f.pa, f.a.PID, quazal.Request{Method: 26, Args: rankingString(rankingU32(nil, gid), hostURL)})
	if err != nil || b[0] != 1 {
		t.Fatal("publish", err)
	}
	query := append(rankingU32(nil, gid), 1)
	b, err = s.extension(f.pb, f.b.PID, quazal.Request{Method: 2, Args: query})
	r := rankingReader{b: b}
	if err != nil || r.u32() != 2 || r.u32() != f.a.PID || r.u32() != f.b.PID || !r.done() {
		t.Fatal("extension PID list", err)
	}
	b, err = s.extension(f.pb, f.b.PID, quazal.Request{Method: 3, Args: query})
	r = rankingReader{b: b}
	if err != nil || r.u32() != 2 {
		t.Fatal("extension details count", err)
	}
	for _, pid := range []uint32{f.a.PID, f.b.PID} {
		if r.u32() != pid || r.str() != f.c.accountName(pid) || r.str() != "joined" {
			t.Fatal("extension participant fields")
		}
	}
	if !r.done() {
		t.Fatal("extension participant tail")
	}
	ids := rankingU32(rankingU32(rankingU32(rankingU32(nil, 3), gid), 99999), gid)
	b, err = s.extension(f.pb, f.b.PID, quazal.Request{Method: 4, Args: ids})
	r = rankingReader{b: b}
	if err != nil || r.u32() != 1 || r.u32() != gid || r.u32() != 2 || r.str() != hostURL || r.str() != nativeStationURL(guestURL) || !r.done() {
		t.Fatal("grouped URL wire layout", err)
	}
	for _, m := range []uint32{2, 3} {
		if _, err := s.extension(f.pb, f.b.PID, quazal.Request{Method: m, Args: append(rankingU32(nil, gid), 2)}); err == nil {
			t.Fatal("invalid extension bool")
		}
		if _, err := s.extension(f.pb, f.b.PID, quazal.Request{Method: m, Args: append(append([]byte(nil), query...), 0)}); err == nil {
			t.Fatal("extension trailing input")
		}
	}
	leave := rankingString(rankingU32(nil, gid), "Ending session participation")
	b, err = s.extension(f.pb, f.a.PID, quazal.Request{Method: 1, Args: leave})
	if err != nil || b[0] != 0 {
		t.Fatal("other connection removed host", err)
	}
	b, err = s.extension(f.pb, f.b.PID, quazal.Request{Method: 1, Args: leave})
	if err != nil || b[0] != 1 || len(s.rooms[gid].members) != 1 || s.rooms[gid].fields["totalConnectedPlayers"] != "1" {
		t.Fatal("guest participation not removed", err)
	}
	b, err = s.extension(f.pa, f.a.PID, quazal.Request{Method: 1, Args: leave})
	if err != nil || b[0] != 1 || len(s.rooms) != 0 {
		t.Fatal("host ending participation retained room", err)
	}
	b, err = s.register(f.pa, f.a.PID, input)
	if err != nil || binary.LittleEndian.Uint32(b) == gid {
		t.Fatal("recreated native room reused stale listing", err)
	}
	if _, err = s.extension(f.pa, f.a.PID, quazal.Request{Method: 4, Args: append(ids, 0)}); err == nil {
		t.Fatal("grouped URL trailing input")
	}
}
