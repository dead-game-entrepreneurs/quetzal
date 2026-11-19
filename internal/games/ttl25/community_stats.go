package ttl25

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func floatBytes(v []float32) []byte {
	b := make([]byte, 0, len(v)*4)
	for _, f := range v {
		b = rankingU32(b, math.Float32bits(f))
	}
	return b
}
func finiteValues(v []float32) bool {
	for _, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return false
		}
	}
	return true
}
func (r *rankingReader) floats(limit uint32) []float32 {
	n := r.u32()
	if n > limit {
		r.err = quazal.ErrRMC
		return nil
	}
	v := make([]float32, n)
	for i := range v {
		v[i] = math.Float32frombits(r.u32())
	}
	if !finiteValues(v) {
		r.err = quazal.ErrRMC
	}
	return v
}
func (c *community) rankedPlayers() []uint32 {
	ids := make([]uint32, 0, len(c.state.Stats))
	for id := range c.state.Stats {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := c.state.Stats[ids[i]].Values[0], c.state.Stats[ids[j]].Values[0]
		if a == b {
			return ids[i] < ids[j]
		}
		return a > b
	})
	return ids
}
func (c *community) clanRating(clan clanRecord) int32 {
	var rating float64
	for pid := range clan.Members {
		v := c.state.Stats[pid].Values
		if len(v) == 102 {
			rating += float64(v[0])
		}
	}
	return int32(max(float64(math.MinInt32), min(rating, float64(math.MaxInt32))))
}
func (c *community) rankedClans() []clanRecord {
	v := make([]clanRecord, 0, len(c.state.Clans))
	for _, clan := range c.state.Clans {
		v = append(v, clan)
	}
	sort.Slice(v, func(i, j int) bool {
		a, b := c.clanRating(v[i]), c.clanRating(v[j])
		if a == b {
			return v[i].ID < v[j].ID
		}
		return a > b
	})
	return v
}
func (c *community) appendStats(b []byte, pid uint32) []byte {
	abbr := ""
	rating := float32(0)
	if clan, ok := c.clanOf(&c.state, pid); ok {
		abbr = clan.Abbr
		rating = float32(c.clanRating(clan))
	}
	b = rankingString(b, c.accountName(pid))
	b = rankingString(b, abbr)
	b = append(b, floatBytes([]float32{rating, 0})...)
	b = rankingU32(b, pid)
	v := c.state.Stats[pid].Values
	b = rankingU32(b, uint32(len(v)))
	return append(b, floatBytes(v)...)
}
func (c *community) statistics(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
	r := rankingReader{b: request.Args}
	if request.Method == 8 {
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		return c.alerts(pid), nil
	}
	if request.Method == 9 {
		values := r.floats(87)
		if !r.done() || len(values) != 0 && len(values) != 87 {
			return nil, quazal.ErrRMC
		}
		key, ok := c.sessions.reportKeyFor(peer, pid)
		if !ok {
			return nil, errors.New("personal statistics require active gathering participation")
		}
		if len(values) == 0 {
			// Retail can finish a round without building a local personal row.
			// Acknowledge its empty list without inventing or clearing statistics.
			if c.sessions.log != nil {
				c.sessions.log.Info("statistics personal report skipped", "pid", pid, "peer", peer.ID,
					"local_port", peer.LocalPort, "accepted", true, "reason", "empty personal vector")
			}
			return nil, nil
		}
		return c.transaction(func(s *communityState) ([]byte, error) {
			return nil, recordStats(s, pid, key+":personal", 15, values, c.now().Unix())
		})
	}
	ids := []uint32{}
	switch request.Method {
	case 4:
		target := r.u32()
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		position := uint32(0)
		for i, id := range c.rankedPlayers() {
			if id == target {
				position = uint32(i + 1)
				break
			}
		}
		return rankingU32(nil, position), nil
	case 5:
		count := r.u32()
		if !r.done() || count > 100 {
			return nil, quazal.ErrRMC
		}
		ids = c.rankedPlayers()
		if count < uint32(len(ids)) {
			ids = ids[:count]
		}
	case 6:
		n := r.u32()
		if n > 256 {
			return nil, quazal.ErrRMC
		}
		seen := map[uint32]bool{}
		for range n {
			id := r.u32()
			if _, exists := c.state.Stats[id]; exists && !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
		if !r.done() {
			return nil, quazal.ErrRMC
		}
	case 7:
		pivot, count := r.u32(), r.u32()
		if !r.done() || count > 100 {
			return nil, quazal.ErrRMC
		}
		ids = c.rankedPlayers()
		start := 0
		for i, id := range ids {
			if id == pivot {
				start = max(0, i-int(count)/2)
				break
			}
		}
		end := min(start+int(count), len(ids))
		ids = ids[start:end]
	default:
		return nil, errors.New("unsupported statistics method")
	}
	b := rankingU32(nil, uint32(len(ids)))
	for _, id := range ids {
		b = c.appendStats(b, id)
	}
	return b, nil
}

// Both vectors contain round deltas. The retail UI derives ratios and most-
// played mode from their accumulated raw counters. Gathering/PID/chunk receipts
// make retries harmless; a conflicting second report is rejected.
func recordStats(s *communityState, pid uint32, key string, offset int, values []float32, now int64) error {
	key = fmt.Sprintf("%s:%d", key, pid)
	digest := reportDigest(values)
	if old, ok := s.Reports[key]; ok {
		if old != digest {
			return errors.New("conflicting repeated statistics report")
		}
		return nil
	}
	if len(s.Reports) >= 100000 {
		return errors.New("statistics report capacity reached")
	}
	stats := s.Stats[pid]
	if len(stats.Values) == 0 {
		stats.Values = make([]float32, 102)
	}
	if offset < 0 || offset+len(values) > 102 || !finiteValues(values) {
		return quazal.ErrRMC
	}
	// Validate the complete sum before publishing any of it, including when
	// this helper is used outside a persistence transaction.
	merged := append([]float32(nil), stats.Values...)
	for i, value := range values {
		merged[offset+i] += value
	}
	if !finiteValues(merged) {
		return errors.New("statistics accumulation exceeds finite range")
	}
	stats.Values = merged
	stats.Updated = now
	s.Stats[pid] = stats
	s.Reports[key] = digest
	return nil
}
func (c *community) gatheringReport(peer prudp.Peer, pid uint32, args []byte) ([]byte, error) {
	r := rankingReader{b: args}
	gid, n := r.u32(), r.u32()
	if n > 32 {
		return nil, quazal.ErrRMC
	}
	type report struct {
		pid    uint32
		values []float32
	}
	reports := make([]report, 0, n)
	seen := map[uint32]bool{}
	for range n {
		target, kind := r.u32(), r.u32()
		values := r.floats(15)
		if target < 1000 || kind != 1 || len(values) != 15 || seen[target] {
			return nil, quazal.ErrRMC
		}
		seen[target] = true
		reports = append(reports, report{target, values})
	}
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	key, members, host, ok := c.sessions.reportAuthority(peer, pid, gid)
	result := func(accepted bool, reason string, mismatches int) ([]byte, error) {
		if c.sessions.log != nil {
			c.sessions.log.Info("statistics gathering report", "pid", pid, "peer", peer.ID,
				"local_port", peer.LocalPort, "gid", gid, "entries", n, "host", host,
				"caller_in_report", seen[pid], "accepted", accepted, "reason", reason,
				"non_authoritative_mismatches", mismatches)
		}
		return boolReply(accepted), nil
	}
	if !ok {
		return result(false, "no reporting authority", 0)
	}
	if n == 0 || host && !seen[pid] {
		return result(false, "empty report or host absent", 0)
	}
	for _, report := range reports {
		if !members[report.pid] {
			return result(false, "target outside round roster", 0)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !host {
		// Retail peers can submit the initial round roster without themselves,
		// and late peers can have different snapshots. These are diagnostics,
		// not authoritative writes or reasons to abort personal reporting.
		mismatches := 0
		for _, report := range reports {
			receipt := fmt.Sprintf("%s:gathering:%d", key, report.pid)
			if digest, exists := c.state.Reports[receipt]; exists && digest != reportDigest(report.values) {
				mismatches++
			}
		}
		return result(true, "participant acknowledged without aggregate write", mismatches)
	}
	b, err := c.transaction(func(s *communityState) ([]byte, error) {
		for _, report := range reports {
			if err := recordStats(s, report.pid, key+":gathering", 0, report.values, c.now().Unix()); err != nil {
				return nil, err
			}
		}
		return boolReply(true), nil
	})
	if err != nil {
		return b, err
	}
	return result(true, "host aggregate committed", 0)
}

func (c *community) accountInfo(pid uint32, args []byte) ([]byte, error) {
	if len(args) != 0 {
		return nil, quazal.ErrRMC
	}
	account, ok := c.accounts.LookupPID(pid)
	if !ok {
		return nil, errors.New("account unavailable")
	}
	public := rankingU32(rankingU32(nil, 4), 0)
	privateBody := append([]byte{0}, rankingString(nil, "")...)
	privateBody = append(privateBody, make([]byte, 8)...)
	private := append(rankingU32(nil, uint32(len(privateBody))), privateBody...)
	if account.PublicData != nil && account.PrivateData != nil {
		if err := ValidateAccountData(*account.PublicData, *account.PrivateData); err != nil {
			return nil, err
		}
		public = account.PublicData.Payload
		private = account.PrivateData.Payload
	}
	b := append([]byte(nil), private...)
	b = append(b, public...)
	return rankingString(b, account.Contact), nil
}
