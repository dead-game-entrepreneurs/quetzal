package snowblind

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func reportFixture(t *testing.T) (*startup, []prudp.Peer, uint32, string) {
	t.Helper()
	s, players, _ := clanFixture(t)
	registry, peers, _ := sessionFixture(t)
	s.sessions = registry
	path := filepath.Join(t.TempDir(), "stats.json")
	stats, err := openStats(path, func(pid uint32) bool { _, ok := s.accounts.LookupPID(pid); return ok })
	if err != nil {
		t.Fatal(err)
	}
	s.stats = stats
	registry.stats = stats
	lobby := createMatch(t, registry, peers[0])
	round := gathering{Kind: "Gathering", Gathering: quazal.Gathering{Description: "BO3Round", Policy: 1}}
	body, _ := round.encode()
	rid := quazal.NewReader(callMatch(t, registry, peers[0], players[0].PID, 21, 1, body)).U32()
	callMatch(t, registry, peers[0], players[0].PID, 26, 2, encode(func(w *quazal.Writer) { w.U32(1); w.U32(rid) }))
	for i := 0; i < 2; i++ {
		callMatch(t, registry, peers[i], players[i].PID, 21, 11, messageArgs(lobby, ""))
		callMatch(t, registry, peers[i], players[i].PID, 21, 11, messageArgs(rid, ""))
	}
	callMatch(t, registry, peers[0], players[0].PID, 21, 30, encode(func(w *quazal.Writer) { w.U32(rid); w.U32(2) }))
	return s, peers, rid, path
}
func summaryArgs(gid uint32, rows []summaryRow) []byte {
	return encode(func(w *quazal.Writer) {
		w.U32(gid)
		w.U32(uint32(len(rows)))
		for _, row := range rows {
			w.U32(row.PID)
			w.U32(row.Category)
			w.U32(uint32(len(row.Values)))
			for _, v := range row.Values {
				w.U32(math.Float32bits(v))
			}
		}
	})
}
func personalArgs(values map[string]float32) []byte {
	names := []string{}
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return encode(func(w *quazal.Writer) {
		w.U32(uint32(len(names)))
		for _, name := range names {
			w.String(name)
			w.U32(math.Float32bits(values[name]))
		}
	})
}
func reportRows() []summaryRow {
	return []summaryRow{{PID: 1000, Category: 1, Values: []float32{3, 1, 1, 1, 0}}, {PID: 1001, Category: 1, Values: []float32{1, 3, 0, 0, 1}}}
}
func fullPersonal() map[string]float32 {
	values := map[string]float32{}
	for _, name := range personalFields {
		values[name] = 0
	}
	values["timeAlive"] = 90.5
	values["numShotsFired"] = 10
	values["numShotsThatHit"] = 6
	values["numEnemiesKilled"] = 3
	values["killsNumCarbine"] = 3
	values["mapId"] = 300
	values["mapWins"] = 1
	return values
}
func reportCall(t *testing.T, s *startup, p prudp.Peer, pid uint32, protocol byte, method uint32, args []byte) []byte {
	t.Helper()
	b, err := s.handle(p, pid, quazal.Request{Protocol: protocol, Method: method, Args: args})
	if err != nil {
		t.Fatalf("%d/%d: %v", protocol, method, err)
	}
	return b
}
func reloadStats(t *testing.T, s *startup, path string) {
	t.Helper()
	stats, err := openStats(path, func(pid uint32) bool { _, ok := s.accounts.LookupPID(pid); return ok })
	if err != nil {
		t.Fatal(err)
	}
	s.stats = stats
	s.sessions.stats = stats
}

func TestReportsPersistDeduplicateAcrossClientsRetriesAndReload(t *testing.T) {
	s, peers, rid, path := reportFixture(t)
	args := summaryArgs(rid, reportRows())
	if len(args) != 72 {
		t.Fatal("full report differs from retail shape")
	}
	for _, i := range []int{0, 0, 1, 1} {
		if !bytes.Equal(reportCall(t, s, peers[i], 1000+uint32(i), 21, 31, args), []byte{1}) {
			t.Fatal("summary rejected")
		}
	}
	personal := personalArgs(fullPersonal())
	if b := reportCall(t, s, peers[0], 1000, 101, 4, personal); len(b) != 0 {
		t.Fatal("personal RPC must have no application fields")
	}
	reportCall(t, s, peers[0], 1000, 101, 4, personal)
	totals := s.stats.totals()
	if totals[1000].Rounds != 1 || totals[1000].Kills != 3 || totals[1001].Kills != 1 || totals[1000].Wins != 1 || totals[1001].Losses != 1 || totals[1001].Penalties != 1 || totals[1000].Counters["numShotsFired"] != 10 || totals[1000].Maps[300].Wins != 1 {
		t.Fatal("duplicate or cross-player awards", totals)
	}
	if _, ok := totals[1000].Counters["mapId"]; ok {
		t.Fatal("map ID was accumulated")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("stats state not private", err)
	}
	before, _ := os.ReadFile(path)
	reloadStats(t, s, path)
	// Reordered wire rows and a fresh RPC call ID must still be the same report.
	reversed := reportRows()
	reversed[0], reversed[1] = reversed[1], reversed[0]
	reportCall(t, s, peers[0], 1000, 21, 31, summaryArgs(rid, reversed))
	reportCall(t, s, peers[0], 1000, 101, 4, personal)
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || !reflect.DeepEqual(totals, s.stats.totals()) {
		t.Fatal("reload/retry awarded stats twice")
	}
	changed := reportRows()
	changed[0].Values[0]++
	if _, err := s.handle(peers[0], 1000, quazal.Request{Protocol: 21, Method: 31, Args: summaryArgs(rid, changed)}); err == nil {
		t.Fatal("conflicting summary overwrote receipt")
	}
	values := fullPersonal()
	values["numShotsFired"]++
	if _, err := s.handle(peers[0], 1000, quazal.Request{Protocol: 101, Method: 4, Args: personalArgs(values)}); err == nil {
		t.Fatal("conflicting personal overwrote receipt")
	}
}

func TestReportContextSurvivesHostLeaveAndRejectsSpoofs(t *testing.T) {
	s, peers, rid, _ := reportFixture(t)
	args := summaryArgs(rid, reportRows())
	if b := reportCall(t, s, peers[2], 1002, 21, 31, args); !bytes.Equal(b, []byte{0}) {
		t.Fatal("outsider report accepted")
	}
	fresh := peers[1]
	fresh.ID++
	if _, err := s.handle(fresh, 1001, quazal.Request{Protocol: 21, Method: 31, Args: args}); err == nil {
		t.Fatal("unregistered generation accepted")
	}
	reportCall(t, s, peers[0], 1000, 21, 31, args)
	if len(s.stats.totals()) != 1 {
		t.Fatal("a reporter awarded another player's copied row")
	}
	reportCall(t, s, peers[0], 1000, 101, 4, personalArgs(fullPersonal()))
	if b := reportCall(t, s, peers[0], 1000, 101, 5, messageArgs(rid, "")); !bytes.Equal(b, []byte{1}) {
		t.Fatal("host EndParticipation")
	}
	if s.sessions.rooms[rid] != nil {
		t.Fatal("host round survived leave")
	}
	if b := reportCall(t, s, peers[1], 1001, 21, 31, args); !bytes.Equal(b, []byte{1}) {
		t.Fatal("late peer report lost with host cleanup")
	}
	reportCall(t, s, peers[1], 1001, 101, 4, personalArgs(nil))
	if len(s.stats.totals()) != 2 {
		t.Fatal("missing late participant")
	}
	// EndParticipation still acknowledges an authenticated retired round member.
	if b := reportCall(t, s, peers[1], 1001, 101, 5, messageArgs(rid, "")); !bytes.Equal(b, []byte{1}) {
		t.Fatal("late participant EndParticipation")
	}
	s.sessions.retiredAt[rid] = time.Now().Add(-16 * time.Minute)
	if b := reportCall(t, s, peers[1], 1001, 21, 31, args); !bytes.Equal(b, []byte{0}) {
		t.Fatal("expired retired round accepted")
	}
}

func TestDefaultReportsNoAwardsAndEmptyPersonalReplay(t *testing.T) {
	s, peers, rid, path := reportFixture(t)
	rows := []summaryRow{{PID: 1000, Category: 2, Values: []float32{}}}
	reportCall(t, s, peers[0], 1000, 21, 31, summaryArgs(rid, rows))
	reportCall(t, s, peers[0], 1000, 101, 4, personalArgs(nil))
	reloadStats(t, s, path)
	reportCall(t, s, peers[0], 1000, 21, 31, summaryArgs(rid, rows))
	reportCall(t, s, peers[0], 1000, 101, 4, personalArgs(nil))
	if len(s.stats.totals()) != 0 {
		t.Fatal("default report invented earned results")
	}
	if _, err := s.handle(peers[0], 1000, quazal.Request{Protocol: 101, Method: 4, Args: personalArgs(fullPersonal())}); err == nil {
		t.Fatal("empty report upgraded to earned personal stats")
	}
}

func TestNewRoundAndRestartDoNotCollideWithOldGatheringIDs(t *testing.T) {
	s, peers, rid, path := reportFixture(t)
	args := summaryArgs(rid, reportRows())
	reportCall(t, s, peers[0], 1000, 21, 31, args)
	firstKey := s.sessions.rooms[rid].report.key
	callMatch(t, s.sessions, peers[0], 1000, 21, 30, encode(func(w *quazal.Writer) { w.U32(rid); w.U32(6) }))
	callMatch(t, s.sessions, peers[0], 1000, 21, 30, encode(func(w *quazal.Writer) { w.U32(rid); w.U32(2) }))
	if s.sessions.rooms[rid].report.key == firstKey {
		t.Fatal("new round reused dedup identity")
	}
	// A delayed copy while the prior leave pipeline is unfinished must not
	// award the next epoch merely because the host restarted its wire ID.
	reportCall(t, s, peers[0], 1000, 21, 31, args)
	if s.stats.totals()[1000].Rounds != 1 {
		t.Fatal("delayed prior report awarded the next round")
	}
	reportCall(t, s, peers[0], 1000, 101, 4, personalArgs(nil))
	reportCall(t, s, peers[0], 1000, 101, 5, messageArgs(rid, ""))
	reportCall(t, s, peers[0], 1000, 21, 31, args)
	if s.stats.totals()[1000].Rounds != 2 {
		t.Fatal("identical results in separate rounds were deduplicated")
	}
	reloadStats(t, s, path)
	registry, _, _ := sessionFixture(t)
	registry.stats = s.stats
	s.sessions = registry
	if b := reportCall(t, s, peers[0], 1000, 21, 31, args); !bytes.Equal(b, []byte{0}) {
		t.Fatal("restart accepted stale wire round ID")
	}
	// New lobbies after restart can reuse the wire ID but have a distinct nonce.
	lobby := createMatch(t, registry, peers[0])
	_ = lobby
	round := gathering{Kind: "Gathering", Gathering: quazal.Gathering{Description: "BO3Round", Policy: 1}}
	b, _ := round.encode()
	assigned := quazal.NewReader(callMatch(t, registry, peers[0], 1000, 21, 1, b)).U32()
	if assigned != rid || registry.rooms[rid].report.key == firstKey {
		t.Fatal("restart round nonce collision")
	}
}

func TestReportWriteFailureDoesNotAcknowledgeOrAward(t *testing.T) {
	s, peers, rid, path := reportFixture(t)
	args := summaryArgs(rid, reportRows())
	s.stats.path = filepath.Join(filepath.Dir(path), "missing", "stats.json")
	if _, err := s.handle(peers[0], 1000, quazal.Request{Protocol: 21, Method: 31, Args: args}); err == nil || len(s.stats.totals()) != 0 || s.sessions.reporting[identity(peers[0], 1000)] != nil {
		t.Fatal("failed summary commit published state")
	}
	s.stats.path = path
	reportCall(t, s, peers[0], 1000, 21, 31, args)
	before, _ := os.ReadFile(path)
	s.stats.path = filepath.Join(filepath.Dir(path), "missing", "stats.json")
	if _, err := s.handle(peers[0], 1000, quazal.Request{Protocol: 101, Method: 4, Args: personalArgs(fullPersonal())}); err == nil || len(s.stats.totals()[1000].Counters) != 0 {
		t.Fatal("failed personal commit published state")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed write changed disk")
	}
	s.stats.path = path
	reportCall(t, s, peers[0], 1000, 101, 4, personalArgs(fullPersonal()))
}

func TestReportValidationAndRoundBinding(t *testing.T) {
	s, peers, rid, _ := reportFixture(t)
	for _, request := range []quazal.Request{{Protocol: 21, Method: 31, Args: summaryArgs(rid, reportRows())}, {Protocol: 101, Method: 4, Args: personalArgs(fullPersonal())}} {
		for n := range len(request.Args) {
			truncated := request
			truncated.Args = request.Args[:n]
			if _, err := s.handle(peers[0], 1000, truncated); err == nil {
				t.Fatalf("accepted %d/%d truncation %d", request.Protocol, request.Method, n)
			}
		}
		request.Args = append(bytes.Clone(request.Args), 0)
		if _, err := s.handle(peers[0], 1000, request); err == nil {
			t.Fatal("trailing report fields")
		}
	}
	if _, err := s.handle(peers[0], 1000, quazal.Request{Protocol: 101, Method: 4, Args: personalArgs(fullPersonal())}); err == nil {
		t.Fatal("personal report without summary")
	}
	for _, bad := range []map[string]float32{{"unknown": 1}, {"numEnemiesKilled": -1}, {"numShotsFired": float32(math.Inf(1))}, {"timeAlive": float32(math.NaN())}, {"numHitsTaken": 1.5}, {"mapId": 300}, {"mapId": 300, "mapWins": 3}} {
		if _, err := parsePersonal(personalArgs(bad)); err == nil {
			t.Fatal("invalid personal values accepted")
		}
	}
	dup := encode(func(w *quazal.Writer) {
		w.U32(2)
		for range 2 {
			w.String("numShotsFired")
			w.U32(0)
		}
	})
	if _, err := parsePersonal(dup); err == nil {
		t.Fatal("duplicate personal key")
	}
	for _, change := range []func([]summaryRow){func(rows []summaryRow) { rows[1].PID = 1000 }, func(rows []summaryRow) { rows[1].PID = 1002 }, func(rows []summaryRow) { rows[0].Values[0] = -1 }, func(rows []summaryRow) { rows[0].Values[2] = 3 }, func(rows []summaryRow) { rows[0].Category = 99 }} {
		rows := reportRows()
		change(rows)
		if b := reportCall(t, s, peers[0], 1000, 21, 31, summaryArgs(rid, rows)); !bytes.Equal(b, []byte{0}) {
			t.Fatal("invalid summary accepted")
		}
	}
	s.sessions.rooms[rid].competition = 0
	s.sessions.rooms[rid].report.ranked = false
	if b := reportCall(t, s, peers[0], 1000, 21, 31, summaryArgs(rid, reportRows())); !bytes.Equal(b, []byte{0}) {
		t.Fatal("unassociated round earned stats")
	}
	s.sessions.rooms[rid].competition = 1
	s.sessions.rooms[rid].report.ranked = true
	reportCall(t, s, peers[0], 1000, 21, 31, summaryArgs(rid, reportRows()))
	values := fullPersonal()
	values["mapId"] = 301
	if _, err := s.handle(peers[0], 1000, quazal.Request{Protocol: 101, Method: 4, Args: personalArgs(values)}); err == nil {
		t.Fatal("foreign map dimension accepted")
	}
}

func TestConcurrentCopiesAwardOneReceipt(t *testing.T) {
	s, _, rid, path := reportFixture(t)
	ctx := s.sessions.rooms[rid].report
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.stats.summary(ctx, 1000, reportRows()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	reloadStats(t, s, path)
	if s.stats.totals()[1000].Rounds != 1 {
		t.Fatal("concurrent duplicate awards")
	}
}

func TestStatsStateRejectsCorruptDataAndPublicPermissions(t *testing.T) {
	s, peers, rid, path := reportFixture(t)
	reportCall(t, s, peers[0], 1000, 21, 31, summaryArgs(rid, reportRows()))
	good, _ := os.ReadFile(path)
	for _, bad := range [][]byte{append(bytes.Clone(good), []byte(" {}")...), []byte(`{"version":1,"rounds":null}`), bytes.Replace(good, []byte(`"category":1`), []byte(`"category":99`), 1), bytes.Replace(good, []byte(`"pid":1000`), []byte(`"pid":999999`), 1)} {
		if err := os.WriteFile(path, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := openStats(path, func(pid uint32) bool { _, ok := s.accounts.LookupPID(pid); return ok }); err == nil {
			t.Fatal("corrupt state accepted")
		}
	}
	os.WriteFile(path, good, 0600)
	os.Chmod(path, 0644)
	if _, err := openStats(path, func(uint32) bool { return true }); err == nil {
		t.Fatal("public stats state accepted")
	}
}

type menuRow struct {
	PID        uint32
	Name, Clan string
	Values     []float32
}

func menuRows(t *testing.T, s *startup, pid uint32, selector byte, name string, count uint32) []menuRow {
	t.Helper()
	b, err := s.queryStats(pid, statsArgs(selector, name, count))
	if err != nil {
		t.Fatal(err)
	}
	r := quazal.NewReader(b)
	n := r.U32()
	if n > count {
		t.Fatal("menu row bound")
	}
	rows := []menuRow{}
	for range n {
		row := menuRow{Name: r.String(255), Clan: r.String(255), PID: r.U32()}
		values := r.U32()
		if values != 6 {
			t.Fatal("native stat row width")
		}
		for range values {
			row.Values = append(row.Values, math.Float32frombits(r.U32()))
		}
		rows = append(rows, row)
	}
	if !r.Done() {
		t.Fatal("menu trailing fields")
	}
	return rows
}
func TestLeaderboardsActualSocialFiltersRankAndSearch(t *testing.T) {
	s, peers, rid, path := reportFixture(t)
	args := summaryArgs(rid, reportRows())
	for i := 0; i < 2; i++ {
		reportCall(t, s, peers[i], 1000+uint32(i), 21, 31, args)
	}
	reloadStats(t, s, path)
	rows := menuRows(t, s, 1000, 1, "", 15)
	if len(rows) != 2 || rows[0].PID != 1000 || rows[1].PID != 1001 || !reflect.DeepEqual(rows[0].Values, []float32{0, 1, 3, 1, 1, 0}) || !reflect.DeepEqual(rows[1].Values, []float32{0, 2, 1, 3, 0, 1}) {
		t.Fatal("leaderboard totals or order", rows)
	}
	if len(menuRows(t, s, 1000, 1, "", 1)) != 1 || len(menuRows(t, s, 1000, 2, "", 15)) != 0 || len(menuRows(t, s, 1000, 3, "", 15)) != 0 {
		t.Fatal("limits or social isolation")
	}
	friends, err := openFriends(filepath.Join(t.TempDir(), "friends.json"), s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.friends = friends
	friends.handle(1000, quazal.Request{Protocol: 103, Method: 1, Args: noticeArgs("Second")})
	if len(menuRows(t, s, 1000, 2, "", 15)) != 0 {
		t.Fatal("pending friend leaked into stats")
	}
	friends.handle(1001, quazal.Request{Protocol: 103, Method: 2, Args: gidArgs(1000)})
	rows = menuRows(t, s, 1000, 2, "", 15)
	if len(rows) != 1 || rows[0].PID != 1001 || rows[0].Values[1] != 2 {
		t.Fatal("friends filter or global rank")
	}
	clanWantStatus(t, clanCall(t, s, 1000, 3, clanCreateArgs("TAG", "Synthetic Clan")), 0)
	clanWantStatus(t, clanCall(t, s, 1000, 5, noticeArgs("Second")), 0)
	clanWantStatus(t, clanCall(t, s, 1001, 7, invitationAction("TAG", "Founder")), 0)
	rows = menuRows(t, s, 1001, 3, "", 15)
	if len(rows) != 2 || rows[0].Clan != "TAG" || rows[1].Clan != "TAG" {
		t.Fatal("clan filter or labels")
	}
	rows = menuRows(t, s, 1000, 0, "Second", 15)
	if len(rows) != 2 {
		t.Fatal("player search should include neighbors")
	}
	rows = menuRows(t, s, 1000, 0, "Third", 15)
	if len(rows) != 1 || rows[0].Values[1] != -1 {
		t.Fatal("unranked account baseline")
	}
	if len(menuRows(t, s, 1000, 0, "Missing", 15)) != 0 {
		t.Fatal("unknown player was invented")
	}
}

func TestSearchWindowCentersTargetAndStableTies(t *testing.T) {
	s, _, _, _ := reportFixture(t)
	// Populate synthetic earned players through persisted report receipts.
	for i := 0; i < 20; i++ {
		account, err := s.accounts.Create(fmt.Sprintf("Ranked%d", i), quazalcrypto.Key{1})
		if err != nil {
			t.Fatal(err)
		}
		ctx, _ := newReportContext(uint32(20000+i), gathering{})
		ctx.participants[owner{pid: account.PID}] = true
		ctx.participants[owner{pid: 1000}] = true
		rows := []summaryRow{{PID: 1000, Category: 1, Values: []float32{0, 0, 0, 0, 0}}, {PID: account.PID, Category: 1, Values: []float32{float32(20 - i), 0, 1, 1, 0}}}
		if err := s.stats.summary(ctx, account.PID, rows); err != nil {
			t.Fatal(err)
		}
	}
	rows := menuRows(t, s, 1000, 0, "Ranked10", 15)
	if len(rows) != 15 || rows[7].Name != "Ranked10" {
		t.Fatal("search window not centered")
	}
	rows = menuRows(t, s, 1000, 1, "", 15)
	if len(rows) != 15 || rows[0].Name != "Ranked0" {
		t.Fatal("top 15")
	}
	// Ensure the on-disk representation contains receipts, not separately mutable totals.
	b, _ := os.ReadFile(s.stats.path)
	var state statsDisk
	if err := json.Unmarshal(b, &state); err != nil || len(state.Rounds) != 20 {
		t.Fatal("missing persistent receipts", err)
	}
}
