package snowblind

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// These are title-native names from the personal-map builder at 0x10e5e0.
var personalFields = strings.Fields(`timeAlive numHitsTaken numShotsFired numShotsThatHit numEnemiesKilled numAlliesKilled
 killsNumCarbine killsNumRocketLauncher killsNumPistol killsNumSniper killsNumHerf killsNumShotgun killsNumFlechette killsNumRailLaser killsNumFragGrenade killsNumDetpack killsNumMelee
 deathsNumCarbine deathsNumRocketLauncher deathsNumPistol deathsNumSniper deathsNumHerf deathsNumShotgun deathsNumFlechette deathsNumRailLaser deathsNumFragGrenade deathsNumDetpack deathsNumMelee
 numUsesInvisibility numUsesForcefield numUsesSlowtime numUsesEStorm numUsesVision mapId mapWins`)

func validPersonal(values map[string]float32) bool {
	if len(values) > len(personalFields) {
		return false
	}
	for name, value := range values {
		known := false
		for _, field := range personalFields {
			if field == name {
				known = true
				break
			}
		}
		if !known || !finiteCounter(value, 1e9) || (name != "timeAlive" && float32(math.Trunc(float64(value))) != value) {
			return false
		}
		if name == "mapWins" && value > 2 {
			return false
		}
	}
	_, hasMap := values["mapId"]
	_, hasWins := values["mapWins"]
	return hasMap == hasWins
}

func finiteCounter(value, max float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0) && value >= 0 && value <= max
}

func parsePersonal(args []byte) (map[string]float32, error) {
	r := quazal.NewReader(args)
	count := r.U32()
	if count > uint32(len(personalFields)) {
		return nil, quazal.ErrRMC
	}
	values := map[string]float32{}
	for range count {
		name := r.String(64)
		value := math.Float32frombits(r.U32())
		if _, duplicate := values[name]; duplicate {
			return nil, quazal.ErrRMC
		}
		values[name] = value
	}
	if !r.Done() || !validPersonal(values) {
		return nil, quazal.ErrRMC
	}
	return values, nil
}

type summaryRow struct {
	PID      uint32    `json:"pid"`
	Category uint32    `json:"category"`
	Values   []float32 `json:"values"`
}

func validSummary(rows []summaryRow, pid uint32, participants []uint32) bool {
	if len(rows) == 0 || len(rows) > 32 {
		return false
	}
	seen := map[uint32]bool{}
	for _, row := range rows {
		if seen[row.PID] || !containsPID(participants, row.PID) {
			return false
		}
		seen[row.PID] = true
		switch row.Category {
		case 1:
			if len(row.Values) != 5 {
				return false
			}
			for _, v := range row.Values {
				if !finiteCounter(v, 65535) || float32(math.Trunc(float64(v))) != v {
					return false
				}
			}
			if row.Values[2] > 2 || row.Values[3] > 1 {
				return false
			}
		case 2:
			if len(rows) != 1 || row.PID != pid || len(row.Values) != 0 {
				return false
			}
		default:
			return false
		}
	}
	return seen[pid]
}

func parseSummary(args []byte) (uint32, []summaryRow, error) {
	r := quazal.NewReader(args)
	gid, count := r.U32(), r.U32()
	if count == 0 || count > 32 {
		return 0, nil, quazal.ErrRMC
	}
	rows := make([]summaryRow, 0, count)
	for range count {
		row := summaryRow{PID: r.U32(), Category: r.U32()}
		n := r.U32()
		if n > 5 {
			return 0, nil, quazal.ErrRMC
		}
		row.Values = make([]float32, n)
		for i := range row.Values {
			row.Values[i] = math.Float32frombits(r.U32())
		}
		rows = append(rows, row)
	}
	if !r.Done() {
		return 0, nil, quazal.ErrRMC
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].PID < rows[j].PID })
	return gid, rows, nil
}

// A private server nonce separates reused gathering IDs and server restarts.
// Wire IDs, report contents and RPC call IDs are not durable deduplication keys.
type reportContext struct {
	key          string
	gid          uint32
	mode         byte
	mapID        uint32
	participants map[owner]bool
	finished     map[owner]bool
	closed       bool
	ranked       bool
}

func newReportContext(gid uint32, lobby gathering) (*reportContext, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	return &reportContext{key: hex.EncodeToString(nonce[:]), gid: gid, mode: lobby.Mode, mapID: lobby.Map, participants: map[owner]bool{}, finished: map[owner]bool{}}, nil
}
func (c *reportContext) pids() []uint32 {
	ids := []uint32{}
	for who := range c.participants {
		if !containsPID(ids, who.pid) {
			ids = append(ids, who.pid)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Called under the session lock: membership, competition and exact connection
// generation are checked before any durable mutation. Retired rounds survive
// host cleanup briefly so a slower recipient can finish its own leave job.
func (s *sessions) summary(who owner, args []byte) ([]byte, error) {
	gid, rows, err := parseSummary(args)
	if err != nil {
		return nil, err
	}
	var ctx *reportContext
	if room := s.rooms[gid]; room != nil {
		ctx = room.report
	}
	if ctx == nil {
		if at, ok := s.retiredAt[gid]; ok && time.Since(at) <= 15*time.Minute {
			ctx = s.retired[gid]
		}
	}
	// A reused wire gathering may already be playing again while a slower
	// participant is still uploading the previous round. Finish that leave
	// pipeline before binding this connection to the newer epoch.
	if previous := s.reporting[who]; previous != nil && previous.gid == gid && previous != ctx && !previous.finished[who] {
		ctx = previous
	} else if previous := s.retired[gid]; previous != nil && previous != ctx && previous.participants[who] && !previous.finished[who] && time.Since(s.retiredAt[gid]) <= 15*time.Minute {
		ctx = previous
	}
	if ctx == nil || !ctx.participants[who] || !validSummary(rows, who.pid, ctx.pids()) {
		return boolean(false)
	}
	if !ctx.ranked && rows[0].Category != 2 {
		return boolean(false)
	}
	if err := s.stats.summary(ctx, who.pid, rows); err != nil {
		return nil, err
	}
	s.reporting[who] = ctx
	s.log.Info("stats summary saved", "gid", gid, "pid", who.pid, "records", len(rows))
	return boolean(true)
}

func (s *sessions) personal(who owner, args []byte) ([]byte, error) {
	values, err := parsePersonal(args)
	if err != nil {
		return nil, err
	}
	ctx := s.reporting[who]
	if ctx == nil || !ctx.participants[who] {
		return nil, errors.New("Snowblind personal stats require a verified round summary")
	}
	if err := s.stats.personal(ctx, who.pid, values); err != nil {
		return nil, err
	}
	s.log.Info("personal stats saved", "gid", ctx.gid, "pid", who.pid, "fields", len(values))
	return []byte{}, nil // Native 101/4 has no application output fields.
}
