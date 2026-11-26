package rsvpsp

import (
	"bytes"
	"encoding/binary"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// statRecord builds a reported record with eight floats and an empty second
// object, as the game sends in 60/12.
func statRecord(pid uint32, name string, values ...float32) []byte {
	b := cat(u32(pid), str(name), u32(2), u32(uint32(len(values))))
	for _, v := range values {
		b = append(b, u32(math.Float32bits(v))...)
	}
	return append(b, u32(0)...)
}

// statRow is one decoded statistics record.
type statRow struct {
	pid                         uint32
	name                        string
	rating, change, rank        float32
	stat5, hits, shots, minutes float32
}

func statRows(t *testing.T, b []byte) []statRow {
	t.Helper()
	r := reader{b: b}
	f := func() float32 { return math.Float32frombits(r.u32()) }
	rows := make([]statRow, r.u32())
	for i := range rows {
		row := statRow{pid: r.u32(), name: r.str(64)}
		if r.u32() != statusPlaying || r.u32() != statValues {
			t.Fatalf("stat record header %x", b)
		}
		row.rating, _, row.change, row.rank, _ = f(), f(), f(), f(), f()
		row.stat5, row.hits, row.shots = f(), f(), f()
		if r.u32() != 1 {
			t.Fatalf("play time object %x", b)
		}
		row.minutes = f()
		rows[i] = row
	}
	if !r.done() {
		t.Fatalf("stat list %x", b)
	}
	return rows
}

type statsRig struct {
	g      *games
	c      *careers
	s      *social
	path   string
	host   prudp.Peer
	joiner prudp.Peer
}

func newStatsRig(t *testing.T, dir string) statsRig {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	names := map[uint32]string{1000: "Sneed", 1002: "Tris", 1003: "Third"}
	g := newGames(log, func(pid uint32) string { return names[pid] })
	s, err := openSocial(filepath.Join(dir, "friends.json"), g, func(name string) (uint32, bool) {
		for pid, n := range names {
			if n == name {
				return pid, true
			}
		}
		return 0, false
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "stats.json")
	c, err := openCareers(path, g, s.friendsOf)
	if err != nil {
		t.Fatal(err)
	}
	s.rating = c.rating
	g.reported = func(results []matchResult, seconds uint32) {
		if err := c.commit(results, seconds); err != nil {
			t.Error(err)
		}
	}
	rig := statsRig{g: g, c: c, s: s, path: path, host: prudp.Peer{ID: 1}, joiner: prudp.Peer{ID: 2}}
	g.registered(rig.host, 1002, "")
	g.registered(rig.joiner, 1000, "")
	return rig
}

func (rig statsRig) spark(t *testing.T, peer prudp.Peer, pid, method uint32, args []byte) []byte {
	t.Helper()
	request := quazal.Request{Protocol: SparkProtocol, Method: method, Args: args}
	var reply []byte
	var err error
	switch method {
	case 6, 7, 8, 17:
		reply, err = rig.c.spark(pid, request)
	default:
		var ok bool
		reply, ok, err = rig.g.spark(peer, pid, request)
		if !ok {
			t.Fatalf("60/%d not handled", method)
		}
	}
	if err != nil {
		t.Fatalf("60/%d: %v", method, err)
	}
	return reply
}

// TestCareerStatsFromReports plays the field sequence: a game that is not
// game 1, both players reporting the same table after a round set, the next
// round, and then the four statistics views.
func TestCareerStatsFromReports(t *testing.T) {
	dir := t.TempDir()
	rig := newStatsRig(t, dir)
	// Another host takes ID 1, so this match is game 2.
	rig.g.registered(prudp.Peer{ID: 3}, 1003, "")
	rig.spark(t, prudp.Peer{ID: 3}, 1003, 4, createArgs(t))
	id := binary.LittleEndian.Uint32(rig.spark(t, rig.host, 1002, 4, createArgs(t)))
	if id != 2 {
		t.Fatalf("game %d", id)
	}
	rig.spark(t, rig.joiner, 1000, 5, cat(u32(id), []byte{0, 0}))

	table := cat(u32(2), statRecord(1000, "SNEED", 2, 0, 0, 0, 0, 0, 2, 93), statRecord(1002, "TRIS", 1, 0, 0, 0, 0, 0, 1, 3))
	rig.spark(t, rig.host, 1002, 12, cat(u32(id), u32(184), table))
	rig.spark(t, rig.joiner, 1000, 12, cat(u32(id), u32(182), table))

	// 60/9 sends the stat set 1 first, not the game ID, and still gets this
	// game's records.
	got := rig.spark(t, rig.joiner, 1000, 9, cat(u32(1), u32(2), u32(1000), u32(1002), u32(0), u32(20)))
	if !bytes.Equal(got, cat(u32(2), statRecord(1000, "SNEED", 2, 0, 0, 0, 0, 0, 2, 93), statRecord(1002, "TRIS", 1, 0, 0, 0, 0, 0, 1, 3))) {
		t.Fatalf("60/9 in game 2 %x", got)
	}

	// Each player counted once, with the first report's play time.
	sneed, tris := rig.c.players[1000], rig.c.players[1002]
	if sneed.Matches != 1 || tris.Matches != 1 || sneed.Seconds != 184 || sneed.Won != 2 || sneed.Hits != 2 || sneed.Shots != 93 {
		t.Fatalf("careers %+v %+v", sneed, tris)
	}
	if sneed.Rating != startRating+ratingK/2 || tris.Rating != startRating-ratingK/2 {
		t.Fatalf("ratings %v %v", sneed.Rating, tris.Rating)
	}

	// The next round is a new round set: counted again.
	rig.spark(t, rig.host, 1002, 18, u32(id))
	if next := binary.LittleEndian.Uint32(rig.spark(t, rig.host, 1002, 4, createNextRoundArgs(t))); next != id {
		t.Fatalf("next round game %d", next)
	}
	table = cat(u32(2), statRecord(1000, "SNEED", 0, 0, 0, 0, 0, 0, 0, 19), statRecord(1002, "TRIS", 3, 0, 0, 0, 0, 0, 4, 8))
	rig.spark(t, rig.joiner, 1000, 12, cat(u32(id), u32(500), table))
	if tris := rig.c.players[1002]; tris.Matches != 2 || tris.Won != 4 || tris.Hits != 5 || tris.Shots != 11 || tris.Seconds != 184+500 {
		t.Fatalf("second round %+v", tris)
	}

	// Leaderboard (17): best rating first, ranks 1 and 2.
	rows := statRows(t, rig.spark(t, rig.host, 1002, 17, cat(u32(1), u32(0), u32(20))))
	if len(rows) != 2 || rows[0].pid != 1002 || rows[0].rank != 1 || rows[1].pid != 1000 || rows[1].rank != 2 ||
		rows[0].hits != 5 || rows[0].shots != 11 || rows[0].minutes != 11 || rows[0].rating <= rows[1].rating {
		t.Fatalf("leaderboard %+v", rows)
	}
	if rows := statRows(t, rig.spark(t, rig.host, 1002, 17, cat(u32(1), u32(1), u32(20)))); len(rows) != 1 || rows[0].pid != 1000 {
		t.Fatalf("leaderboard page 2 %+v", rows)
	}
	// Own (7): the window around the caller.
	if rows := statRows(t, rig.spark(t, rig.joiner, 1000, 7, cat(u32(1), u32(0), u32(1)))); len(rows) != 1 || rows[0].pid != 1000 || rows[0].rank != 2 {
		t.Fatalf("own %+v", rows)
	}
	// Participants (8): the game's players.
	if rows := statRows(t, rig.spark(t, rig.joiner, 1000, 8, cat(u32(1), u32(id), u32(0), u32(20)))); len(rows) != 2 || rows[0].pid != 1002 || rows[1].pid != 1000 {
		t.Fatalf("participants %+v", rows)
	}
	// Friends (6): the caller, then friends, by rating.
	if rows := statRows(t, rig.spark(t, rig.joiner, 1000, 6, u32(1))); len(rows) != 1 || rows[0].pid != 1000 {
		t.Fatalf("friends without friends %+v", rows)
	}
	for _, step := range []struct {
		pid    uint32
		method uint32
		args   []byte
	}{{1000, 1, cat(u32(1002), u32(0), str(""))}, {1002, 3, u32(1000)}} {
		reply, ok, err := rig.s.friends(step.pid, quazal.Request{Protocol: FriendsProtocol, Method: step.method, Args: step.args})
		if !ok || err != nil || !bytes.Equal(reply, []byte{1}) {
			t.Fatalf("friends %d/%d: %x %v %v", step.pid, step.method, reply, ok, err)
		}
	}
	if rows := statRows(t, rig.spark(t, rig.joiner, 1000, 6, u32(1))); len(rows) != 2 || rows[0].pid != 1002 || rows[1].pid != 1000 {
		t.Fatalf("friends %+v", rows)
	}
	// The friends list shows the rating.
	list := friendRows(t, mustList(t, rig.s, 1000))
	if len(list) != 1 || math.Float32frombits(list[0].rating) != float32(rig.c.players[1002].Rating) {
		t.Fatalf("friends list rating %+v", list)
	}
	// A malformed request is refused.
	if _, err := rig.c.spark(1000, quazal.Request{Protocol: SparkProtocol, Method: 17, Args: cat(u32(2), u32(0), u32(20))}); err == nil {
		t.Fatal("unknown stat set accepted")
	}

	// Saved and reloaded.
	reopened, err := openCareers(rig.path, rig.g, rig.s.friendsOf)
	if err != nil || reopened.players[1002] != rig.c.players[1002] {
		t.Fatalf("reopened %+v %v", reopened, err)
	}
	if err := os.WriteFile(rig.path, []byte(`{"x":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openCareers(rig.path, rig.g, rig.s.friendsOf); err == nil {
		t.Fatal("bad stats key accepted")
	}
}

func mustList(t *testing.T, s *social, pid uint32) []byte {
	t.Helper()
	b, err := s.list(pid, nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// createNextRoundArgs is createArgs with the next round's only URL.
func createNextRoundArgs(t *testing.T) []byte {
	t.Helper()
	args := createArgs(t)
	r := reader{b: args}
	r.str(64)
	r.take(int(r.u32()))
	r.bool()
	head := args[:len(args)-len(r.b)]
	return cat(head, u32(1), str("udp:/fullhouse"))
}

func TestReportsWithOddValuesAreNotCounted(t *testing.T) {
	rig := newStatsRig(t, t.TempDir())
	id := binary.LittleEndian.Uint32(rig.spark(t, rig.host, 1002, 4, createArgs(t)))
	rig.spark(t, rig.joiner, 1000, 5, cat(u32(id), []byte{0, 0}))
	nan := float32(math.NaN())
	for _, table := range [][]byte{
		cat(u32(1), statRecord(1002, "TRIS")),                           // empty vector (an aborted match)
		cat(u32(1), statRecord(1002, "TRIS", 1, 2, 3)),                  // wrong length
		cat(u32(1), statRecord(1002, "TRIS", nan, 0, 0, 0, 0, 0, 0, 0)), // not a number
		cat(u32(1), statRecord(1002, "TRIS", -1, 0, 0, 0, 0, 0, 0, 0)),  // negative
		cat(u32(1), statRecord(4242, "NOBODY", 1, 0, 0, 0, 0, 0, 1, 1)), // not a player
	} {
		rig.spark(t, rig.host, 1002, 12, cat(u32(id), u32(60), table))
	}
	if len(rig.c.players) != 0 {
		t.Fatalf("counted %+v", rig.c.players)
	}
}
