package snowblind

import (
	"bytes"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal/peerrelay"
)

type owner struct {
	port uint16
	peer uint64
	pid  uint32
}

func identity(p prudp.Peer, pid uint32) owner { return owner{p.LocalPort, p.ID, pid} }

type registration struct {
	peer prudp.Peer
	url  string
}
type room struct {
	owner       owner
	data        gathering
	input       []byte
	members     map[owner]string
	url         string
	capacity    uint16
	competition uint32
	parent      uint32
	report      *reportContext
}
type sessions struct {
	mu        sync.Mutex
	next      uint32
	rooms     map[uint32]*room
	peers     map[owner]registration
	name      func(uint32) string
	log       *slog.Logger
	stats     *statsStore
	reporting map[owner]*reportContext
	retired   map[uint32]*reportContext
	retiredAt map[uint32]time.Time
	relay     *peerrelay.Relay
	relayMode string
}

func newSessions(log *slog.Logger, name func(uint32) string) *sessions {
	return &sessions{next: 10000, rooms: map[uint32]*room{}, peers: map[owner]registration{}, name: name, log: log, reporting: map[owner]*reportContext{}, retired: map[uint32]*reportContext{}, retiredAt: map[uint32]time.Time{}}
}
func (s *sessions) registered(p prudp.Peer, pid uint32, url string) {
	if pid < 1000 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers[identity(p, pid)] = registration{p, quazal.WithRVCID(url)}
}

// Only ticket-proven secure registrations reach this registry. Presence follows
// exact connection generations, so a late disconnect cannot retire a new login.
func (s *sessions) online(pid uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for who := range s.peers {
		if who.pid == pid {
			return true
		}
	}
	return false
}

func (s *sessions) disconnect(p prudp.Peer, pid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	who := identity(p, pid)
	delete(s.peers, who)
	delete(s.reporting, who)
	s.relay.RemoveParticipant(relayOwner(who), 0)
	for id, r := range s.rooms {
		if r.owner == who {
			s.remove(id)
		} else {
			delete(r.members, who)
			r.updateCount()
		}
	}
}
func (r *room) updateCount() {
	if r.data.Kind == "BO3Match" {
		r.data.Players = byte(len(r.members))
	}
}
func boolean(v bool) ([]byte, error) {
	var w quazal.Writer
	w.Bool(v)
	return w.Bytes()
}
func (s *sessions) handle(p prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
	if pid < 1000 {
		return nil, errors.New("Snowblind requires a named account")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	who := identity(p, pid)
	if _, ok := s.peers[who]; !ok {
		return nil, errors.New("Snowblind connection is not registered")
	}
	if s.stats != nil {
		if request.Protocol == 21 && request.Method == 31 {
			return s.summary(who, request.Args)
		}
		if request.Protocol == 101 && request.Method == 4 {
			return s.personal(who, request.Args)
		}
	}
	if request.Protocol == 101 && request.Method == 3 {
		return s.search(who, request.Args)
	}
	if request.Protocol == 21 && (request.Method == 1 || request.Method == 4) {
		return s.put(who, request)
	}
	if request.Protocol == 50 {
		return s.extension(who, request)
	}
	if request.Protocol == 26 && request.Method == 2 {
		r := quazal.NewReader(request.Args)
		ranking, gid := r.U32(), r.U32()
		if !r.Done() {
			return nil, quazal.ErrRMC
		}
		room, ok := s.rooms[gid]
		if !ok || room.owner != who || room.data.Kind != "Gathering" || ranking != 1 {
			return boolean(false)
		}
		room.competition = ranking
		if room.report != nil {
			room.report.ranked = true
		}
		return boolean(true)
	}
	if request.Protocol != 21 && !(request.Protocol == 101 && request.Method == 5) {
		return nil, errors.New("unsupported Snowblind matchmaking protocol")
	}
	r := quazal.NewReader(request.Args)
	id := r.U32()
	var text string
	var state uint32
	if request.Protocol == 101 || request.Method == 11 || request.Method == 12 || request.Method == 26 || request.Method == 27 {
		text = r.String(512)
	}
	if request.Method == 30 && request.Protocol == 21 {
		state = r.U32()
	}
	switch request.Protocol {
	case 21:
		switch request.Method {
		case 2, 11, 12, 13, 15, 16, 21, 26, 27, 28, 29, 30:
		default:
			return nil, errors.New("unsupported Snowblind matchmaking method")
		}
	}
	if !r.Done() {
		return nil, quazal.ErrRMC
	}
	room, exists := s.rooms[id]
	if request.Protocol == 101 {
		if ctx := s.reporting[who]; ctx != nil && ctx.gid == id {
			if !exists || room.report != ctx {
				ctx.finished[who] = true
				return boolean(true)
			}
			b, err := s.leave(who, id, room)
			if err == nil && len(b) == 1 && b[0] == 1 {
				ctx.finished[who] = true
			}
			return b, err
		}
		if !exists && s.stats != nil {
			ctx := s.retired[id]
			if ctx != nil && ctx.participants[who] && time.Since(s.retiredAt[id]) <= 15*time.Minute {
				ctx.finished[who] = true
				return boolean(true)
			}
		}
		return s.leave(who, id, room)
	}
	var w quazal.Writer
	switch request.Method {
	case 21:
		w.Bool(exists)
		if exists {
			b, err := room.data.encode()
			if err != nil {
				return nil, err
			}
			w.Raw(b)
		} else {
			w.Any(quazal.AnyData{})
		}
		return w.Bytes()
	case 13, 15:
		return s.participants(room, request.Method == 15)
	case 16:
		urls := s.urls(who, id, room)
		w.U32(uint32(len(urls)))
		for _, url := range urls {
			w.String(url)
		}
		return w.Bytes()
	case 28:
		url := ""
		if exists {
			if candidates := s.offeredURLs(who, id, room.owner, room.url); room.url != "" && len(candidates) > 0 {
				url = candidates[0]
			}
		}
		w.Bool(url != "")
		w.String(url)
		return w.Bytes()
	case 29:
		w.Bool(exists)
		if exists {
			w.U32(room.data.State)
		} else {
			w.U32(0)
		}
		return w.Bytes()
	}
	if !exists {
		return boolean(false)
	}
	switch request.Method {
	case 2:
		if room.owner != who {
			return boolean(false)
		}
		s.remove(id)
		return boolean(true)
	case 11:
		if _, ok := room.members[who]; ok {
			return boolean(true)
		}
		if len(room.members) >= int(room.capacity) || room.data.State >= 6 {
			return boolean(false)
		}
		for member := range room.members {
			if member.pid == pid {
				return boolean(false)
			}
		}
		room.members[who] = text
		if room.report != nil {
			room.report.participants[who] = true
		}
		room.updateCount()
		s.log.Info("matchmaking participant joined", "gid", id, "pid", pid, "participants", len(room.members))
		return boolean(true)
	case 12:
		return s.leave(who, id, room)
	case 26, 27:
		if room.owner != who {
			return boolean(false)
		}
		url, err := s.validateURL(who, text)
		if err != nil {
			return boolean(false)
		}
		room.url = url
		room.data.HostPID = pid
		s.log.Info("matchmaking host URL published", "gid", id, "pid", pid)
		return boolean(true)
	case 30:
		if room.owner != who {
			return boolean(false)
		}
		if err := s.reportState(room, state); err != nil {
			return nil, err
		}
		room.data.State = state
		return boolean(true)
	}
	return nil, quazal.ErrRMC
}
func (s *sessions) put(who owner, request quazal.Request) ([]byte, error) {
	g, err := decodeGathering(request.Args)
	if err != nil {
		return nil, err
	}
	if (g.OwnerPID != 0 && g.OwnerPID != who.pid) || (g.HostPID != 0 && g.HostPID != who.pid) {
		return nil, errors.New("foreign Snowblind gathering identity")
	}
	if request.Method == 4 {
		room, ok := s.rooms[g.ID]
		if !ok || room.owner != who || room.data.Kind != g.Kind {
			return boolean(false)
		}
		if g.MaxParticipants != 0 && int(g.MaxParticipants) < len(room.members) {
			return boolean(false)
		}
		g.OwnerPID = who.pid
		g.HostPID = who.pid
		if err := s.reportState(room, g.State); err != nil {
			return nil, err
		}
		room.data = g
		if g.Kind == "BO3Match" {
			room.capacity = g.MaxParticipants
			room.updateCount()
		}
		return boolean(true)
	}
	if g.ID != 0 || g.OwnerPID != 0 || g.HostPID != 0 {
		return nil, quazal.ErrRMC
	}
	capacity := g.MaxParticipants
	var parent uint32
	if g.Kind == "Gathering" {
		found := false
		for gid, room := range s.rooms {
			if room.owner == who && room.data.Kind == "BO3Match" {
				parent = gid
				capacity = room.capacity
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("Snowblind round requires an owned lobby")
		}
	}
	for id, room := range s.rooms {
		if room.owner.pid == who.pid && room.data.Kind == g.Kind {
			if room.owner == who && bytes.Equal(room.input, request.Args) {
				var w quazal.Writer
				w.U32(id)
				return w.Bytes()
			}
			return nil, errors.New("account already hosts this Snowblind gathering kind")
		}
	}
	if len(s.rooms) >= 1024 || s.next == 0 {
		return nil, errors.New("Snowblind gathering capacity reached")
	}
	id := s.next
	s.next++
	g.ID = id
	g.OwnerPID = who.pid
	g.HostPID = who.pid
	created := &room{owner: who, data: g, input: bytes.Clone(request.Args), members: map[owner]string{}, capacity: capacity, parent: parent}
	if s.stats != nil && g.Kind == "Gathering" {
		ctx, err := newReportContext(id, s.rooms[parent].data)
		if err != nil {
			return nil, err
		}
		created.report = ctx
	}
	s.rooms[id] = created
	s.log.Info("matchmaking gathering registered", "gid", id, "pid", who.pid, "kind", g.Kind, "mode", g.Mode, "map", g.Map, "capacity", capacity)
	var w quazal.Writer
	w.U32(id)
	return w.Bytes()
}
func (s *sessions) leave(who owner, id uint32, room *room) ([]byte, error) {
	if room == nil {
		return boolean(false)
	}
	if room.owner == who {
		s.remove(id)
		return boolean(true)
	}
	if _, ok := room.members[who]; !ok {
		return boolean(false)
	}
	delete(room.members, who)
	s.relay.RemoveParticipant(relayOwner(who), id)
	room.updateCount()
	return boolean(true)
}
func (s *sessions) participants(room *room, detailed bool) ([]byte, error) {
	var members []owner
	if room != nil {
		for p := range room.members {
			members = append(members, p)
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].pid < members[j].pid })
	var w quazal.Writer
	w.U32(uint32(len(members)))
	for _, p := range members {
		w.U32(p.pid)
		if detailed {
			w.String(s.name(p.pid))
			w.String(room.members[p])
		}
	}
	return w.Bytes()
}
func (s *sessions) urls(caller owner, id uint32, room *room) []string {
	if room == nil {
		return nil
	}
	var members []owner
	for p := range room.members {
		if p != room.owner {
			members = append(members, p)
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].pid < members[j].pid })
	var urls []string
	if room.url != "" {
		urls = append(urls, s.offeredURLs(caller, id, room.owner, room.url)...)
	}
	for _, p := range members {
		if reg, ok := s.peers[p]; ok {
			urls = append(urls, s.offeredURLs(caller, id, p, reg.url)...)
		}
	}
	return urls
}
func (s *sessions) search(caller owner, args []byte) ([]byte, error) {
	r := quazal.NewReader(args)
	query := r.String(2048)
	if !r.Done() {
		return nil, quazal.ErrRMC
	}
	predicate, err := parseQuery(query)
	if err != nil {
		return nil, err
	}
	var ids []uint32
	for id, room := range s.rooms {
		if room.url != "" && room.data.State < 6 && predicate(room.data) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	ids = ids[:min(len(ids), 100)]
	var w quazal.Writer
	w.U32(uint32(len(ids)))
	for _, id := range ids {
		room := s.rooms[id]
		b, err := room.data.encode()
		if err != nil {
			return nil, err
		}
		w.Raw(b)
		// 0x15fc38/0x15fcf0: each result is Any Gathering + L<StationURL>.
		urls := s.urls(caller, id, room)
		w.U32(uint32(len(urls)))
		for _, url := range urls {
			w.String(url)
		}
	}
	return w.Bytes()
}
func (s *sessions) extension(who owner, request quazal.Request) ([]byte, error) {
	r := quazal.NewReader(request.Args)
	id := r.U32()
	if request.Method == 4 {
		if id > 32 {
			return nil, quazal.ErrRMC
		}
		ids := make([]uint32, id)
		for i := range ids {
			ids[i] = r.U32()
		}
		if !r.Done() {
			return nil, quazal.ErrRMC
		}
		seen := map[uint32]bool{}
		selected := []uint32{}
		for _, gid := range ids {
			if !seen[gid] && s.rooms[gid] != nil {
				selected = append(selected, gid)
				seen[gid] = true
			}
		}
		var w quazal.Writer
		w.U32(uint32(len(selected)))
		for _, gid := range selected {
			w.U32(gid)
			urls := s.urls(who, gid, s.rooms[gid])
			w.U32(uint32(len(urls)))
			for _, url := range urls {
				w.String(url)
			}
		}
		return w.Bytes()
	}
	switch request.Method {
	case 1:
		r.String(512)
		if !r.Done() {
			return nil, quazal.ErrRMC
		}
		return s.leave(who, id, s.rooms[id])
	case 2, 3:
		r.Bool()
		if !r.Done() {
			return nil, quazal.ErrRMC
		}
		return s.participants(s.rooms[id], request.Method == 3)
	default:
		return nil, errors.New("unsupported Snowblind matchmaking extension")
	}
}
func (s *sessions) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rooms)
}

// Removing a staging lobby also retires rounds created from that lobby.
func (s *sessions) remove(id uint32) {
	s.removeRoutes(id, s.rooms[id])
	s.retire(s.rooms[id])
	delete(s.rooms, id)
	for child, room := range s.rooms {
		if room.parent == id {
			s.removeRoutes(child, room)
			s.retire(room)
			delete(s.rooms, child)
		}
	}
}

func (s *sessions) retire(room *room) {
	if room == nil || room.report == nil {
		return
	}
	for gid, at := range s.retiredAt {
		if time.Since(at) > 15*time.Minute {
			delete(s.retired, gid)
			delete(s.retiredAt, gid)
		}
	}
	if len(s.retired) >= 1024 {
		var oldest uint32
		for gid, at := range s.retiredAt {
			if oldest == 0 || at.Before(s.retiredAt[oldest]) {
				oldest = gid
			}
		}
		delete(s.retired, oldest)
		delete(s.retiredAt, oldest)
	}
	s.retired[room.data.ID], s.retiredAt[room.data.ID] = room.report, time.Now()
}

func (s *sessions) reportState(room *room, state uint32) error {
	if room.report == nil {
		return nil
	}
	if state == 2 && room.report.closed {
		lobby := gathering{Mode: room.report.mode, Map: room.report.mapID}
		if parent := s.rooms[room.parent]; parent != nil {
			lobby = parent.data
		}
		ctx, err := newReportContext(room.data.ID, lobby)
		if err != nil {
			return err
		}
		for member := range room.members {
			ctx.participants[member] = true
		}
		ctx.ranked = room.competition == 1
		s.retire(room)
		room.report = ctx
	}
	if state == 6 {
		room.report.closed = true
	}
	return nil
}
