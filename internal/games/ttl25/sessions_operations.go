package ttl25

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
	"strconv"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func (r *rankingReader) ids(limit uint32) []uint32 {
	n := r.u32()
	if n > limit {
		r.err = quazal.ErrRMC
		return nil
	}
	ids := make([]uint32, n)
	for i := range ids {
		ids[i] = r.u32()
	}
	return ids
}
func (s *sessionRegistry) listLocked(ids []uint32, offset, count uint32) []byte {
	visible := ids[:0]
	for _, id := range ids {
		if room, exists := s.rooms[id]; exists && room.migrationUntil.IsZero() {
			visible = append(visible, id)
		}
	}
	ids = visible
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	start := min(uint64(offset), uint64(len(ids)))
	end := min(start+uint64(count), uint64(len(ids)))
	b := rankingU32(nil, uint32(end-start))
	for _, id := range ids[start:end] {
		b = rankingAny(b, s.rooms[id].data)
	}
	return b
}
func (s *sessionRegistry) operation(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
	switch request.Method {
	case 11, 12, 13, 15, 16, 26, 27, 28, 29, 30:
		return s.lobby(peer, pid, request)
	}
	if request.Method == 4 {
		return s.update(peer, pid, request.Args)
	}
	r := rankingReader{b: request.Args}
	owner := ownerOf(peer, pid)
	var gid, target, offset uint32
	count := uint32(100)
	var ids []uint32
	var message string
	switch request.Method {
	case 5, 8, 14:
		gid = r.u32()
		ids = r.ids(32)
		message = r.strLimit(512)
	case 6, 7:
		gid = r.u32()
		message = r.strLimit(512)
	case 9, 21:
		gid = r.u32()
	case 10:
	case 20, 23:
		ids = r.ids(256)
	case 22:
		target = r.u32()
		offset, count = r.u32(), r.u32()
	case 24:
		offset, count = r.u32(), r.u32()
	default:
		return nil, errors.New("unsupported gathering operation")
	}
	if !r.done() || count > 100 {
		return nil, quazal.ErrRMC
	}
	if request.Method == 6 {
		s.mu.Lock()
		_, invited := s.invites[gid][pid]
		s.mu.Unlock()
		if !invited {
			return boolReply(false), nil
		}
		return s.lobby(peer, pid, quazal.Request{Method: 11, Args: rankingString(rankingU32(nil, gid), message)})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneMigrations()
	room, exists := s.rooms[gid]
	switch request.Method {
	case 5, 8, 14:
		if !exists || room.owner != owner {
			return boolReply(false), nil
		}
		if request.Method == 14 {
			add := map[sessionOwner]bool{}
			for _, id := range ids {
				found := false
				for who := range s.urls {
					if who.pid == id {
						add[who] = true
						found = true
						break
					}
				}
				if !found {
					return boolReply(false), nil
				}
			}
			limit := 0
			for _, b := range []byte(room.fields["maxPlayers"]) {
				limit = limit*10 + int(b-'0')
			}
			newCount := len(room.members)
			for who := range add {
				if _, present := room.members[who]; !present {
					newCount++
				}
			}
			if newCount > limit {
				return boolReply(false), nil
			}
			for who := range add {
				room.members[who] = message
			}
			s.updateCount(&room)
			s.rooms[gid] = room
			return boolReply(true), nil
		}
		if s.invites[gid] == nil {
			s.invites[gid] = map[uint32]string{}
		}
		for _, id := range ids {
			if id < 1000 || id == pid || s.name(id) == "" {
				return boolReply(false), nil
			}
		}
		for _, id := range ids {
			if request.Method == 5 {
				s.invites[gid][id] = message
			} else {
				delete(s.invites[gid], id)
			}
		}
		return boolReply(true), nil
	case 7:
		_, ok := s.invites[gid][pid]
		delete(s.invites[gid], pid)
		return boolReply(ok), nil
	case 9, 10:
		type invite struct {
			gid, to uint32
			message string
		}
		v := []invite{}
		for id, targets := range s.invites {
			if request.Method == 9 && (id != gid || s.rooms[id].owner != owner) {
				continue
			}
			for to, text := range targets {
				if request.Method == 9 || to == pid {
					v = append(v, invite{id, to, text})
				}
			}
		}
		sort.Slice(v, func(i, j int) bool {
			if v[i].gid == v[j].gid {
				return v[i].to < v[j].to
			}
			return v[i].gid < v[j].gid
		})
		b := rankingU32(nil, uint32(len(v)))
		for _, inv := range v {
			b = rankingU32(b, inv.gid)
			b = rankingU32(b, inv.to)
			b = rankingString(b, inv.message)
		}
		return b, nil
	case 21:
		_, member := room.members[owner]
		if !exists || !room.migrationUntil.IsZero() && !member {
			return append(boolReply(false), rankingAny(nil, quazal.AnyData{})...), nil
		}
		return rankingAny(boolReply(true), room.data), nil
	case 20, 22, 23, 24:
		selected := []uint32{}
		wanted := map[uint32]bool{}
		for _, id := range ids {
			wanted[id] = true
		}
		for id, room := range s.rooms {
			match := false
			switch request.Method {
			case 20:
				match = wanted[id]
			case 22:
				match = room.owner.pid == target
			case 23:
				for _, member := range uniqueMembers(room) {
					if wanted[member] {
						match = true
						break
					}
				}
			case 24:
				_, match = s.invites[id][pid]
			}
			if match {
				selected = append(selected, id)
			}
		}
		return s.listLocked(selected, offset, count), nil
	}
	return nil, errors.New("unsupported gathering operation")
}

func (s *sessionRegistry) update(peer prudp.Peer, pid uint32, args []byte) ([]byte, error) {
	r := rankingReader{b: args}
	kind := r.str()
	n := r.u32()
	if n > 2048 {
		return nil, quazal.ErrRMC
	}
	data := r.take(int(n))
	if !r.done() || len(data) < 16 {
		return nil, quazal.ErrRMC
	}
	id := binary.LittleEndian.Uint32(data[4:])
	clean := bytes.Clone(data)
	for i := 4; i < 16; i++ {
		clean[i] = 0
	}
	parsed, fields, err := readNewSession(rankingAny(nil, quazal.AnyData{Type: kind, Payload: clean}))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneMigrations()
	room, ok := s.rooms[id]
	if !ok || room.owner != ownerOf(peer, pid) || room.data.Type != kind || !room.migrationUntil.IsZero() {
		s.logger().Warn("matchmaking room update rejected", "pid", pid, "peer", peer.ID,
			"local_port", peer.LocalPort, "gid", id, "reason", "room missing or owner/type mismatch")
		return boolReply(false), nil
	}
	limit := int(binary.LittleEndian.Uint16(parsed.Payload[18:]))
	if limit == 0 && kind == "TTLRound" {
		limit = 32
	}
	if len(room.members) > limit {
		return boolReply(false), nil
	}
	room.data.Payload = bytes.Clone(data)
	// Authority comes from the exact current owner connection. The stock
	// successor can retain the departed owner's PID in its cached record;
	// neither supplied identity is ever used to grant ownership.
	binary.LittleEndian.PutUint32(room.data.Payload[8:], pid)
	binary.LittleEndian.PutUint32(room.data.Payload[12:], pid)
	room.input = rankingAny(nil, quazal.AnyData{Type: kind, Payload: clean})
	room.fields = fields
	room.state = binary.LittleEndian.Uint32(data[32:])
	// The host observes native session membership. Backend participation can
	// outlive a failed peer join, so its lower reported count is useful. A
	// cached pre-departure count must not resurrect participants who left.
	if kind == "TTLSession" {
		known := len(uniqueMembers(room))
		if !roomMember(room, pid) {
			known++
		}
		count, _ := strconv.Atoi(room.fields["totalConnectedPlayers"])
		if count > known {
			room.fields["totalConnectedPlayers"] = strconv.Itoa(known)
			binary.LittleEndian.PutUint32(room.data.Payload[len(room.data.Payload)-13:], uint32(known))
		}
	}
	s.rooms[id] = room
	s.logger().Debug("matchmaking room updated", "pid", pid, "peer", peer.ID, "local_port", peer.LocalPort,
		"room", s.roomDiagnostic(id, room))
	return boolReply(true), nil
}

func (c *community) findRooms(request quazal.Request) ([]byte, error) {
	r := rankingReader{b: request.Args}
	level := ""
	ranking, diff := uint32(0), uint32(0)
	if request.Method == 1 {
		level = r.str()
	} else {
		ranking, diff = r.u32(), r.u32()
	}
	offset, count := r.u32(), r.u32()
	if !r.done() || count > 100 {
		return nil, quazal.ErrRMC
	}
	c.sessions.mu.Lock()
	defer c.sessions.mu.Unlock()
	c.sessions.pruneMigrations()
	ids := []uint32{}
	for id, room := range c.sessions.rooms {
		if room.data.Type != "TTLSession" || !room.migrationUntil.IsZero() {
			continue
		}
		match := room.fields["levelName"] == level
		if request.Method == 3 {
			score := float64(0)
			if stats, ok := c.state.Stats[room.owner.pid]; ok {
				score = float64(stats.Values[0])
			}
			distance := score - float64(ranking)
			if distance < 0 {
				distance = -distance
			}
			match = distance <= float64(diff)
		}
		if match {
			ids = append(ids, id)
		}
	}
	return c.sessions.listLocked(ids, offset, count), nil
}
