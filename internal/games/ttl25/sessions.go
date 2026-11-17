package ttl25

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

type sessionOwner struct {
	port uint16
	peer uint64
	pid  uint32
}

func ownerOf(peer prudp.Peer, pid uint32) sessionOwner {
	return sessionOwner{peer.LocalPort, peer.ID, pid}
}

type sessionRecord struct {
	owner   sessionOwner
	data    quazal.AnyData
	fields  map[string]string
	input   []byte
	members map[sessionOwner]string
	url     string
	state   uint32
}
type sessionRegistry struct {
	mu       sync.Mutex
	next     uint32
	rooms    map[uint32]sessionRecord
	runtime  string
	name     func(uint32) string
	invites  map[uint32]map[uint32]string
	urls     map[sessionOwner]string
	peers    map[sessionOwner]prudp.Peer
	finished map[uint32]finishedRound
	now      func() time.Time
	relay    *nativeRelay
}

type finishedRound struct {
	room    sessionRecord
	expires time.Time
}

func newSessions() *sessionRegistry {
	return &sessionRegistry{next: 10000, rooms: make(map[uint32]sessionRecord), runtime: rand.Text(), name: func(uint32) string { return "" }, invites: map[uint32]map[uint32]string{}, urls: map[sessionOwner]string{}, peers: map[sessionOwner]prudp.Peer{}, finished: map[uint32]finishedRound{}, now: time.Now}
}

var sessionNumbers = []string{"levelIndex", "gameType", "timeLimit", "scoreLimit", "tagLimit", "bestOfSeries", "maxPlayers", "reservedPlayers", "totalConnectedPlayers"}
var sessionBools = []string{"isCustomQuery", "isRanked", "isPrivate", "playersRespawn", "friendlyFire", "apprehension", "arcadeDamage", "selfDamage", "alternatingTeams"}

func readNewSession(args []byte) (quazal.AnyData, map[string]string, error) {
	r := rankingReader{b: args}
	data := quazal.AnyData{Type: r.str()}
	n := r.u32()
	if (data.Type != "TTLSession" && data.Type != "TTLRound") || n > 2048 {
		return data, nil, quazal.ErrRMC
	}
	data.Payload = bytes.Clone(r.take(int(n)))
	if !r.done() {
		return data, nil, quazal.ErrRMC
	}
	r = rankingReader{b: data.Payload}
	body := r.u32()
	id, owner, host := r.u32(), r.u32(), r.u32()
	limits := r.u32()
	min, max := limits&0xffff, limits>>16
	for range 4 {
		r.u32()
	} // participation policy, argument, flags, state
	description := r.str()
	fields := map[string]string{}
	if data.Type == "TTLSession" {
		fields["levelName"] = r.str()
		for _, name := range sessionNumbers {
			fields[name] = strconv.FormatUint(uint64(r.u32()), 10)
		}
		for _, name := range sessionBools {
			b := r.take(1)
			if b == nil || b[0] > 1 {
				return data, nil, quazal.ErrRMC
			}
			fields[name] = strconv.Itoa(int(b[0]))
		}
	} else {
		fields["isPrivate"] = "0"
		if r.boolean() {
			fields["isPrivate"] = "1"
		}
		for _, name := range []string{"levelIndex", "gameType", "timeLimit"} {
			fields[name] = strconv.FormatUint(uint64(r.u32()), 10)
		}
		fields["levelName"] = r.str()
		fields["ranking"] = strconv.FormatUint(uint64(r.u32()), 10)
		capacity := max
		if capacity == 0 {
			capacity = 32
		}
		fields["maxPlayers"] = strconv.FormatUint(uint64(capacity), 10)
		fields["reservedPlayers"] = "0"
		fields["totalConnectedPlayers"] = "0"
	}
	if !r.done() || uint64(body) != uint64(len(data.Payload)-4) || id != 0 || owner != 0 || host != 0 || min > max || max > 32 {
		return data, nil, quazal.ErrRMC
	}
	if data.Type == "TTLSession" && (max == 0 || !acceptsText(description) || fields["levelName"] == "") {
		return data, nil, quazal.ErrRMC
	}
	players, _ := strconv.ParseUint(fields["maxPlayers"], 10, 32)
	reserved, _ := strconv.ParseUint(fields["reservedPlayers"], 10, 32)
	connected, _ := strconv.ParseUint(fields["totalConnectedPlayers"], 10, 32)
	if players == 0 || players > 32 || reserved > players || connected > players {
		return data, nil, quazal.ErrRMC
	}
	return data, fields, nil
}

func (s *sessionRegistry) register(peer prudp.Peer, pid uint32, args []byte) ([]byte, error) {
	data, fields, err := readNewSession(args)
	if err != nil {
		return nil, err
	}
	if pid < 1000 {
		return nil, errors.New("session requires named principal")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	owner := ownerOf(peer, pid)
	for id, room := range s.rooms {
		if room.owner == owner && room.data.Type == data.Type {
			if bytes.Equal(room.input, args) {
				return rankingU32(nil, id), nil
			}
			return nil, errors.New("connection already hosts a session")
		}
	}
	if len(s.rooms) >= 1024 {
		return nil, errors.New("session capacity reached")
	}
	id := s.next
	s.next++
	binary.LittleEndian.PutUint32(data.Payload[4:], id)
	binary.LittleEndian.PutUint32(data.Payload[8:], pid)
	binary.LittleEndian.PutUint32(data.Payload[12:], pid)
	s.rooms[id] = sessionRecord{owner: owner, data: data, fields: fields, input: bytes.Clone(args), members: map[sessionOwner]string{}}
	return rankingU32(nil, id), nil
}

func (s *sessionRegistry) unregister(peer prudp.Peer, pid uint32, args []byte) ([]byte, error) {
	r := rankingReader{b: args}
	id := r.u32()
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	room, ok := s.rooms[id]
	if !ok || room.owner != ownerOf(peer, pid) {
		return []byte{0}, nil
	}
	s.finishRound(id, room)
	s.relay.removeParticipant(room.owner, id)
	delete(s.rooms, id)
	delete(s.invites, id)
	return []byte{1}, nil
}
func (s *sessionRegistry) disconnect(peer prudp.Peer, pid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.urls, ownerOf(peer, pid))
	delete(s.peers, ownerOf(peer, pid))
	s.relay.removeParticipant(ownerOf(peer, pid), 0)
	for id, round := range s.finished {
		if round.room.owner == ownerOf(peer, pid) {
			delete(s.finished, id)
		} else {
			delete(round.room.members, ownerOf(peer, pid))
		}
	}
	for id, room := range s.rooms {
		if room.owner == ownerOf(peer, pid) {
			delete(s.rooms, id)
			delete(s.invites, id)
		} else {
			delete(room.members, ownerOf(peer, pid))
			s.updateCount(&room)
			s.rooms[id] = room
		}
	}
}

// The retail query builder emits equality predicates joined by AND. This
// evaluates typed room fields directly; no incoming text is executed as SQL.
func parseSessionQuery(query string) (map[string]string, error) {
	const prefix = "WHERE Gatherings.type = 'TTLSession'"
	if !strings.HasPrefix(query, prefix) {
		return nil, errors.New("unsupported session query")
	}
	filters := make(map[string]string)
	tail := strings.TrimPrefix(query, prefix)
	if tail == "" {
		return filters, nil
	}
	if !strings.HasPrefix(tail, " AND ") {
		return nil, quazal.ErrRMC
	}
	for _, term := range strings.Split(tail[5:], " AND ") {
		parts := strings.SplitN(term, " = ", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "TTLSessions.") {
			return nil, quazal.ErrRMC
		}
		name := strings.TrimPrefix(parts[0], "TTLSessions.")
		value := parts[1]
		if _, duplicate := filters[name]; duplicate {
			return nil, quazal.ErrRMC
		}
		if name == "levelName" {
			if len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' || strings.Contains(value[1:len(value)-1], "'") {
				return nil, quazal.ErrRMC
			}
			filters[name] = value[1 : len(value)-1]
			continue
		}
		known := false
		isBool := false
		for _, field := range sessionNumbers {
			known = known || name == field
		}
		for _, field := range sessionBools {
			if name == field {
				known = true
				isBool = true
			}
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if !known || err != nil || isBool && n > 1 {
			return nil, quazal.ErrRMC
		}
		filters[name] = strconv.FormatUint(n, 10)
	}
	return filters, nil
}

func (s *sessionRegistry) search(args []byte) ([]byte, error) {
	r := rankingReader{b: args}
	query := r.strLimit(4096)
	offset, count := r.u32(), r.u32()
	if !r.done() || count > 100 {
		return nil, quazal.ErrRMC
	}
	filters, err := parseSessionQuery(query)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]uint32, 0, len(s.rooms))
	for id, room := range s.rooms {
		if room.data.Type != "TTLSession" {
			continue
		}
		match := true
		for field, value := range filters {
			if room.fields[field] != value {
				match = false
				break
			}
		}
		if match {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	start := min(uint64(offset), uint64(len(ids)))
	end := min(start+uint64(count), uint64(len(ids)))
	b := rankingU32(nil, uint32(end-start))
	for _, id := range ids[start:end] {
		b = rankingAny(b, s.rooms[id].data)
	}
	return b, nil
}
