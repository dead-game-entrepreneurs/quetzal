package snowblind

import (
	"math"
	"sort"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// The native menu uses six values. Rank is ordinal; the unrecovered historical
// overall-score formula is not replaced with another game's skill formula.
func statsQuery(accounts *quazal.Store, args []byte, clans *clanStore) ([]byte, error) {
	return (&startup{accounts: accounts, clans: clans}).queryStats(0, args)
}
func (s *startup) queryStats(pid uint32, args []byte) ([]byte, error) {
	r := quazal.NewReader(args)
	selector, name, count := r.U8(), r.String(255), r.U32()
	if !r.Done() || selector > 3 || count > 100 || (selector != 0 && name != "") {
		return nil, quazal.ErrRMC
	}
	var w quazal.Writer
	if count == 0 || s.accounts == nil {
		w.U32(0)
		return w.Bytes()
	}
	totals := map[uint32]playerStats{}
	if s.stats != nil {
		totals = s.stats.totals()
	}
	all := make([]playerStats, 0, len(totals))
	for _, total := range totals {
		all = append(all, total)
	}
	// Replacement ordering until an original Snowblind score formula is found.
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Wins != b.Wins {
			return a.Wins > b.Wins
		}
		if a.Kills != b.Kills {
			return a.Kills > b.Kills
		}
		if a.Deaths != b.Deaths {
			return a.Deaths < b.Deaths
		}
		return a.PID < b.PID
	})
	ranks := map[uint32]int{}
	for i, total := range all {
		ranks[total.PID] = i + 1
	}
	selected := []playerStats{}
	switch selector {
	case 0:
		account, ok := s.accounts.Lookup(name)
		if ok && account.PID >= 1000 {
			if rank := ranks[account.PID]; rank > 0 {
				// Manual: searched player between up to seven higher/lower players.
				start := max(0, rank-1-int(count)/2)
				end := min(len(all), start+int(count))
				start = max(0, end-int(count))
				selected = all[start:end]
			} else {
				selected = append(selected, playerStats{PID: account.PID})
			}
		}
	case 1:
		selected = all
	case 2:
		accepted := map[uint32]bool{}
		if s.friends != nil {
			s.friends.mu.Lock()
			for _, edge := range s.friends.state.Edges {
				if !edge.Accepted {
					continue
				}
				if edge.From == pid {
					accepted[edge.To] = true
				}
				if edge.To == pid {
					accepted[edge.From] = true
				}
			}
			s.friends.mu.Unlock()
		}
		for _, total := range all {
			if accepted[total.PID] {
				selected = append(selected, total)
			}
		}
	case 3:
		members := map[uint32]bool{}
		if s.clans != nil {
			s.clans.mu.Lock()
			if index := s.clans.memberClan(pid); index >= 0 {
				for _, id := range clanMembers(s.clans.records[index]) {
					members[id] = true
				}
			}
			s.clans.mu.Unlock()
		}
		for _, total := range all {
			if members[total.PID] {
				selected = append(selected, total)
			}
		}
	}
	selected = selected[:min(len(selected), int(count))]
	w.U32(uint32(len(selected)))
	for _, total := range selected {
		account, _ := s.accounts.LookupPID(total.PID)
		w.String(account.Name)
		tag := ""
		if s.clans != nil {
			tag = s.clans.abbreviation(total.PID)
		}
		w.String(tag)
		w.U32(total.PID)
		rank := float32(-1)
		if ranks[total.PID] > 0 {
			rank = float32(ranks[total.PID])
		}
		w.U32(6)
		for _, value := range []float32{0, rank, float32(total.Kills), float32(total.Deaths), float32(total.Wins), float32(total.Losses)} {
			w.U32(math.Float32bits(value))
		}
	}
	return w.Bytes()
}
