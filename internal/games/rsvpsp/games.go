package rsvpsp

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

const (
	sparkGameType = "SparkGame"
	// Gathering base written before the SparkGame fields: id, owner and host
	// (u32), min and max (u16), four u32 (policy, argument, flags, state),
	// then the description String.
	baseFixed = 12 + 4 + 16
	// SparkGame fields from 0x08b9ce68: bool, twelve u32, bool.
	sparkFields = 1 + 12*4 + 1

	maxGames       = 256
	maxURLs        = 8
	maxURL         = 256
	maxDescription = 64
	maxPage        = 100
)

var errGame = errors.New("rsvpsp: unknown or malformed game")

// Participation notifications (14/1), routed by the game's handler 0x08b8c340
// on type/1000: 900 updates the player list of game param2 with player
// param1, its name (the String) and status type%1000, where 0 removes the
// player (0x08b8c3d8); 901 makes the players of game param1 refresh their
// stat records with 60/9 (0x08b8c868).
const (
	eventParticipation = 900000
	eventRefreshStats  = 901000
	// statusPlaying is what the host records for itself at creation
	// (0x08b8b354 → 0x08ba6290 with status 2).
	statusPlaying = 2
	maxPlayers    = 16
	maxCalls      = 256
)

// match is one hosted game. The record is the client's SparkGame any<Data>
// payload (body length, then the body) with the server's ID, owner and host
// written in; the other fields stay as the host sent them.
type match struct {
	id, owner uint32
	peer      prudp.Peer
	record    []byte
	urls      []string
	// hostURLs are the addresses a joiner connects to: the URLs sent with the
	// game, or for a next-round game (sent as "udp:/fullhouse") the previous
	// round's.
	hostURLs []string
	players  []uint32 // the host first, then joiners in order
	started  bool     // set by the host's 60/19 (in play); still listed
	// stats holds each player's latest reported record (60/12), served by
	// 60/9 and the join reply in place of an empty one.
	stats map[uint32][]byte
	// counted marks players whose result for this round has gone into their
	// career stats; every player reports the whole table.
	counted map[uint32]bool
}

// matchResult is one player's reported result for a round: the first stat
// object's eight values (see careers.go) and nothing else.
type matchResult struct {
	pid    uint32
	values [statValues]float32
}

// games tracks hosted matches in memory. A match belongs to the registration
// that created it and ends when the host creates another or leaves.
type games struct {
	mu    sync.Mutex
	log   *slog.Logger
	name  func(pid uint32) string
	byID  map[uint32]*match
	peers map[uint32]prudp.Peer // live named registrations, for notifications
	calls []quazal.ServerCall
	// reported receives each round's new results with the report's play time
	// in seconds. It is called with mu held and must not call back.
	reported func(results []matchResult, seconds uint32)
}

func newGames(log *slog.Logger, name func(uint32) string) *games {
	return &games{log: log, name: name, byID: make(map[uint32]*match), peers: make(map[uint32]prudp.Peer)}
}

// registered records the connection that receives a player's notifications.
func (g *games) registered(peer prudp.Peer, pid uint32, _ string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.peers[pid] = peer
}

// serverCalls is drained by the secure transport.
func (g *games) serverCalls() []quazal.ServerCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	calls := g.calls
	g.calls = nil
	return calls
}

// notifyLocked queues ProcessNotificationEvent (14/1: pidSource, type,
// param1, param2, String) to every player of m except skip.
func (g *games) notifyLocked(m *match, skip, source, kind, param1, param2 uint32, text string) {
	args := putU32(nil, source)
	args = putU32(args, kind)
	args = putU32(args, param1)
	args = putU32(args, param2)
	args = putString(args, text)
	for _, pid := range m.players {
		peer, live := g.peers[pid]
		if pid == skip || !live || len(g.calls) >= maxCalls {
			continue
		}
		g.calls = append(g.calls, quazal.ServerCall{Peer: peer, Protocol: 14, Method: 1, Args: args})
	}
}

// putRecord writes a player record as the game reads it (0x08ba6bc0): PID,
// name, status, then two nested stat objects. The player's latest reported
// record is used when there is one; otherwise both objects are empty (u32 0
// each, as in a report before any play).
func (g *games) putRecord(b []byte, m *match, pid uint32) []byte {
	if record, ok := m.stats[pid]; ok {
		return append(b, record...)
	}
	b = putU32(b, pid)
	b = putString(b, g.name(pid))
	b = putU32(b, statusPlaying)
	b = putU32(b, 0)
	return putU32(b, 0)
}

func (m *match) has(pid uint32) bool { return slices.Contains(m.players, pid) }

// matchOfLocked returns the game pid is a player of (the lowest ID if more
// than one), or nil.
func (g *games) matchOfLocked(pid uint32) *match {
	var found *match
	for _, m := range g.byID {
		if m.has(pid) && (found == nil || m.id < found.id) {
			found = m
		}
	}
	return found
}

// players returns the players of game id, or nil.
func (g *games) players(id uint32) []uint32 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if m := g.byID[id]; m != nil {
		return slices.Clone(m.players)
	}
	return nil
}

func samePeer(a, b prudp.Peer) bool { return a.ID == b.ID && a.LocalPort == b.LocalPort }

// checkGame validates a SparkGame body: base header, description, then the
// fixed SparkGame fields with nothing after them.
func checkGame(payload []byte) error {
	if len(payload) < 4+baseFixed+2+sparkFields || uint64(binary.LittleEndian.Uint32(payload)) != uint64(len(payload)-4) {
		return errGame
	}
	r := reader{b: payload[4+baseFixed:]}
	r.str(maxDescription)
	r.take(sparkFields)
	if !r.done() {
		return errGame
	}
	return nil
}

// description returns a stored SparkGame record's description (the Gathering
// base String after the fixed fields).
func description(record []byte) string {
	r := reader{b: record[4+baseFixed:]}
	return r.str(maxDescription)
}

func readURLs(r *reader) []string {
	count := r.u32()
	if count > maxURLs {
		r.err = quazal.ErrRMC
		return nil
	}
	urls := make([]string, 0, count)
	for range count {
		url := r.str(maxURL)
		if r.err == nil && !strings.HasPrefix(url, "udp:/") && !strings.HasPrefix(url, "prudp:/") {
			r.err = quazal.ErrRMC
		}
		urls = append(urls, url)
	}
	return urls
}

// create is Spark 60/4: any<SparkGame>, bool, List<StationURL> (the host's
// game socket addresses). The reply is the new game ID (u32).
func (g *games) create(peer prudp.Peer, pid uint32, args []byte) ([]byte, error) {
	r := reader{b: args}
	kind := r.str(64)
	payload := r.take(int(r.u32()))
	flag := r.bool()
	urls := readURLs(&r)
	if !r.done() || kind != sparkGameType || len(urls) == 0 || checkGame(payload) != nil {
		return nil, errGame
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	// A host re-creates the match for the next round right after 60/18
	// (0x08bb8a20), advertising only "udp:/fullhouse". Its new game context
	// lists only itself, so the previous round's players are carried over
	// and announced as for a join.
	//
	// The next round keeps the previous game's ID: the other players' game
	// contexts still hold it, and every notification is matched against it
	// (900 on param2, 901 on param1). New games take the lowest free ID.
	// (Field tests once suggested only game 1 could reach the next round;
	// the cause was 60/9 reading its stat set 1 as a game ID, see records.)
	nextRound := slices.Equal(urls, []string{"udp:/fullhouse"})
	carried := []uint32{pid}
	var previous uint32
	hostURLs := urls
	for id, old := range g.byID {
		if old.owner == pid {
			if nextRound {
				hostURLs = old.hostURLs
			}
			for _, player := range old.players {
				if !slices.Contains(carried, player) {
					carried = append(carried, player)
				}
			}
			if previous == 0 || id < previous {
				previous = id
			}
			delete(g.byID, id)
		}
	}
	if len(g.byID) >= maxGames {
		return nil, errors.New("rsvpsp: game capacity reached")
	}
	id := uint32(1)
	if nextRound && previous != 0 {
		id = previous
	} else {
		for g.byID[id] != nil {
			id++
		}
	}
	record := append([]byte(nil), payload...)
	binary.LittleEndian.PutUint32(record[4:], id)
	binary.LittleEndian.PutUint32(record[8:], pid)
	binary.LittleEndian.PutUint32(record[12:], pid)
	m := &match{id: id, owner: pid, peer: peer, record: record, urls: urls, hostURLs: hostURLs, players: []uint32{pid}}
	g.byID[id] = m
	if nextRound {
		m.players = carried
		for _, player := range carried[1:] {
			g.notifyLocked(m, player, player, eventParticipation+statusPlaying, player, id, g.name(player))
		}
		if len(carried) > 1 {
			g.notifyLocked(m, 0, pid, eventRefreshStats, id, 0, "")
		}
	}
	g.log.Info("rsvpsp game created", "game", id, "owner", pid, "flag", flag, "urls", urls, "players", m.players)
	return putU32(nil, id), nil
}

// join is Spark 60/5: game ID, bool, bool. The reply carries the host's
// addresses (List<String>), the players' records (the joiner's new player
// list) and the game. The other players are told through notifications.
func (g *games) join(pid uint32, args []byte) ([]byte, error) {
	r := reader{b: args}
	id := r.u32()
	r.bool()
	r.bool()
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.byID[id]
	if m == nil {
		return nil, errGame
	}
	if !m.has(pid) {
		if len(m.players) >= maxPlayers {
			return nil, errors.New("rsvpsp: game is full")
		}
		m.players = append(m.players, pid)
		g.notifyLocked(m, pid, pid, eventParticipation+statusPlaying, pid, id, g.name(pid))
		g.notifyLocked(m, pid, pid, eventRefreshStats, id, 0, "")
	}
	reply := putU32(nil, uint32(len(m.hostURLs)))
	for _, url := range m.hostURLs {
		reply = putString(reply, url)
	}
	reply = putU32(reply, uint32(len(m.players)))
	for _, player := range m.players {
		reply = g.putRecord(reply, m, player)
	}
	reply = putAny(reply, quazal.AnyData{Type: sparkGameType, Payload: m.record})
	g.log.Info("rsvpsp game joined", "game", id, "pid", pid, "owner", m.owner, "players", m.players)
	return reply, nil
}

// start is Spark 60/19 (game ID, no reply fields), sent by the host's game
// context (0x08b8b014, host flag at context+0x68) when it enters the game
// page 0xfb6 (0x0893207c): at the first launch into the Unreal lobby, and
// again whenever the players return to the lobby after a round set. The
// game stays listed and joinable: the game itself offers joining in progress
// ("Join in progress", T_MsgBoxText_JoinServerGame). Calls by anyone else do
// nothing.
func (g *games) start(pid uint32, args []byte) ([]byte, error) {
	r := reader{b: args}
	id := r.u32()
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if m := g.byID[id]; m != nil && m.owner == pid {
		m.started = true
		g.log.Info("rsvpsp game started", "game", id, "owner", pid, "players", m.players)
	}
	return nil, nil
}

// leave is Spark 60/16 (game ID, u32 play time; void, stub 0x8010), sent by
// 0x08b8b9e8 when a player's game context closes without a report, i.e. the
// player leaves the game. A player is taken out of the game and the others
// are told (900000, status 0), so a later 60/5 adds and announces them again.
// The host leaving closes the game.
func (g *games) leave(pid uint32, args []byte) ([]byte, error) {
	r := reader{b: args}
	id, seconds := r.u32(), r.u32()
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.byID[id]
	switch {
	case m == nil || !m.has(pid):
		g.log.Info("rsvpsp leave ignored", "game", id, "pid", pid, "seconds", seconds)
	case m.owner == pid:
		delete(g.byID, id)
		g.log.Info("rsvpsp game closed", "game", id, "owner", pid, "reason", "host left")
	default:
		m.players = slices.DeleteFunc(m.players, func(p uint32) bool { return p == pid })
		g.notifyLocked(m, pid, pid, eventParticipation, pid, id, g.name(pid))
		g.log.Info("rsvpsp player left", "game", id, "pid", pid, "players", m.players, "reason", "left")
	}
	return nil, nil
}

// nextHost is Spark 60/18 (game ID), sent by the host's debriefing state
// (0x08bb8878) after the end-of-match reports. The reply is the PID that
// hosts the next round (stub 0x8012: u32); the success handler 0x08bb8a20
// compares it with its own PID and, if equal, sets the match up again from
// the current settings. The current host keeps hosting.
func (g *games) nextHost(pid uint32, args []byte) ([]byte, error) {
	r := reader{b: args}
	id := r.u32()
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.byID[id]
	if m == nil || !m.has(pid) {
		return nil, errGame
	}
	g.log.Info("rsvpsp next host", "game", id, "pid", pid, "host", m.owner)
	return putU32(nil, m.owner), nil
}

// records is Spark 60/9: u32 stat set, List<u32> PIDs, ResultRange, sent
// after a 901 notification (0x08b8c868 → 0x08b91258). The first value is the
// constant 1 that every statistics call sends, not a game ID; the PIDs are
// looked up in the caller's current game. The reply is List<stat record> for
// those PIDs that are players of it, with their latest reported records.
//
// Reading the 1 as a game ID made every game other than game 1 answer with an
// empty list, so the results screen never moved on; that was the reason only
// game 1 reached the next round.
func (g *games) records(pid uint32, args []byte) ([]byte, error) {
	r := reader{b: args}
	r.u32()
	count := r.u32()
	if count > maxPlayers {
		return nil, quazal.ErrRMC
	}
	pids := make([]uint32, 0, count)
	for range count {
		pids = append(pids, r.u32())
	}
	r.u32()
	r.u32()
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var listed []uint32
	m := g.matchOfLocked(pid)
	if m != nil {
		for _, pid := range pids {
			if m.has(pid) && !slices.Contains(listed, pid) {
				listed = append(listed, pid)
			}
		}
	}
	reply := putU32(nil, uint32(len(listed)))
	for _, pid := range listed {
		reply = g.putRecord(reply, m, pid)
	}
	return reply, nil
}

// search answers browse (60/20: seven filter Strings, ResultRange) and quick
// match (60/21: bool, u32, u32, ResultRange). Filters are not applied yet:
// every other player's game is listed. The reply is List<any<SparkGame>> then
// List<GatheringURLs> (u32 ID, List<StationURL>).
func (g *games) search(pid uint32, request quazal.Request) ([]byte, error) {
	r := reader{b: request.Args}
	if request.Method == 20 {
		for range 7 {
			r.str(256)
		}
	} else {
		r.bool()
		r.u32()
		r.u32()
	}
	offset, size := r.u32(), r.u32()
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	listed := make([]*match, 0, len(g.byID))
	for _, m := range g.byID {
		if m.owner != pid {
			listed = append(listed, m)
		}
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].id < listed[j].id })
	if uint64(offset) >= uint64(len(listed)) {
		listed = nil
	} else {
		listed = listed[offset:]
	}
	if size > maxPage {
		size = maxPage
	}
	if uint64(len(listed)) > uint64(size) {
		listed = listed[:size]
	}
	reply := putU32(nil, uint32(len(listed)))
	for _, m := range listed {
		reply = putAny(reply, quazal.AnyData{Type: sparkGameType, Payload: m.record})
	}
	reply = putU32(reply, uint32(len(listed)))
	for _, m := range listed {
		reply = putU32(reply, m.id)
		reply = putU32(reply, uint32(len(m.urls)))
		for _, url := range m.urls {
			reply = putString(reply, url)
		}
	}
	return reply, nil
}

// report is Spark 60/12, sent by every player when a match ends: game ID,
// u32, then a list of stat records. The reply is void (stub 0x800c).
//
// Each record is PID, String name, u32 status, then two stat objects, each
// a u32 count and that many 4-byte values (floats such as kills, hits and
// shots in the first). The latest record per player is kept for 60/9.
//
// The host's results screen handles its end-of-match event (0x7eb) only
// once a player-records request (60/9) has completed while it is showing
// (condition 0x08bb86d4 on 0x08b8bed4, consumed by 0x08bb81ac); without one
// the host stays on "Waiting for other players". Every report therefore
// sends the game's players 901000, which makes them fetch 60/9 again.
func (g *games) report(pid uint32, args []byte) ([]byte, error) {
	r := reader{b: args}
	id, value, count := r.u32(), r.u32(), r.u32()
	if r.err != nil || count > maxPlayers || len(args) > 4096 {
		return nil, quazal.ErrRMC
	}
	records := make(map[uint32][]byte, count)
	results := make(map[uint32]matchResult, count)
	for range count {
		start := len(args) - len(r.b)
		player := r.u32()
		r.str(64)
		r.u32()
		var first []byte
		for i := range 2 {
			n := r.u32()
			if n > 64 {
				r.err = quazal.ErrRMC
			}
			values := r.take(int(n) * 4)
			if i == 0 {
				first = values
			}
		}
		if r.err != nil {
			break
		}
		records[player] = bytes.Clone(args[start : len(args)-len(r.b)])
		if len(first) == statValues*4 {
			result := matchResult{pid: player}
			for i := range result.values {
				result.values[i] = math.Float32frombits(binary.LittleEndian.Uint32(first[i*4:]))
			}
			results[player] = result
		}
	}
	parsed := r.done()
	g.log.Info("rsvpsp match report", "game", id, "pid", pid, "value", value, "records", count, "parsed", parsed, "args", hex.EncodeToString(args))
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.byID[id]
	if m == nil || !m.has(pid) {
		return nil, nil
	}
	if parsed {
		if m.stats == nil {
			m.stats = make(map[uint32][]byte)
		}
		for player, record := range records {
			if m.has(player) {
				m.stats[player] = record
			}
		}
		// Career stats take each player's first reported result for this
		// round; the other players' copies of the same table are ignored.
		if m.counted == nil {
			m.counted = make(map[uint32]bool)
		}
		var fresh []matchResult
		for _, player := range m.players {
			if result, ok := results[player]; ok && !m.counted[player] && validResult(result) {
				m.counted[player] = true
				fresh = append(fresh, result)
			}
		}
		if len(fresh) > 0 && g.reported != nil {
			g.reported(fresh, value)
		}
	}
	g.notifyLocked(m, 0, pid, eventRefreshStats, id, 0, "")
	return nil, nil
}

// updateURLs is MatchMaking 21/39: game ID and the caller's StationURLs,
// sent by a player after joining. The reply is void (stub 0x8027).
func (g *games) updateURLs(pid uint32, args []byte) ([]byte, error) {
	r := reader{b: args}
	id := r.u32()
	urls := readURLs(&r)
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	g.log.Info("rsvpsp player URLs", "game", id, "pid", pid, "urls", urls)
	return nil, nil
}

// probeExt is NAT traversal 3/3: target StationURLs and the caller's own URL,
// asking the server to have the targets probe the caller. The reply is void
// (stub 0x8003). On one LAN the players reach each other directly, so it is
// acknowledged without sending probes; play across NATs will need them.
func (g *games) probeExt(pid uint32, args []byte) ([]byte, error) {
	r := reader{b: args}
	targets := readURLs(&r)
	source := r.str(maxURL)
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	g.log.Info("rsvpsp probe request acknowledged", "pid", pid, "targets", targets, "source", source)
	return nil, nil
}

// findByDescription is MatchMaking 21/18: String description, ResultRange.
// The reply is List<any<Gathering>> (stub 0x8012 → 0x08ba47c4). A player
// following the host into the next round calls it after 60/18 with the new
// game's description, "(<previous game ID>)<host name>" (format "(%d)%s",
// 0x08bb896c), then joins the game it finds.
func (g *games) findByDescription(args []byte) ([]byte, error) {
	r := reader{b: args}
	text := r.str(maxDescription)
	offset, size := r.u32(), r.u32()
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var found []*match
	for _, m := range g.byID {
		if description(m.record) == text {
			found = append(found, m)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].id < found[j].id })
	if uint64(offset) >= uint64(len(found)) {
		found = nil
	} else {
		found = found[offset:]
	}
	if uint64(len(found)) > uint64(min(size, maxPage)) {
		found = found[:min(size, maxPage)]
	}
	reply := putU32(nil, uint32(len(found)))
	for _, m := range found {
		reply = putAny(reply, quazal.AnyData{Type: sparkGameType, Payload: m.record})
	}
	g.log.Info("rsvpsp game search by description", "description", text, "found", len(found))
	return reply, nil
}

// disconnect closes the games a registration hosted and removes the player
// from the games it joined, telling the remaining players.
func (g *games) disconnect(peer prudp.Peer, pid uint32) {
	g.mu.Lock()
	defer g.mu.Unlock()
	// Only the player's current registration leaving takes it out of games.
	live, current := g.peers[pid]
	current = current && samePeer(live, peer)
	if current {
		delete(g.peers, pid)
	}
	for id, m := range g.byID {
		switch {
		case m.owner == pid && samePeer(m.peer, peer):
			delete(g.byID, id)
			g.log.Info("rsvpsp game closed", "game", id, "owner", pid, "reason", "host disconnected")
		case current && m.owner != pid && m.has(pid):
			m.players = slices.DeleteFunc(m.players, func(p uint32) bool { return p == pid })
			g.notifyLocked(m, pid, pid, eventParticipation, pid, id, g.name(pid))
			g.log.Info("rsvpsp player left", "game", id, "pid", pid, "players", m.players)
		}
	}
}
