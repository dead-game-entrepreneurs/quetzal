package rsvpsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"sync"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// Statistics. Every stat record (0x08ba6b38) carries two float vectors. The
// stats screen (StatsState, state 0x22; table helper 0x08bc1c1c/0x08bc1f2c)
// reads the first as rating [0] (change [2]), rank [3] (change [4]) and three
// stat columns [5..7], and the second as play time in minutes [0]. The page
// filler 0x08935600 shows Rank, Name, Rating, Accuracy ([6] / [7] × 100) and
// Play Time, so [6] and [7] are hits and shots.
//
// The 60/12 reports use the same eight values per player, filled by the
// game: [0] the rounds the player won in the round set (2 and 1 after three
// rounds), [5] always 0 so far, [6] hits and [7] shots, with the rest 0 and
// an empty second vector. The report's u32 is the play time in seconds.
const (
	statValues    = 8
	statSet       = 1 // first argument of every statistics call
	startRating   = 1000
	ratingK       = 32
	maxPlaySecond = 3600
	maxStatsBytes = 8 << 20
	maxRange      = 100
)

// career is one player's accumulated statistics.
type career struct {
	Matches uint32  `json:"matches"`
	Seconds uint64  `json:"seconds"`
	Won     float64 `json:"rounds_won"`
	Stat5   float64 `json:"stat5"`
	Hits    float64 `json:"hits"`
	Shots   float64 `json:"shots"`
	Rating  float64 `json:"rating"`
	Change  float64 `json:"last_change"`
}

// careers keeps every player's statistics in stats.json.
type careers struct {
	mu      sync.Mutex
	path    string
	players map[uint32]career
	games   *games
	friends func(pid uint32) []uint32
}

func openCareers(path string, g *games, friends func(uint32) []uint32) (*careers, error) {
	c := &careers{path: path, games: g, friends: friends, players: map[uint32]career{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	var stored map[string]career
	if err := json.Unmarshal(b, &stored); err != nil {
		return nil, fmt.Errorf("rsvpsp: stats: %w", err)
	}
	for key, value := range stored {
		pid, err := strconv.ParseUint(key, 10, 32)
		if err != nil || pid == 0 || !finite(value.Won, value.Stat5, value.Hits, value.Shots, value.Rating, value.Change) {
			return nil, errors.New("rsvpsp: stats: invalid record")
		}
		c.players[uint32(pid)] = value
	}
	return c, nil
}

func finite(values ...float64) bool {
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// validResult accepts reported values that are finite, non-negative and
// below a sanity bound.
func validResult(r matchResult) bool {
	for _, v := range r.values {
		if math.IsNaN(float64(v)) || v < 0 || v > 1e6 {
			return false
		}
	}
	return true
}

// commit adds one round's results. Ratings move Elo-style on rounds won,
// each player compared with every other player of the round.
func (c *careers) commit(results []matchResult, seconds uint32) error {
	seconds = min(seconds, maxPlaySecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	next := make(map[uint32]career, len(c.players)+len(results))
	for pid, value := range c.players {
		next[pid] = value
	}
	rating := func(pid uint32) float64 {
		if value, ok := next[pid]; ok && value.Matches > 0 {
			return value.Rating
		}
		return startRating
	}
	changes := make(map[uint32]float64, len(results))
	for _, a := range results {
		for _, b := range results {
			if a.pid == b.pid {
				continue
			}
			score := 0.5
			if a.values[0] > b.values[0] {
				score = 1
			} else if a.values[0] < b.values[0] {
				score = 0
			}
			expected := 1 / (1 + math.Pow(10, (rating(b.pid)-rating(a.pid))/400))
			changes[a.pid] += ratingK * (score - expected) / float64(len(results)-1)
		}
	}
	for _, r := range results {
		value := next[r.pid]
		value.Rating = rating(r.pid) + changes[r.pid]
		value.Change = changes[r.pid]
		value.Matches++
		value.Seconds += uint64(seconds)
		value.Won += float64(r.values[0])
		value.Stat5 += float64(r.values[5])
		value.Hits += float64(r.values[6])
		value.Shots += float64(r.values[7])
		next[r.pid] = value
	}
	stored := make(map[string]career, len(next))
	for pid, value := range next {
		stored[strconv.FormatUint(uint64(pid), 10)] = value
	}
	if err := writeJSON(c.path, stored, maxStatsBytes); err != nil {
		return err
	}
	c.players = next
	return nil
}

// rankedLocked lists the players with at least one match, best rating first.
func (c *careers) rankedLocked() []uint32 {
	ids := make([]uint32, 0, len(c.players))
	for pid, value := range c.players {
		if value.Matches > 0 {
			ids = append(ids, pid)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := c.players[ids[i]], c.players[ids[j]]
		if a.Rating != b.Rating {
			return a.Rating > b.Rating
		}
		return ids[i] < ids[j]
	})
	return ids
}

// rating is a player's current rating for the friends list.
func (c *careers) rating(pid uint32) float32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if value, ok := c.players[pid]; ok && value.Matches > 0 {
		return float32(value.Rating)
	}
	return 0
}

// putLocked writes a stat record for pid as the stats screen reads it.
// Players without matches get zeros and rank 0.
func (c *careers) putLocked(b []byte, pid uint32, rank int) []byte {
	value := c.players[pid]
	rating := float32(0)
	if value.Matches > 0 {
		rating = float32(value.Rating)
	}
	b = putU32(b, pid)
	b = putString(b, c.games.name(pid))
	b = putU32(b, statusPlaying)
	b = putU32(b, statValues)
	for _, v := range [statValues]float32{rating, 0, float32(value.Change), float32(rank), 0,
		float32(value.Stat5), float32(value.Hits), float32(value.Shots)} {
		b = putU32(b, math.Float32bits(v))
	}
	b = putU32(b, 1)
	return putU32(b, math.Float32bits(float32(value.Seconds/60)))
}

// listLocked writes List<stat record> for pids, ranked by position in the
// leaderboard.
func (c *careers) listLocked(pids []uint32) []byte {
	ranked := c.rankedLocked()
	b := putU32(nil, uint32(len(pids)))
	for _, pid := range pids {
		b = c.putLocked(b, pid, slices.Index(ranked, pid)+1)
	}
	return b
}

// statsRange reads the stat set and a ResultRange (offset, size).
func statsRange(r *reader) (int, int) {
	if r.u32() != statSet {
		r.err = quazal.ErrRMC
	}
	return int(min(r.u32(), 1<<20)), int(min(r.u32(), maxRange))
}

// spark answers the statistics views (StatsState 0x08bc11b4, modes 1–4):
// 7 own (stat set, range; the client then highlights its row), 6 friends
// (stat set), 8 participants (stat set, game ID, range) and 17 leaderboard
// (stat set, range). All reply List<stat record>.
func (c *careers) spark(pid uint32, request quazal.Request) ([]byte, error) {
	r := reader{b: request.Args}
	var pids []uint32
	switch request.Method {
	case 6:
		if r.u32() != statSet {
			r.err = quazal.ErrRMC
		}
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		pids = append([]uint32{pid}, c.friends(pid)...)
		c.mu.Lock()
		defer c.mu.Unlock()
		sort.SliceStable(pids, func(i, j int) bool { return c.players[pids[i]].Rating > c.players[pids[j]].Rating })
		return c.listLocked(pids), nil
	case 8:
		if r.u32() != statSet {
			r.err = quazal.ErrRMC
		}
		id := r.u32()
		r.u32()
		size := int(min(r.u32(), maxRange))
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		pids = c.games.players(id)
		if len(pids) > size {
			pids = pids[:size]
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.listLocked(pids), nil
	case 7, 17:
		offset, size := statsRange(&r)
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		ranked := c.rankedLocked()
		if request.Method == 7 {
			// Own statistics: the part of the leaderboard around the caller,
			// or only the caller before their first match.
			at := slices.Index(ranked, pid)
			if at < 0 {
				return c.listLocked([]uint32{pid}), nil
			}
			offset = max(0, min(at-size/2, len(ranked)-size))
		}
		if offset > len(ranked) {
			offset = len(ranked)
		}
		return c.listLocked(ranked[offset:min(len(ranked), offset+size)]), nil
	}
	return nil, errUnsupported
}
