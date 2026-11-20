package ttl25

// Independent implementation of the original statistics FAQ's published rules.
// Research credit and the formula ambiguity are documented in
// docs/games/25tolife/rankings.md. No 25tl-rdv source is incorporated here.

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

const initialRating = 1600

func validRating(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= math.MaxInt32
}

func validateRatingRounds(rounds map[string]ratingRound) error {
	if len(rounds) > 100000 {
		return errors.New("too many stored rating rounds")
	}
	for key, round := range rounds {
		if key == "" || round.Expires < 0 || len(round.Expected) > 32 || len(round.Before) > 32 || len(round.Personal) > 32 || len(round.Skipped) > 32 || len(round.Clans) > 32 {
			return errors.New("invalid stored rating round")
		}
		if round.Done {
			if len(round.Expected)+len(round.Before)+len(round.Personal)+len(round.Skipped)+len(round.Clans) != 0 {
				return errors.New("completed rating round retains reports")
			}
			continue
		}
		seen := map[uint32]bool{}
		for _, pid := range round.Expected {
			if _, ok := round.Before[pid]; !ok || seen[pid] {
				return errors.New("invalid expected rating participant")
			}
			seen[pid] = true
		}
		for pid, rating := range round.Before {
			if pid < 1000 || !validRating(rating) {
				return errors.New("invalid baseline rating")
			}
		}
		for pid, values := range round.Personal {
			if _, ok := round.Before[pid]; !ok || len(values) != 87 || !finiteValues(values) {
				return errors.New("invalid pending personal statistics")
			}
		}
		for pid := range round.Skipped {
			if _, ok := round.Before[pid]; !ok {
				return errors.New("invalid skipped rating participant")
			}
		}
		for pid := range round.Clans {
			if _, ok := round.Before[pid]; !ok {
				return errors.New("invalid rating clan participant")
			}
		}
	}
	return nil
}

type ratingRound struct {
	Expires  int64                `json:"expires"`
	Ready    bool                 `json:"ready"`
	Done     bool                 `json:"done"`
	Expected []uint32             `json:"expected,omitempty"`
	Before   map[uint32]float64   `json:"before,omitempty"`
	Clans    map[uint32]uint32    `json:"clans,omitempty"`
	Personal map[uint32][]float32 `json:"personal,omitempty"`
	Skipped  map[uint32]bool      `json:"skipped,omitempty"`
}

func playerRating(stats playerStatistics) float64 {
	if stats.Rating == nil {
		return initialRating
	}
	return *stats.Rating
}

func clanSkill(clan clanRecord) int32 {
	if clan.ID == 0 {
		return 0
	}
	if clan.Rating == nil {
		return initialRating
	}
	return *clan.Rating
}

func (c *community) playerRank(pid uint32) uint32 {
	rating := playerRating(c.state.Stats[pid])
	rank := uint32(1)
	for _, stats := range c.state.Stats {
		if playerRating(stats) > rating {
			rank++
		}
	}
	return rank
}

func (c *community) clanRank(cid uint32) uint32 {
	clan, exists := c.state.Clans[cid]
	if !exists {
		return 0
	}
	rank := uint32(1)
	for _, other := range c.state.Clans {
		if clanSkill(other) > clanSkill(clan) {
			rank++
		}
	}
	return rank
}

// SkillIcon describes the stock lobby selection: top-ten numbers take priority
// over rating stars. The stock client renders the supplied rating/rank itself.
func SkillIcon(rank uint32, rating float64) (number, stars int) {
	if rank >= 1 && rank <= 10 {
		return int(rank), 0
	}
	for i, threshold := range []float64{1600, 1700, 1800, 2000, 2200} {
		if rating >= threshold {
			stars = i + 1
		}
	}
	return 0, stars
}

// roundPerformance consumes the 87-value PERSONAL report, not the 102-value
// browser record. Arrests already count as kills, so the extra award is +1.
func roundPerformance(v []float32) (score float64, team int, valid bool) {
	if len(v) != 87 || !finiteValues(v) {
		return 0, 0, false
	}
	for _, value := range v[1:] {
		if value < 0 {
			return 0, 0, false
		}
	}
	law := v[1]+v[2]+v[3] > 0
	criminal := v[4]+v[5]+v[6] > 0
	if law == criminal {
		return 0, 0, false
	}
	team = 1
	if criminal {
		team = 2
	}
	f := func(i int) float64 { return float64(v[i]) }
	score = 2*(f(7)+f(8)-f(15)-f(16)) - f(9) - f(10) - f(11) - f(12) - f(19) + f(21) + 2*f(31) + 5*(f(29)+f(30))
	return score, team, true
}

// ratingRoster is only available for stock TTLRounds explicitly associated
// with Ranking 1 by 26/2. A staging room cannot manufacture a rated round.
func (s *sessionRegistry) ratingRoster(key string) ([]uint32, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !strings.HasPrefix(key, s.runtime+":") {
		return nil, false
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(key, s.runtime+":"), 10, 32)
	if err != nil {
		return nil, false
	}
	room, ok := s.rooms[uint32(n)]
	if !ok {
		room, ok = s.finished[uint32(n)].room, s.finished[uint32(n)].room.data.Type != ""
	}
	if !ok || room.data.Type != "TTLRound" || room.fields["competition"] != "1" || room.fields["rated"] != "1" {
		return nil, false
	}
	ids := make([]uint32, 0, len(room.reportPIDs))
	for pid := range room.reportPIDs {
		ids = append(ids, pid)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, true
}

func (c *community) ensureStatistics(pid uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pid < 1000 {
		return nil
	}
	if _, exists := c.state.Stats[pid]; exists {
		return nil
	}
	_, err := c.transaction(func(s *communityState) ([]byte, error) {
		s.Stats[pid] = playerStatistics{Values: make([]float32, 102)}
		return nil, nil
	})
	return err
}

func (c *community) prepareRating(s *communityState, key string, members []uint32) (ratingRound, error) {
	if round, ok := s.RatingRounds[key]; ok {
		if !round.Done {
			if round.Before == nil {
				round.Before = map[uint32]float64{}
			}
			if round.Clans == nil {
				round.Clans = map[uint32]uint32{}
			}
			if round.Personal == nil {
				round.Personal = map[uint32][]float32{}
			}
			if round.Skipped == nil {
				round.Skipped = map[uint32]bool{}
			}
			for _, pid := range members {
				if _, exists := round.Before[pid]; !exists {
					round.Before[pid] = playerRating(s.Stats[pid])
					if clan, ok := c.clanOf(s, pid); ok {
						round.Clans[pid] = clan.ID
					}
				}
			}
		}
		return round, nil
	}
	if len(s.RatingRounds) >= 100000 {
		return ratingRound{}, errors.New("rating round capacity reached")
	}
	round := ratingRound{Expires: c.now().Add(2 * time.Minute).Unix(), Before: map[uint32]float64{}, Clans: map[uint32]uint32{}, Personal: map[uint32][]float32{}, Skipped: map[uint32]bool{}}
	for _, pid := range members {
		round.Before[pid] = playerRating(s.Stats[pid])
		if clan, ok := c.clanOf(s, pid); ok {
			round.Clans[pid] = clan.ID
		}
	}
	if s.RatingRounds == nil {
		s.RatingRounds = map[string]ratingRound{}
	}
	return round, nil
}

func settleRating(s *communityState, key string, now int64) {
	round := s.RatingRounds[key]
	if round.Done {
		return
	}
	complete := round.Ready
	for _, pid := range round.Expected {
		if _, ok := round.Personal[pid]; !ok && !round.Skipped[pid] {
			complete = false
		}
	}
	if !complete && now < round.Expires {
		return
	}
	type entry struct {
		pid   uint32
		score float64
		team  int
	}
	var players []entry
	if round.Ready {
		for _, pid := range round.Expected {
			if score, team, ok := roundPerformance(round.Personal[pid]); ok {
				players = append(players, entry{pid, score, team})
			}
		}
	}
	// Calculate every delta against the same frozen ratings before writing any.
	deltas := map[uint32]float64{}
	for _, player := range players {
		var difference float64
		for _, opponent := range players {
			if opponent.team == player.team {
				continue
			}
			actual := .5
			if player.score > opponent.score {
				actual = 1
			} else if player.score < opponent.score {
				actual = 0
			}
			expected := 1 / (1 + math.Pow(10, (round.Before[opponent.pid]-round.Before[player.pid])/400))
			difference += actual - expected
		}
		deltas[player.pid] = (16 + float64(len(players))) * difference
	}
	for _, player := range players {
		stats := s.Stats[player.pid]
		before := playerRating(stats)
		after := math.Round(max(0, min(float64(math.MaxInt32), before+deltas[player.pid])))
		stats.Rating = &after
		s.Stats[player.pid] = stats
		if cid := round.Clans[player.pid]; cid != 0 {
			if clan, ok := s.Clans[cid]; ok {
				value := clanSkill(clan)
				if after > before && value < math.MaxInt32 {
					value++
				} else if after < before && value > 0 {
					value--
				}
				clan.Rating = &value
				s.Clans[cid] = clan
			}
		}
	}
	// Retain a durable completion marker; retries cannot rate this round twice.
	s.RatingRounds[key] = ratingRound{Done: true, Expires: round.Expires}
}

func (c *community) settleExpiredRatings() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now().Unix()
	due := false
	for _, round := range c.state.RatingRounds {
		if !round.Done && round.Expires <= now {
			due = true
			break
		}
	}
	if !due {
		return nil
	}
	_, err := c.transaction(func(s *communityState) ([]byte, error) {
		for key, round := range s.RatingRounds {
			if !round.Done && round.Expires <= now {
				settleRating(s, key, now)
			}
		}
		return nil, nil
	})
	return err
}
