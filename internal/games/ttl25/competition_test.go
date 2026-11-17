package ttl25

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestCompetitionRoundRequiresExactRoundOwnerAndRanking(t *testing.T) {
	f := newCommunityFixture(t)
	s := f.c.sessions
	// Retail's initial round has a zero trailing value; the following 26/2
	// supplies Ranking 1 independently. Do not require it to be pre-linked.
	input := bytes.Clone(testRoundArgs())
	binary.LittleEndian.PutUint32(input[len(input)-4:], 0)
	b, err := s.register(f.pa, f.a.PID, input)
	if err != nil {
		t.Fatal(err)
	}
	gid := binary.LittleEndian.Uint32(b)
	args := rankingU32(rankingU32(nil, 1), gid)
	for range 2 {
		b, err = s.registerCompetitionRound(f.pa, f.a.PID, args)
		if err != nil {
			t.Fatal(err)
		}
		wantBool(t, b, true)
	}
	if s.rooms[gid].fields["competition"] != "1" {
		t.Fatal("round association not retained")
	}
	b, err = s.registerCompetitionRound(f.pb, f.b.PID, args)
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, false)
	other := f.pa
	other.ID++
	b, err = s.registerCompetitionRound(other, f.a.PID, args)
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, false)
	b, err = s.registerCompetitionRound(f.pa, f.a.PID, rankingU32(rankingU32(nil, 2), gid))
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, false)
	if _, err = s.registerCompetitionRound(f.pa, f.a.PID, append(args, 0)); err == nil {
		t.Fatal("trailing input accepted")
	}
	if _, err = s.registerCompetitionRound(f.pa, f.a.PID, args[:4]); err == nil {
		t.Fatal("truncated input accepted")
	}
}
