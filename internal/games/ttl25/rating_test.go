package ttl25

import (
	"encoding/binary"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func performance(law bool, kills, deaths float32) []float32 {
	v := make([]float32, 87)
	if law {
		v[1], v[7], v[9] = 1, kills, deaths
	} else {
		v[4], v[8], v[10] = 1, kills, deaths
	}
	return v
}

func TestRatingUsesFAQWorkedExampleAndSurvivesReplayAndReload(t *testing.T) {
	f := newCommunityFixture(t)
	extra, err := f.store.Create("Carol", quazalcrypto.Key{4})
	if err != nil {
		t.Fatal(err)
	}
	ids := []uint32{f.a.PID, f.b.PID, f.d.PID, extra.PID}
	peers := []prudp.Peer{f.pa, f.pb, f.pd, {ID: 4, LocalPort: 30121}}
	before := []float64{1600, 2000, 1400, 1800}
	stats := [][]float32{performance(true, 2, 0), performance(true, 4, 0), performance(false, 3, 0), performance(false, 2, 1)}
	// The client's reserved first personal value cannot set its own rating.
	for _, values := range stats {
		values[0] = 99999
	}
	f.c.state.Clans[1] = clanRecord{ID: 1, Name: "Example", Abbr: "EX", Leader: f.b.PID, Members: map[uint32]bool{f.b.PID: false}}
	f.c.state.NextClan = 2
	for i, pid := range ids {
		value := before[i]
		f.c.state.Stats[pid] = playerStatistics{Values: make([]float32, 102), Rating: &value}
	}
	s := f.c.sessions
	b, err := s.register(f.pa, f.a.PID, sessionArgs("Ranked room", 0))
	if err != nil {
		t.Fatal(err)
	}
	staging := s.rooms[binary.LittleEndian.Uint32(b)]
	staging.fields["isRanked"] = "1"
	b, err = s.register(f.pa, f.a.PID, testRoundArgs())
	if err != nil {
		t.Fatal(err)
	}
	gid := binary.LittleEndian.Uint32(b)
	wantBool(t, mustRatingRegistration(t, s, f.pa, f.a.PID, gid), true)
	b, err = s.update(f.pa, f.a.PID, rankingAny(nil, s.rooms[gid].data))
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, true)
	for i, pid := range ids {
		b, err = s.lobby(peers[i], pid, quazal.Request{Method: 11, Args: rankingString(rankingU32(nil, gid), "")})
		if err != nil {
			t.Fatal(err)
		}
		wantBool(t, b, true)
	}
	aggregate := make([][]float32, 4)
	for i := range aggregate {
		aggregate[i] = make([]float32, 15)
		aggregate[i][0] = 999
	}
	args := reportArgs(gid, ids, aggregate)
	b, err = f.c.gatheringReport(f.pa, f.a.PID, args)
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, true)
	for _, i := range []int{2, 0, 3} {
		communityCall(t, f.c, peers[i], ids[i], 9, append(rankingU32(nil, 87), floatBytes(stats[i])...))
	}
	for i, pid := range ids {
		if playerRating(f.c.state.Stats[pid]) != before[i] {
			t.Fatal("rating changed before all reports arrived")
		}
	}
	loaded, err := openCommunity(f.c.path, f.store, s)
	if err != nil {
		t.Fatal(err)
	}
	f.c = loaded
	communityCall(t, f.c, peers[1], ids[1], 9, append(rankingU32(nil, 87), floatBytes(stats[1])...))
	// The published rounded example is A=1600 B=2005 C=1415 D=1780.
	for i, want := range []float64{1600, 2005, 1415, 1780} {
		if got := playerRating(f.c.state.Stats[ids[i]]); got != want {
			t.Fatalf("player%d got%v want%v", i, got, want)
		}
	}
	for i, pid := range ids {
		communityCall(t, f.c, peers[i], pid, 9, append(rankingU32(nil, 87), floatBytes(stats[i])...))
	}
	b, err = f.c.gatheringReport(f.pa, f.a.PID, args)
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, true)
	if playerRating(f.c.state.Stats[ids[1]]) != 2005 {
		t.Fatal("duplicate reports rated twice")
	}
	if clanSkill(f.c.state.Clans[1]) != 1601 {
		t.Fatal("clan did not receive exactly one point for a rating increase")
	}
	r := rankingReader{b: communityCall(t, f.c, peers[1], ids[1], 6, append(rankingU32(nil, 1), rankingU32(nil, ids[1])...))}
	if r.u32() != 1 {
		t.Fatal("missing stats row")
	}
	r.str()
	r.str()
	r.u32()
	r.u32()
	r.u32()
	v := r.floats(102)
	if !r.done() || v[0] != 2005 || v[15] != 1 {
		t.Fatal("rating/rank did not reach browser/lobby fields", v)
	}
}

func TestUnrankedRoomCannotChangeSkillRatings(t *testing.T) {
	f := newCommunityFixture(t)
	s := f.c.sessions
	if _, err := s.register(f.pa, f.a.PID, sessionArgs("Unranked", 0)); err != nil {
		t.Fatal(err)
	}
	b, err := s.register(f.pa, f.a.PID, testRoundArgs())
	if err != nil {
		t.Fatal(err)
	}
	gid := binary.LittleEndian.Uint32(b)
	wantBool(t, mustRatingRegistration(t, s, f.pa, f.a.PID, gid), true)
	if _, rated := s.ratingRoster(s.runtime + ":" + strconv.FormatUint(uint64(gid), 10)); rated {
		t.Fatal("unranked staging became rated")
	}
	aggregate := make([]float32, 15)
	b, err = f.c.gatheringReport(f.pa, f.a.PID, reportArgs(gid, []uint32{f.a.PID}, [][]float32{aggregate}))
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, true)
	communityCall(t, f.c, f.pa, f.a.PID, 9, append(rankingU32(nil, 87), floatBytes(performance(true, 50, 0))...))
	if len(f.c.state.RatingRounds) != 0 || playerRating(f.c.state.Stats[f.a.PID]) != 1600 {
		t.Fatal("unranked report changed rating")
	}
}

func mustRatingRegistration(t *testing.T, s *sessionRegistry, peer prudp.Peer, pid, gid uint32) []byte {
	t.Helper()
	b, err := s.registerCompetitionRound(peer, pid, append(rankingU32(nil, 1), rankingU32(nil, gid)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMVPArrestWeightAndSkillIconBoundaries(t *testing.T) {
	v := performance(true, 1, 0)
	v[21] = 1
	if score, _, ok := roundPerformance(v); !ok || score != 3 {
		t.Fatal("arrest's kill was counted more than once", score)
	}
	for _, tc := range []struct {
		rating float64
		stars  int
	}{{1599, 0}, {1600, 1}, {1699, 1}, {1700, 2}, {1800, 3}, {2000, 4}, {2200, 5}} {
		if number, stars := SkillIcon(11, tc.rating); number != 0 || stars != tc.stars {
			t.Fatal(tc, number, stars)
		}
	}
	if number, stars := SkillIcon(5, 2300); number != 5 || stars != 0 {
		t.Fatal("top-ten number must override stars")
	}
}

func TestPlayerAndClanRanksShareTiesAndKeepStableRowOrder(t *testing.T) {
	f := newCommunityFixture(t)
	for _, pid := range []uint32{f.a.PID, f.b.PID, f.d.PID} {
		value := float64(1700)
		if pid == f.d.PID {
			value = 1600
		}
		f.c.state.Stats[pid] = playerStatistics{Values: make([]float32, 102), Rating: &value}
	}
	if f.c.playerRank(f.a.PID) != 1 || f.c.playerRank(f.b.PID) != 1 || f.c.playerRank(f.d.PID) != 3 {
		t.Fatal("inconsistent competition ranks")
	}
	ids := f.c.rankedPlayers()
	if ids[0] != f.a.PID || ids[1] != f.b.PID {
		t.Fatal("tied display order changed")
	}
	for id := uint32(1); id <= 3; id++ {
		rating := int32(1700)
		if id == 3 {
			rating = 1600
		}
		f.c.state.Clans[id] = clanRecord{ID: id, Rating: &rating}
	}
	if f.c.clanRank(1) != 1 || f.c.clanRank(2) != 1 || f.c.clanRank(3) != 3 || f.c.clanRank(0) != 0 {
		t.Fatal("inconsistent clan ranks")
	}
}

func TestExpiredIncompleteRatingRoundDoesNotInventMissingPlayerOrRerate(t *testing.T) {
	f := newCommunityFixture(t)
	now := time.Unix(1000, 0)
	f.c.now = func() time.Time { return now }
	ids := []uint32{f.a.PID, f.b.PID, f.d.PID}
	for _, pid := range ids {
		f.c.state.Stats[pid] = playerStatistics{Values: make([]float32, 102)}
	}
	round, err := f.c.prepareRating(&f.c.state, "test:1", ids)
	if err != nil {
		t.Fatal(err)
	}
	round.Ready = true
	round.Expected = ids
	round.Personal[f.a.PID] = performance(true, 4, 0)
	round.Personal[f.b.PID] = performance(false, 1, 0)
	f.c.state.RatingRounds["test:1"] = round
	now = now.Add(2 * time.Minute)
	if err := f.c.settleExpiredRatings(); err != nil {
		t.Fatal(err)
	}
	a := playerRating(f.c.state.Stats[f.a.PID])
	if a <= 1600 || playerRating(f.c.state.Stats[f.d.PID]) != 1600 {
		t.Fatal("missing report affected ratings")
	}
	if err := f.c.settleExpiredRatings(); err != nil {
		t.Fatal(err)
	}
	if playerRating(f.c.state.Stats[f.a.PID]) != a {
		t.Fatal("expired round rated twice")
	}
	if !validRating(1600) || validRating(math.NaN()) {
		t.Fatal("rating validation")
	}
}
