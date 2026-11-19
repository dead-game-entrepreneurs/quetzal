package ttl25

import (
	"math"
	"testing"
)

func TestRoundStatisticsAccumulateAcrossRoundsAndReload(t *testing.T) {
	f := newCommunityFixture(t)
	c := f.c
	// The stock UI reads kills from raw indexes 9+11 and derives K/D from
	// indexes 24+25. Different round chunks must add to career counters.
	rounds := []struct{ kills, deaths float32 }{{16, 6}, {7, 12}, {7, 7}}
	for i, round := range rounds {
		key := []string{"runtime:10001", "runtime:10002", "runtime:10003"}[i]
		aggregate, personal := make([]float32, 15), make([]float32, 87)
		aggregate[7], aggregate[11] = 1, round.kills
		personal[25-15] = round.deaths
		personal[43-15], personal[85-15] = 240, 240
		for range 2 { // A duplicate report remains a no-op for each chunk.
			_, err := c.transaction(func(s *communityState) ([]byte, error) {
				if err := recordStats(s, f.a.PID, key+":gathering", 0, aggregate, int64(i)); err != nil {
					return nil, err
				}
				return nil, recordStats(s, f.a.PID, key+":personal", 15, personal, int64(i))
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	loaded, err := openCommunity(c.path, f.store, newSessions())
	if err != nil {
		t.Fatal(err)
	}
	v := loaded.state.Stats[f.a.PID].Values
	if v[9]+v[11] != 30 || v[24]+v[25] != 25 || v[7] != 3 || v[43] != 720 || v[85] != 720 || len(loaded.state.Reports) != 6 {
		t.Fatal("career counters did not survive accumulation, retries and reload", v)
	}
	changed := make([]float32, 15)
	changed[11] = 100
	if err := recordStats(&loaded.state, f.a.PID, "runtime:10003:gathering", 0, changed, 99); err == nil {
		t.Fatal("conflicting retry changed an already counted round")
	}
	if loaded.state.Stats[f.a.PID].Values[11] != 30 {
		t.Fatal("conflicting retry changed the total")
	}
}

func TestStatisticsOverflowDoesNotPublishCountersOrReceipt(t *testing.T) {
	s := initialCommunity()
	first := make([]float32, 15)
	first[11] = math.MaxFloat32
	if err := recordStats(&s, 1000, "first:gathering", 0, first, 1); err != nil {
		t.Fatal(err)
	}
	if err := recordStats(&s, 1000, "second:gathering", 0, first, 2); err == nil {
		t.Fatal("nonfinite total accepted")
	}
	if s.Stats[1000].Values[11] != math.MaxFloat32 || s.Stats[1000].Updated != 1 || len(s.Reports) != 1 {
		t.Fatal("failed accumulation mutated counters or receipts")
	}
}
