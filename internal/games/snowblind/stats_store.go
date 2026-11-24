package snowblind

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"reflect"
	"sync"
)

const (
	maxStatsRounds     = 8192
	maxStatsStateBytes = 32 << 20
)

type statReceipt struct {
	Summary          []summaryRow       `json:"summary"`
	Personal         map[string]float32 `json:"personal,omitempty"`
	PersonalReceived bool               `json:"personal_received,omitempty"`
}
type statRound struct {
	Gathering    uint32                 `json:"gathering"`
	Mode         byte                   `json:"mode"`
	Map          uint32                 `json:"map"`
	Participants []uint32               `json:"participants"`
	Reports      map[uint32]statReceipt `json:"reports"`
}
type statsDisk struct {
	Version int                  `json:"version"`
	Rounds  map[string]statRound `json:"rounds"`
}
type statsStore struct {
	mu    sync.Mutex
	path  string
	state statsDisk
}

type playerStats struct {
	PID                                                   uint32
	Rounds, Kills, Deaths, Wins, Losses, Draws, Penalties uint64
	Counters                                              map[string]float64
	Maps                                                  map[uint32]mapStats
}
type mapStats struct{ Reports, Wins, Draws uint64 }

func openStats(path string, validPID func(uint32) bool) (*statsStore, error) {
	if path == "" || validPID == nil {
		return nil, errors.New("Snowblind stats path and account validator are required")
	}
	s := &statsStore{path: path, state: statsDisk{Version: 1, Rounds: map[string]statRound{}}}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxStatsStateBytes {
		return nil, errors.New("invalid Snowblind stats file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&s.state); err != nil {
		return nil, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF || s.state.Version != 1 || s.state.Rounds == nil || len(s.state.Rounds) > maxStatsRounds {
		return nil, errors.New("invalid Snowblind stats state")
	}
	for key, round := range s.state.Rounds {
		raw, err := hex.DecodeString(key)
		if err != nil || len(raw) != 16 || round.Gathering == 0 || len(round.Participants) == 0 || len(round.Participants) > 32 || round.Reports == nil || len(round.Reports) > 32 {
			return nil, errors.New("invalid Snowblind stats round")
		}
		seen := map[uint32]bool{}
		for _, pid := range round.Participants {
			if pid < 1000 || !validPID(pid) || seen[pid] {
				return nil, errors.New("invalid Snowblind stats participant")
			}
			seen[pid] = true
		}
		for pid, receipt := range round.Reports {
			if !validSummary(receipt.Summary, pid, round.Participants) || !validReceiptPersonal(round, pid, receipt) {
				return nil, errors.New("invalid Snowblind stat receipt")
			}
		}
	}
	return s, nil
}

func ownSummary(receipt statReceipt, pid uint32) summaryRow {
	for _, row := range receipt.Summary {
		if row.PID == pid {
			return row
		}
	}
	return summaryRow{}
}
func validReceiptPersonal(round statRound, pid uint32, receipt statReceipt) bool {
	if !validPersonal(receipt.Personal) {
		return false
	}
	if !receipt.PersonalReceived {
		return len(receipt.Personal) == 0
	}
	if ownSummary(receipt, pid).Category == 2 && len(receipt.Personal) != 0 {
		return false
	}
	if mapID, ok := receipt.Personal["mapId"]; ok && round.Map != 0 && float64(mapID) != float64(round.Map) {
		return false
	}
	return true
}

// The durable receipts are the source of truth. Totals are derived from one
// authenticated self row per round, so a crash cannot split awards from dedup.
func (s *statsStore) summary(ctx *reportContext, pid uint32, rows []summaryRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validSummary(rows, pid, ctx.pids()) {
		return errors.New("invalid Snowblind summary")
	}
	round, exists := s.state.Rounds[ctx.key]
	if exists {
		if round.Gathering != ctx.gid || round.Map != ctx.mapID || round.Mode != ctx.mode {
			return errors.New("Snowblind round identity conflict")
		}
		if receipt, ok := round.Reports[pid]; ok {
			if !reflect.DeepEqual(receipt.Summary, rows) {
				return errors.New("conflicting Snowblind summary retry")
			}
			return nil
		}
	} else {
		if len(s.state.Rounds) >= maxStatsRounds {
			return errors.New("Snowblind stats round capacity reached")
		}
		round = statRound{Gathering: ctx.gid, Map: ctx.mapID, Mode: ctx.mode, Participants: ctx.pids(), Reports: map[uint32]statReceipt{}}
	}
	round.Reports = cloneReports(round.Reports)
	// A default leave can precede other players joining. Retain the union of
	// ticket-proven participants, rather than trusting the submitted roster.
	round.Participants = append([]uint32{}, round.Participants...)
	for _, id := range ctx.pids() {
		if !containsPID(round.Participants, id) {
			round.Participants = append(round.Participants, id)
		}
	}
	copied := make([]summaryRow, len(rows))
	for i, row := range rows {
		copied[i] = row
		copied[i].Values = append([]float32{}, row.Values...)
	}
	round.Reports[pid] = statReceipt{Summary: copied}
	return s.commit(ctx.key, round)
}
func cloneReports(records map[uint32]statReceipt) map[uint32]statReceipt {
	next := make(map[uint32]statReceipt, len(records)+1)
	for pid, receipt := range records {
		next[pid] = receipt
	}
	return next
}
func (s *statsStore) personal(ctx *reportContext, pid uint32, values map[string]float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	round, ok := s.state.Rounds[ctx.key]
	if !ok {
		return errors.New("Snowblind personal stats lack a summary receipt")
	}
	receipt, ok := round.Reports[pid]
	if !ok {
		return errors.New("Snowblind personal stats lack an authenticated summary")
	}
	if receipt.PersonalReceived {
		if !maps.Equal(receipt.Personal, values) {
			return errors.New("conflicting Snowblind personal retry")
		}
		return nil
	}
	receipt.PersonalReceived = true
	receipt.Personal = values
	if !validReceiptPersonal(round, pid, receipt) {
		return errors.New("invalid Snowblind personal stats")
	}
	receipt.Personal = map[string]float32{}
	for name, value := range values {
		receipt.Personal[name] = value
	}
	round.Reports = cloneReports(round.Reports)
	round.Reports[pid] = receipt
	return s.commit(ctx.key, round)
}
func (s *statsStore) commit(key string, round statRound) error {
	next := statsDisk{Version: 1, Rounds: make(map[string]statRound, len(s.state.Rounds)+1)}
	for k, v := range s.state.Rounds {
		next.Rounds[k] = v
	}
	next.Rounds[key] = round
	if err := saveState(s.path, ".stats-*", next, maxStatsStateBytes); err != nil {
		return err
	}
	s.state = next
	return nil
}
func (s *statsStore) totals() map[uint32]playerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := map[uint32]playerStats{}
	for _, round := range s.state.Rounds {
		for pid, receipt := range round.Reports {
			own := ownSummary(receipt, pid)
			if own.Category != 1 || len(receipt.Summary) < 2 {
				continue
			} // No solo/default awards.
			total := result[pid]
			total.PID = pid
			total.Rounds++
			total.Kills += uint64(own.Values[0])
			total.Deaths += uint64(own.Values[1])
			total.Penalties += uint64(own.Values[4])
			switch own.Values[2] {
			case 1:
				total.Wins++
			case 0:
				total.Losses++
			case 2:
				total.Draws++
			}
			if total.Counters == nil {
				total.Counters = map[string]float64{}
				total.Maps = map[uint32]mapStats{}
			}
			if receipt.PersonalReceived {
				for name, value := range receipt.Personal {
					if name != "mapId" && name != "mapWins" {
						total.Counters[name] += float64(value)
					}
				}
				if mapID, ok := receipt.Personal["mapId"]; ok {
					id := uint32(mapID)
					m := total.Maps[id]
					m.Reports++
					switch receipt.Personal["mapWins"] {
					case 1:
						m.Wins++
					case 2:
						m.Draws++
					}
					total.Maps[id] = m
				}
			}
			result[pid] = total
		}
	}
	return result
}
