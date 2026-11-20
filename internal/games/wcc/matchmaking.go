package wcc

import (
	"encoding/binary"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// Gathering classes declared by the title's DDL metadata (namespace WCCPS2).
// Their title fields follow the SDK Gathering base and are kept opaque: the
// server stores the client's encoding and only rewrites server-owned fields.
var gatheringTypes = map[string]bool{"WCCGathering": true, "WackyGathering": true, "ChatGathering": true}

// any<Gathering> buffer offsets shared with the SDK base class.
const (
	offsetBody        = 0  // u32 length of the remainder
	offsetID          = 4  // u32 gathering ID (server-assigned)
	offsetOwner       = 8  // u32 owner PID (server-owned)
	offsetHost        = 12 // u32 host PID (server-owned)
	offsetMax         = 18 // u16 maximum participants
	offsetState       = 32 // u32 application state
	offsetDescription = 36 // String description, then title fields

	maxGatheringPayload   = 2048
	maxGatherings         = 1024
	maxOwnedPerConnection = 4
	defaultCapacity       = 16
	maxCapacity           = 64
	maxPage               = 100
	orphanGrace           = 30 * time.Second
)

var errUnsupported = errors.New("unsupported matchmaking method")

type connection struct {
	port uint16
	peer uint64
	pid  uint32
}

func connectionOf(peer prudp.Peer, pid uint32) connection {
	return connection{peer.LocalPort, peer.ID, pid}
}

type liveConnection struct {
	peer prudp.Peer
	url  string
}

type gathering struct {
	owner      connection
	data       quazal.AnyData
	members    map[connection]string
	sessionURL string
	// A departed owner's gathering stays reachable by its participants until
	// one of them claims it with 21/36 MigrateGatheringOwnership.
	orphanedUntil time.Time
}

type registry struct {
	mu         sync.Mutex
	log        *slog.Logger
	now        func() time.Time
	name       func(uint32) string
	next       uint32
	gatherings map[uint32]*gathering
	live       map[connection]liveConnection
	// Server-initiated NAT probes (3/2) and other calls (14/1 notification
	// events) queued for the secure transport.
	probes    []quazal.ProbeInstruction
	calls     []quazal.ServerCall
	lastProbe map[[2]connection]time.Time
}

func newRegistry(log *slog.Logger, name func(uint32) string) *registry {
	if name == nil {
		name = func(uint32) string { return "" }
	}
	return &registry{log: log, now: time.Now, name: name, next: 1000, gatherings: map[uint32]*gathering{}, live: map[connection]liveConnection{}, lastProbe: map[[2]connection]time.Time{}}
}

// validateGathering checks the SDK base header and bounds without
// interpreting the title-specific tail.
func validateGathering(data quazal.AnyData) error {
	p := data.Payload
	if !gatheringTypes[data.Type] || len(p) > maxGatheringPayload || len(p) < offsetDescription+3 {
		return quazal.ErrRMC
	}
	if binary.LittleEndian.Uint32(p[offsetBody:]) != uint32(len(p)-4) {
		return quazal.ErrRMC
	}
	min, max := binary.LittleEndian.Uint16(p[16:]), binary.LittleEndian.Uint16(p[offsetMax:])
	if max != 0 && min > max {
		return quazal.ErrRMC
	}
	r := reader{b: p[offsetDescription:]}
	r.str(256)
	return r.err
}

func capacity(g *gathering) int {
	max := int(binary.LittleEndian.Uint16(g.data.Payload[offsetMax:]))
	if max == 0 {
		return defaultCapacity
	}
	return min(max, maxCapacity)
}

func (g *gathering) stamp(id, owner uint32) {
	binary.LittleEndian.PutUint32(g.data.Payload[offsetID:], id)
	binary.LittleEndian.PutUint32(g.data.Payload[offsetOwner:], owner)
	binary.LittleEndian.PutUint32(g.data.Payload[offsetHost:], owner)
}

// WCCGathering and WackyGathering share a 26-byte title tail after the
// description: gameRules, eyeToyOnly, pad, timeout, isPrivate, rating (one
// byte each), numPlayers (int16 at tail offset 6), isTournament, zStarted,
// strPassword[16]. Confirmed against retail captures (numPlayers 1 → 2 on join).
const (
	cardTableTail       = 26
	cardTableNumPlayers = 6
)

// syncPlayers keeps the listed player count equal to the live participants
// after someone leaves or ownership moves. The retail host maintains it with
// 21/4 while it is present, but a departing host hands off (21/36) and the
// successor sends no update, which left the table listed as full.
func (g *gathering) syncPlayers() {
	if g.data.Type != "WCCGathering" && g.data.Type != "WackyGathering" {
		return
	}
	r := reader{b: g.data.Payload[offsetDescription:]}
	r.str(256)
	if r.err != nil || len(r.b) != cardTableTail {
		return
	}
	at := len(g.data.Payload) - cardTableTail + cardTableNumPlayers
	binary.LittleEndian.PutUint16(g.data.Payload[at:], uint16(len(g.memberPIDs())))
}

func (g *gathering) memberPIDs() []uint32 {
	seen := map[uint32]bool{}
	for c := range g.members {
		seen[c.pid] = true
	}
	ids := make([]uint32, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (g *gathering) hasPID(pid uint32) bool {
	for c := range g.members {
		if c.pid == pid {
			return true
		}
	}
	return false
}

func (s *registry) registered(peer prudp.Peer, pid uint32, url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[connectionOf(peer, pid)] = liveConnection{peer: peer, url: url}
}

func (s *registry) disconnect(peer prudp.Peer, pid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := connectionOf(peer, pid)
	delete(s.live, c)
	for id, g := range s.gatherings {
		if _, member := g.members[c]; member {
			delete(g.members, c)
			g.syncPlayers()
		}
		if g.owner != c {
			continue
		}
		if len(g.members) == 0 {
			delete(s.gatherings, id)
			s.log.Info("wcc gathering closed", "gid", id, "reason", "owner disconnected")
			continue
		}
		g.orphanedUntil = s.now().Add(orphanGrace)
		s.log.Info("wcc gathering awaiting migration", "gid", id, "participants", len(g.members))
	}
}

func (s *registry) pruneLocked() {
	now := s.now()
	for id, g := range s.gatherings {
		if !g.orphanedUntil.IsZero() && !now.Before(g.orphanedUntil) {
			delete(s.gatherings, id)
			s.log.Info("wcc gathering closed", "gid", id, "reason", "migration window expired")
		}
	}
}

// visible reports whether a gathering is listed to everyone.
func (g *gathering) visible() bool { return g.orphanedUntil.IsZero() }

func (s *registry) pageLocked(ids []uint32, offset, size uint32) []byte {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	size = min(size, maxPage)
	start := min(uint64(offset), uint64(len(ids)))
	end := min(start+uint64(size), uint64(len(ids)))
	b := putU32(nil, uint32(end-start))
	for _, id := range ids[start:end] {
		b = putAny(b, s.gatherings[id].data)
	}
	return b
}

func (s *registry) urlsLocked(g *gathering) []string {
	seen := map[string]bool{}
	var urls []string
	add := func(u string) {
		if u != "" && !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	if g.sessionURL != "" {
		add(g.sessionURL)
	} else if g.visible() {
		add(s.live[g.owner].url)
	}
	members := make([]connection, 0, len(g.members))
	for c := range g.members {
		members = append(members, c)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].pid < members[j].pid })
	for _, c := range members {
		add(s.live[c].url)
	}
	return urls
}

func validSessionURL(url string) bool {
	return len(url) <= 512 && (strings.HasPrefix(url, "prudp:/") || strings.HasPrefix(url, "prudps:/"))
}

// matchmaking handles the MatchMakingProtocol (21) methods called by the
// title (1, 2, 4, 11, 12, 16, 17, 21, 26, 29, 30, 36) plus read-only
// companions that share the same records.
func (s *registry) matchmaking(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
	c := connectionOf(peer, pid)
	r := reader{b: request.Args}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if _, ok := s.live[c]; !ok {
		return nil, errors.New("matchmaking requires a registered connection")
	}
	switch request.Method {
	case 1: // RegisterGathering(any<Gathering>) -> u32 gid
		data := r.any(maxGatheringPayload)
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		if err := validateGathering(data); err != nil {
			s.log.Warn("wcc gathering rejected", "pid", pid, "type", data.Type, "bytes", len(data.Payload))
			return nil, err
		}
		owned := 0
		for _, g := range s.gatherings {
			if g.owner == c {
				owned++
			}
		}
		if owned >= maxOwnedPerConnection || len(s.gatherings) >= maxGatherings {
			return nil, errors.New("gathering capacity reached")
		}
		for s.gatherings[s.next] != nil || s.next < 1000 {
			s.next++
		}
		id := s.next
		s.next++
		g := &gathering{owner: c, data: data, members: map[connection]string{}}
		g.stamp(id, pid)
		s.gatherings[id] = g
		s.log.Info("wcc gathering registered", "gid", id, "pid", pid, "type", data.Type)
		return putU32(nil, id), nil
	case 4: // UpdateGathering(any<Gathering>) -> bool
		data := r.any(maxGatheringPayload)
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		if err := validateGathering(data); err != nil {
			return nil, err
		}
		id := binary.LittleEndian.Uint32(data.Payload[offsetID:])
		g := s.gatherings[id]
		if g == nil || g.owner != c || g.data.Type != data.Type || !g.visible() {
			return putBool(nil, false), nil
		}
		next := &gathering{data: data}
		if len(g.members) > capacity(next) {
			return putBool(nil, false), nil
		}
		g.data = data
		g.stamp(id, pid)
		return putBool(nil, true), nil
	case 17: // FindByType(String, ResultRange) -> list<any<Gathering>>
		kind := r.str(64)
		offset, size := r.u32(), r.u32()
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		var ids []uint32
		for id, g := range s.gatherings {
			if g.visible() && g.data.Type == kind {
				ids = append(ids, id)
			}
		}
		return s.pageLocked(ids, offset, size), nil
	case 20: // FindByID(list<u32>) -> list<any<Gathering>>
		wanted := r.ids(256)
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		var ids []uint32
		seen := map[uint32]bool{}
		for _, id := range wanted {
			if g := s.gatherings[id]; g != nil && g.visible() && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		return s.pageLocked(ids, 0, maxPage), nil
	case 22: // FindByOwner(u32, ResultRange) -> list<any<Gathering>>
		owner := r.u32()
		offset, size := r.u32(), r.u32()
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		var ids []uint32
		for id, g := range s.gatherings {
			if g.visible() && g.owner.pid == owner {
				ids = append(ids, id)
			}
		}
		return s.pageLocked(ids, offset, size), nil
	case 36: // MigrateGatheringOwnership(u32, list<u32>) -> bool
		id := r.u32()
		candidates := r.ids(32)
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		return putBool(nil, s.migrateLocked(c, id, candidates)), nil
	}

	id := r.u32()
	var text string
	var state uint32
	switch request.Method {
	case 11, 12, 26, 27:
		text = r.str(512)
	case 30:
		state = r.u32()
	case 2, 13, 15, 16, 21, 28, 29:
	default:
		return nil, errUnsupported
	}
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	g := s.gatherings[id]
	member := g != nil && (g.owner == c || g.hasPID(pid))
	switch request.Method {
	case 2: // UnregisterGathering(u32) -> bool
		if g == nil || g.owner != c {
			return putBool(nil, false), nil
		}
		delete(s.gatherings, id)
		s.log.Info("wcc gathering closed", "gid", id, "reason", "unregistered")
		return putBool(nil, true), nil
	case 11: // Participate(u32, String) -> bool
		if g == nil {
			return putBool(nil, false), nil
		}
		if _, ok := g.members[c]; ok {
			return putBool(nil, true), nil
		}
		if !g.visible() && !member || len(g.members) >= capacity(g) {
			return putBool(nil, false), nil
		}
		g.members[c] = text
		s.log.Info("wcc participant joined", "gid", id, "pid", pid, "participants", len(g.members))
		return putBool(nil, true), nil
	case 12: // CancelParticipation(u32, String) -> bool
		return putBool(nil, s.leaveLocked(c, id)), nil
	case 13: // GetParticipants(u32) -> list<u32>
		if g == nil {
			return putU32(nil, 0), nil
		}
		ids := g.memberPIDs()
		b := putU32(nil, uint32(len(ids)))
		for _, id := range ids {
			b = putU32(b, id)
		}
		return b, nil
	case 15: // GetDetailedParticipants(u32) -> list<ParticipantDetails>
		if g == nil {
			return putU32(nil, 0), nil
		}
		ids := g.memberPIDs()
		b := putU32(nil, uint32(len(ids)))
		for _, member := range ids {
			message := ""
			for mc, m := range g.members {
				if mc.pid == member {
					message = m
					break
				}
			}
			b = putU32(b, member)
			b = putString(b, s.name(member))
			b = putString(b, message)
		}
		return b, nil
	case 16: // GetParticipantsURLs(u32) -> list<StationURL>
		if g == nil || !g.visible() && !member {
			return putU32(nil, 0), nil
		}
		urls := s.urlsLocked(g)
		b := putU32(nil, uint32(len(urls)))
		for _, u := range urls {
			b = putString(b, u)
		}
		return b, nil
	case 21: // FindBySingleID(u32) -> bool, any<Gathering>
		if g == nil || !g.visible() && !member {
			return putAny(putBool(nil, false), quazal.AnyData{}), nil
		}
		return putAny(putBool(nil, true), g.data), nil
	case 26, 27: // LaunchSession / UpdateSessionURL(u32, String) -> bool
		// The retail host launches with an empty URL: peers then reach it
		// through its registered StationURL (21/16, 11/3).
		if g == nil || g.owner != c || text != "" && !validSessionURL(text) {
			return putBool(nil, false), nil
		}
		g.sessionURL = text
		s.log.Info("wcc session launched", "gid", id, "pid", pid, "method", request.Method)
		return putBool(nil, true), nil
	case 28: // GetSessionURL(u32) -> bool, String
		ok := g != nil && g.sessionURL != "" && (g.visible() || member)
		url := ""
		if ok {
			url = g.sessionURL
		}
		return putString(putBool(nil, ok), url), nil
	case 29: // GetState(u32) -> bool, u32
		if g == nil {
			return putU32(putBool(nil, false), 0), nil
		}
		return putU32(putBool(nil, true), binary.LittleEndian.Uint32(g.data.Payload[offsetState:])), nil
	case 30: // SetState(u32, u32) -> bool
		if g == nil || g.owner != c || !g.visible() {
			return putBool(nil, false), nil
		}
		binary.LittleEndian.PutUint32(g.data.Payload[offsetState:], state)
		return putBool(nil, true), nil
	}
	return nil, errUnsupported
}

func (s *registry) leaveLocked(c connection, id uint32) bool {
	g := s.gatherings[id]
	if g == nil {
		return false
	}
	if _, ok := g.members[c]; !ok {
		return false
	}
	delete(g.members, c)
	g.syncPlayers()
	// At the end of a game the retail host migrates ownership to a remaining
	// player (21/36) and leaves; that player then leaves too. A gathering
	// whose last participant has left is finished, whoever still owns it.
	if len(g.members) == 0 {
		delete(s.gatherings, id)
		s.log.Info("wcc gathering closed", "gid", id, "reason", "last participant left")
	}
	return true
}

// migrateLocked transfers ownership to the first candidate that is a live
// participant. The current owner may hand off; after the owner departs, any
// remaining participant may claim the orphaned gathering.
func (s *registry) migrateLocked(caller connection, id uint32, candidates []uint32) bool {
	g := s.gatherings[id]
	if g == nil {
		return false
	}
	_, participant := g.members[caller]
	if g.owner != caller && !(participant && !g.visible()) {
		return false
	}
	for _, pid := range candidates {
		for mc := range g.members {
			if mc.pid != pid {
				continue
			}
			if _, ok := s.live[mc]; !ok {
				continue
			}
			g.owner = mc
			g.orphanedUntil = time.Time{}
			g.sessionURL = ""
			g.stamp(id, pid)
			g.syncPlayers()
			s.log.Info("wcc gathering migrated", "gid", id, "owner", pid)
			s.notifyOwnershipLocked(g, caller, id, pid)
			return true
		}
	}
	return false
}

// Notification event categories handled by the title's handler 0x0028e7b8,
// which dispatches on type/1000. Category 4 (ownership changed) calls
// 0x002950d8 with uiParam2; when that is the local PID the client takes over
// as table host. Without it the successor never acts as owner (no 21/4
// updates) and the table cannot be started.
const ownershipChanged = 4000

// notifyOwnershipLocked sends ProcessNotificationEvent (14/1: pidSource,
// type, param1, param2, String) to every remaining participant except the
// caller of 21/36. Sending it to the caller could toggle its host state.
func (s *registry) notifyOwnershipLocked(g *gathering, caller connection, gid, owner uint32) {
	args := putU32(nil, caller.pid)
	args = putU32(args, ownershipChanged)
	args = putU32(args, gid)
	args = putU32(args, owner)
	args = putString(args, "")
	for mc := range g.members {
		live, ok := s.live[mc]
		if mc == caller || !ok || len(s.calls) >= maxProbes {
			continue
		}
		s.calls = append(s.calls, quazal.ServerCall{Peer: live.peer, Protocol: 14, Method: 1, Args: args})
		s.log.Info("wcc ownership notification queued", "gid", gid, "to", mc.pid, "owner", owner)
	}
}

// serverCalls is drained by the secure transport with probe instructions.
func (s *registry) serverCalls() []quazal.ServerCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.calls
	s.calls = nil
	return out
}

// extension handles MatchMakingProtocolExt (50) methods 1-4. Method 5,
// GetGatheringRelations, is declared by this build but not yet mapped.
func (s *registry) extension(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
	r := reader{b: request.Args}
	switch request.Method {
	case 1: // EndParticipation(u32, String) -> bool
		id := r.u32()
		r.str(512)
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return putBool(nil, s.leaveLocked(connectionOf(peer, pid), id)), nil
	case 2, 3: // GetParticipants / GetDetailedParticipants(u32, bool onlyActive)
		id := r.u32()
		b := r.take(1)
		if !r.done() || b[0] > 1 {
			return nil, quazal.ErrRMC
		}
		method := uint32(13)
		if request.Method == 3 {
			method = 15
		}
		return s.matchmaking(peer, pid, quazal.Request{Protocol: 21, Method: method, Args: putU32(nil, id)})
	case 4: // GetParticipantsURLs(list<u32>) -> list<GatheringURLs>
		ids := r.ids(32)
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		c := connectionOf(peer, pid)
		var out []byte
		count := uint32(0)
		seen := map[uint32]bool{}
		for _, id := range ids {
			g := s.gatherings[id]
			if seen[id] || g == nil || !g.visible() && g.owner != c && !g.hasPID(pid) {
				continue
			}
			seen[id] = true
			count++
			urls := s.urlsLocked(g)
			out = putU32(out, id)
			out = putU32(out, uint32(len(urls)))
			for _, u := range urls {
				out = putString(out, u)
			}
		}
		return append(putU32(nil, count), out...), nil
	}
	return nil, errUnsupported
}

// reachableLocked reports whether a connection may be introduced to target:
// they meet in a gathering, or target owns or plays at a listed gathering.
// The retail joiner resolves the host (11/3) before 21/11 Participate, and
// with three or more players it also asks (3/1) for probes from the players
// already seated, to whom it must connect as well.
func (s *registry) reachableLocked(a connection, target uint32) bool {
	if s.sharedLocked(a, target) {
		return true
	}
	for _, g := range s.gatherings {
		if g.visible() && (g.owner.pid == target || g.hasPID(target)) {
			return true
		}
	}
	return false
}

// sharedLocked reports whether two connections meet in a gathering.
func (s *registry) sharedLocked(a connection, pid uint32) bool {
	for _, g := range s.gatherings {
		aIn := g.owner == a || func() bool { _, ok := g.members[a]; return ok }()
		bIn := g.owner.pid == pid || g.hasPID(pid)
		if aIn && bIn {
			return true
		}
	}
	return false
}

func urlField(url, key string) string {
	_, body, ok := strings.Cut(url, ":/")
	if !ok {
		return ""
	}
	for _, part := range strings.Split(body, ";") {
		if k, v, ok := strings.Cut(part, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func urlPID(url string) (uint32, bool) {
	n, err := strconv.ParseUint(urlField(url, "PID"), 10, 32)
	return uint32(n), err == nil
}

// probeTargets authorizes NAT probes (3/1) toward live connections the
// requester shares a gathering with, or toward the host of a listed gathering.
func (s *registry) probeTargets(peer prudp.Peer, pid uint32, urls []string) ([]prudp.Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	c := connectionOf(peer, pid)
	var out []prudp.Peer
	seen := map[connection]bool{}
	for _, url := range urls {
		target, ok := urlPID(url)
		if !ok || target == pid || !s.reachableLocked(c, target) {
			continue
		}
		for lc, live := range s.live {
			if lc.pid == target && !seen[lc] {
				seen[lc] = true
				out = append(out, live.peer)
			}
		}
	}
	return out, nil
}

// connectionURLs answers 11/3 (cid, target PID) -> bool, list<StationURL>.
//
// The retail joiner never calls 3/1 itself; it resolves the host here and
// connects straight away. So the server also asks the host (3/2 InitiateProbe,
// handled by the client dispatcher 0x00336508) to send packets toward the
// joiner. That opens the host's stateful firewall/NAT to the joiner, which a
// direct connection otherwise needs inbound rules for.
func (s *registry) connectionURLs(peer prudp.Peer, pid, _, target uint32) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	requester := connectionOf(peer, pid)
	if !s.reachableLocked(requester, target) {
		return nil, nil
	}
	for lc, live := range s.live {
		if lc.pid == target && live.url != "" {
			s.queueProbeLocked(lc, requester)
			return []string{live.url}, nil
		}
	}
	return nil, nil
}

const (
	probeInterval = 2 * time.Second
	maxProbes     = 64
)

// queueProbeLocked asks prober to probe the registered URL of toward.
func (s *registry) queueProbeLocked(prober, toward connection) {
	target, ok := s.live[toward]
	source, live := s.live[prober]
	if !ok || !live || target.url == "" || prober == toward {
		return
	}
	cid := urlField(target.url, "CID")
	if cid == "" {
		return
	}
	key := [2]connection{prober, toward}
	now := s.now()
	if last, seen := s.lastProbe[key]; seen && now.Sub(last) < probeInterval {
		return
	}
	for k, t := range s.lastProbe {
		if now.Sub(t) >= time.Minute {
			delete(s.lastProbe, k)
		}
	}
	if len(s.probes) >= maxProbes {
		return
	}
	s.lastProbe[key] = now
	s.probes = append(s.probes, quazal.ProbeInstruction{Peer: source.peer, URL: target.url + ";RVCID=" + cid})
	s.log.Info("wcc probe queued", "prober", prober.pid, "toward", toward.pid)
}

// probeInstructions is drained by the secure transport, including on idle ticks.
func (s *registry) probeInstructions() []quazal.ProbeInstruction {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.probes
	s.probes = nil
	return out
}

type snapshot struct {
	Players    int            `json:"players"`
	Gatherings map[string]int `json:"gatherings"`
}

func (s *registry) snapshot() snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	out := snapshot{Players: len(s.live), Gatherings: map[string]int{}}
	for _, g := range s.gatherings {
		out.Gatherings[g.data.Type]++
	}
	return out
}
