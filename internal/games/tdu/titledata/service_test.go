package titledata

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func TestRecordGoldensAndBoundaries(t *testing.T) {
	event := EventStats{EventID: 0x1122334455667788, Name: "Ace", Values: [4]uint32{9, 8, 7, 6}}
	game := GameStats{Name: "Ace", Values: [3]uint32{1, 2, 0x3fc00000}, DoubleBits: [3]uint64{0x4004000000000000, 0x400c000000000000, 0x4016000000000000}, Counter: 4}
	ev, _ := event.MarshalBinary()
	gv, _ := game.MarshalBinary()
	for _, v := range []struct {
		got []byte
		hex string
	}{{ev, "887766554433221104004163650009000000080000000700000006000000"}, {gv, "04004163650001000000020000000000c03f0000000000000440040000000000000000000c400000000000001640"}} {
		want, _ := hex.DecodeString(v.hex)
		if !bytes.Equal(v.got, want) {
			t.Fatalf("record got%x want%x", v.got, want)
		}
	}
	parsedEvent, err := ParseEvent(ev)
	if err != nil || parsedEvent != event {
		t.Fatal(parsedEvent, err)
	}
	parsedGame, err := ParseGame(gv)
	if err != nil || parsedGame != game {
		t.Fatal(parsedGame, err)
	}
	for n := range len(ev) {
		if _, err := ParseEvent(ev[:n]); err == nil {
			t.Fatalf("accepted event truncation%d", n)
		}
	}
	for n := range len(gv) {
		if _, err := ParseGame(gv[:n]); err == nil {
			t.Fatalf("accepted game truncation%d", n)
		}
	}
	if _, err := ParseEvent(append(ev, 0)); err == nil {
		t.Fatal("accepted trailing event bytes")
	}
	bad := append([]byte(nil), gv...)
	bad[5] = 'x'
	if _, err := ParseGame(bad); err == nil {
		t.Fatal("accepted missing string terminator")
	}
	game.Name = "bad\x00name"
	if _, err := game.MarshalBinary(); err == nil {
		t.Fatal("accepted embedded NUL")
	}
}

type fixture struct {
	store    *Store
	service  *Service
	ace, bee quazal.Account
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	accounts, err := quazal.OpenStore(filepath.Join(dir, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	ace, err := accounts.Create("Ace", quazalcrypto.InitialKey([]byte("synthetic ace")))
	if err != nil {
		t.Fatal(err)
	}
	bee, err := accounts.Create("Bee", quazalcrypto.InitialKey([]byte("synthetic bee")))
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(dir, "title.json"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store, accounts)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{store, service, ace, bee}
}
func (f *fixture) rpc(t *testing.T, pid uint32, protocol byte, method uint32, args []byte) []byte {
	t.Helper()
	b, err := f.service.Handle(pid, quazal.Request{Protocol: protocol, Method: method, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestAccountBoundStatsPersistAndQueriesReturnReportedData(t *testing.T) {
	f := setup(t)
	for _, row := range []struct {
		pid   uint32
		name  string
		score uint32
		time  uint32
	}{{f.ace.PID, "Ace", 10, 150}, {f.bee.PID, "Bee", 20, 100}} {
		g := GameStats{Name: row.name, Values: [3]uint32{row.score, 2, math.Float32bits(1.5)}, DoubleBits: [3]uint64{math.Float64bits(42.25), math.Float64bits(5.75), 0}}
		b, _ := g.MarshalBinary()
		if !bytes.Equal(f.rpc(t, row.pid, 103, 1, b), []byte{1}) {
			t.Fatal("report not accepted")
		}
		e := EventStats{EventID: 99, Name: row.name, Values: [4]uint32{row.time, 1, 2, row.score}}
		b, _ = e.MarshalBinary()
		f.rpc(t, row.pid, 103, 6, b)
	}
	page := f.rpc(t, f.ace.PID, 103, 2, put32(put32(put32(nil, 2), 0), 20))
	r := reader{b: page}
	if r.u32() != 2 || readGame(&r).Name != "Bee" || readGame(&r).Name != "Ace" || r.u32() != 2 || !r.done() {
		t.Fatal("global stats query inconsistent")
	}
	page = f.rpc(t, f.ace.PID, 103, 7, put32(put32(put32(put64(nil, 99), 1), 0), 20))
	r = reader{b: page}
	if r.u32() != 2 || readEvent(&r).Name != "Bee" || readEvent(&r).Name != "Ace" || r.u32() != 2 || !r.done() {
		t.Fatal("event ranking inconsistent")
	}
	own := f.rpc(t, f.ace.PID, 103, 4, putstr(nil, "Ace"))
	r = reader{b: own}
	if r.u32() != 1 || readGame(&r).Name != "Ace" || r.u32() != 2 || r.u32() != 6 {
		t.Fatal("own stats schema")
	}
	for range 6 {
		if r.u32() == 0 {
			t.Fatal("missing actual rank")
		}
	}
	if !r.done() {
		t.Fatal("trailing stats")
	}
	page = f.rpc(t, f.ace.PID, 103, 10, putstr(put64(put32(nil, 1), 99), "Ace"))
	r = reader{b: page}
	if r.u32() != 1 || readEvent(&r).Values[0] != 150 || r.u32() != 1 || r.u32() != 2 || !r.done() {
		t.Fatal("event ID/name query incorrect")
	}
	again, err := Open(f.store.path)
	if err != nil {
		t.Fatal(err)
	}
	if again.data.Games[f.ace.PID].DoubleBits[0] != math.Float64bits(42.25) || len(again.data.Events) != 2 {
		t.Fatal("persisted reports changed")
	}
	info, err := os.Stat(f.store.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("report file not private", err)
	}
	bad, _ := GameStats{Name: "Bee"}.MarshalBinary()
	if _, err := f.service.Handle(f.ace.PID, quazal.Request{Protocol: 103, Method: 1, Args: bad}); err == nil {
		t.Fatal("cross-account report accepted")
	}
	if _, err := f.service.Handle(100, quazal.Request{Protocol: 103, Method: 1, Args: bad}); err == nil {
		t.Fatal("guest report accepted")
	}
}
func TestChallengesUploadBrowseDownloadIdempotently(t *testing.T) {
	f := setup(t)
	payload := []byte("synthetic player-created challenge data")
	upload := put32(nil, 7)
	upload = putstr(upload, "Challenge")
	upload = putstr(upload, "Description")
	upload = putstr(upload, "Ace")
	upload = putblob(upload, payload)
	first := f.rpc(t, f.ace.PID, 102, 1, upload)
	again := f.rpc(t, f.ace.PID, 102, 1, upload)
	if !bytes.Equal(first, again) || binary.LittleEndian.Uint32(first) == 0 || len(f.store.data.Files) != 1 {
		t.Fatal("duplicate upload created another file")
	}
	id := binary.LittleEndian.Uint32(first)
	download := f.rpc(t, f.bee.PID, 102, 2, put64(put32(nil, 7), uint64(id)))
	r := reader{b: download}
	if r.u8() != 1 || !bytes.Equal(r.blob(), payload) || !r.done() {
		t.Fatal("download was not the uploaded bytes")
	}
	listing := f.rpc(t, f.bee.PID, 102, 4, putstr(append(put32(put32(nil, 100), 0), 0), ""))
	r = reader{b: listing}
	if r.u32() != 1 || r.u64() != uint64(id) || r.u32() != 7 || r.str(255) != "Challenge" || r.str(255) != "Description" || r.str(15) != "Ace" {
		t.Fatal("challenge listing fields")
	}
	for range 7 {
		r.u32()
	}
	if r.u8() != 0 || !r.done() {
		t.Fatal("PS2 metadata word/flag count")
	}
	missing := f.rpc(t, f.bee.PID, 102, 2, put64(put32(nil, 7), uint64(id)+1))
	if !bytes.Equal(missing, make([]byte, 5)) {
		t.Fatal("missing file claimed success")
	}
	reload, err := Open(f.store.path)
	if err != nil || !bytes.Equal(reload.data.Files[id].Data, payload) {
		t.Fatal("challenge did not persist", err)
	}
	bad := putblob(putstr(putstr(putstr(put32(nil, 7), "x"), "x"), "Bee"), payload)
	if _, err := f.service.Handle(f.ace.PID, quazal.Request{Protocol: 102, Method: 1, Args: bad}); err == nil {
		t.Fatal("creator impersonation accepted")
	}
	for n := range len(upload) {
		if _, err := f.service.Handle(f.ace.PID, quazal.Request{Protocol: 102, Method: 1, Args: upload[:n]}); err == nil {
			t.Fatalf("accepted upload truncation%d", n)
		}
	}
}
func TestFailedDiskCommitDoesNotPublishReport(t *testing.T) {
	f := setup(t)
	f.store.path = filepath.Join(t.TempDir(), "missing", "state.json")
	wire, _ := GameStats{Name: "Ace", Values: [3]uint32{999}}.MarshalBinary()
	if _, err := f.service.Handle(f.ace.PID, quazal.Request{Protocol: 103, Method: 1, Args: wire}); err == nil {
		t.Fatal("disk failure returned success")
	}
	if len(f.store.data.Games) != 0 {
		t.Fatal("uncommitted report became visible")
	}
}
func TestInvalidStoredStateIsNotReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "title.json")
	bad := []byte(`{"Version":1,"NextFile":0}`)
	if err := os.WriteFile(path, bad, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("invalid stored state accepted")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, bad) {
		t.Fatal("invalid state silently replaced")
	}
}
func FuzzTitleRecordParsers(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1, 0, 0})
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = ParseEvent(b); _, _ = ParseGame(b) })
}

func TestCompiledMissingValueSentinelsDoNotOutrankReportedStats(t *testing.T) {
	d := diskState{Games: map[uint32]GameStats{1: {Name: "missing", Values: [3]uint32{^uint32(0), ^uint32(0), 0xbf800000}, Counter: ^uint32(0)}, 2: {Name: "reported", Values: [3]uint32{5, 6, math.Float32bits(1.5)}, Counter: 9}}}
	for _, selector := range []uint32{2, 3, 4, 6} {
		if got := sortedGames(d, selector); got[0].Name != "reported" {
			t.Fatalf("missing value ranked first for selector%d", selector)
		}
	}
}

func TestRetailExecutedRecordFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/native-records.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Kind       string
		WireHex    string `json:"wire_hex"`
		Name       string
		Values     []uint32
		EventID    string   `json:"event_id"`
		DoubleBits []string `json:"double_bits"`
		Counter    uint32
	}
	if err = json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatal("missing native record fixtures")
	}
	for _, row := range rows {
		expected, err := hex.DecodeString(row.WireHex)
		if err != nil {
			t.Fatal(err)
		}
		var actual []byte
		switch row.Kind {
		case "event":
			id, err := strconv.ParseUint(row.EventID, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			record := EventStats{Name: row.Name, EventID: id}
			copy(record.Values[:], row.Values)
			actual, err = record.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
		case "game":
			record := GameStats{Name: row.Name, Counter: row.Counter}
			copy(record.Values[:], row.Values)
			for i, v := range row.DoubleBits {
				record.DoubleBits[i], err = strconv.ParseUint(v, 10, 64)
				if err != nil {
					t.Fatal(err)
				}
			}
			actual, err = record.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("unknown native fixture")
		}
		if !bytes.Equal(actual, expected) {
			t.Fatalf("native %s record mismatch %x vs%x", row.Kind, actual, expected)
		}
	}
}
