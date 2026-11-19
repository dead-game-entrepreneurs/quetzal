package ttl25

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func validSessionURL(url string, pid uint32) bool {
	if len(url) > 512 || (!strings.HasPrefix(url, "prudps:/") && !strings.HasPrefix(url, "prudp:/")) {
		return false
	}
	_, body, _ := strings.Cut(url, ":/")
	values := map[string]string{}
	for _, part := range strings.Split(body, ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok || k == "" || v == "" {
			return false
		}
		if _, ok := values[k]; ok {
			return false
		}
		values[k] = v
	}
	address, err := netip.ParseAddr(values["address"])
	if err != nil || !address.Is4() || address.IsUnspecified() || address.IsMulticast() {
		return false
	}
	port, err := strconv.ParseUint(values["port"], 10, 16)
	if err != nil || port == 0 {
		return false
	}
	id, err := strconv.ParseUint(values["PID"], 10, 32)
	return err == nil && uint32(id) == pid
}
func uniqueMembers(room sessionRecord) []uint32 {
	ids := map[uint32]bool{}
	for owner := range room.members {
		ids[owner.pid] = true
	}
	return sortedPIDs(ids)
}
func roomMember(room sessionRecord, pid uint32) bool {
	for owner := range room.members {
		if owner.pid == pid {
			return true
		}
	}
	return false
}

func (s *sessionRegistry) lobby(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
	r := rankingReader{b: request.Args}
	gid := r.u32()
	owner := ownerOf(peer, pid)
	var text string
	switch request.Method {
	case 11, 12, 26, 27:
		text = r.strLimit(512)
	case 30:
	}
	var state uint32
	if request.Method == 30 {
		state = r.u32()
	}
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneMigrations()
	room, exists := s.rooms[gid]
	switch request.Method {
	case 11:
		if !exists {
			s.joinRejected(owner, gid, "room not found")
			return boolReply(false), nil
		}
		if _, already := room.members[owner]; already {
			s.roomEvent("matchmaking participation reused", owner, gid, room)
			return boolReply(true), nil
		}
		if !room.migrationUntil.IsZero() {
			s.joinRejected(owner, gid, "host migration pending")
			return boolReply(false), nil
		}
		limit, _ := strconv.Atoi(room.fields["maxPlayers"])
		_, invited := s.invites[gid][pid]
		if roomMember(room, pid) || len(room.members) >= limit || room.fields["isPrivate"] == "1" && room.owner != owner && !invited {
			reason := "private room requires invitation"
			if roomMember(room, pid) {
				reason = "account already participating on another connection"
			} else if len(room.members) >= limit {
				reason = "room full"
			}
			s.joinRejected(owner, gid, reason)
			return boolReply(false), nil
		}
		room.members[owner] = text
		if room.data.Type == "TTLRound" {
			room.reporters[owner] = true
			room.reportPIDs[pid] = true
		}
		s.updateCount(&room)
		s.rooms[gid] = room
		delete(s.invites[gid], pid)
		s.roomEvent("matchmaking participant joined", owner, gid, room)
		return boolReply(true), nil
	case 12:
		if !exists {
			return boolReply(false), nil
		}
		return boolReply(s.leaveRoom(owner, gid, room, "participation cancelled")), nil
	case 13:
		if !exists {
			return rankingU32(nil, 0), nil
		}
		return appendIDs(nil, uniqueMembers(room)), nil
	case 15:
		if !exists {
			return rankingU32(nil, 0), nil
		}
		ids := uniqueMembers(room)
		b := rankingU32(nil, uint32(len(ids)))
		for _, id := range ids {
			message := ""
			for member, value := range room.members {
				if member.pid == id {
					message = value
					break
				}
			}
			b = rankingU32(b, id)
			b = rankingString(b, s.name(id))
			b = rankingString(b, message)
		}
		return b, nil
	case 16:
		if !exists || room.fields["isPrivate"] == "1" && !roomMember(room, pid) && room.owner != owner {
			return rankingU32(nil, 0), nil
		}
		list := s.participantURLsFor(owner, gid, room)
		s.logger().Info("matchmaking participant URLs returned", "pid", pid, "peer", peer.ID,
			"gid", gid, "url_count", len(list), "participants", len(uniqueMembers(room)),
			"caller_is_member", roomMember(room, pid))
		b := rankingU32(nil, uint32(len(list)))
		for _, u := range list {
			b = rankingString(b, u)
		}
		return b, nil
	case 26, 27:
		if !exists || !s.updateHost(owner, gid, room, request.Method, text) {
			s.logger().Warn("matchmaking host update rejected", "pid", pid, "peer", peer.ID,
				"gid", gid, "method", request.Method, "room_exists", exists, "url_empty", text == "")
			return boolReply(false), nil
		}
		return boolReply(true), nil
	case 28:
		ok := exists && room.url != "" && (room.fields["isPrivate"] != "1" || roomMember(room, pid) || room.owner == owner)
		url := ""
		if ok {
			url = s.forcedURL(owner, room.owner, gid, room.url)
			ok = url != ""
		}
		return rankingString(boolReply(ok), url), nil
	case 29:
		if !exists {
			return rankingU32(boolReply(false), 0), nil
		}
		return rankingU32(boolReply(true), room.state), nil
	case 30:
		if !exists || room.owner != owner || !room.migrationUntil.IsZero() {
			return boolReply(false), nil
		}
		room.state = state
		binary.LittleEndian.PutUint32(room.data.Payload[32:], state)
		s.rooms[gid] = room
		s.roomEvent("matchmaking room state changed", owner, gid, room)
		return boolReply(true), nil
	default:
		return nil, errors.New("unsupported lobby method")
	}
}

// Called with s.mu held by both matchmaking protocol implementations.
func (s *sessionRegistry) participantURLs(room sessionRecord) []string {
	urls := map[string]bool{}
	if room.url != "" {
		urls[nativeStationURL(room.url)] = true
	}
	for member := range room.members {
		if member == room.owner && room.url != "" {
			continue
		}
		if u := s.urls[member]; u != "" {
			urls[u] = true
		}
	}
	list := make([]string, 0, len(urls))
	for u := range urls {
		list = append(list, u)
	}
	sort.Strings(list)
	return list
}

func (s *sessionRegistry) updateCount(room *sessionRecord) {
	count := uint32(len(uniqueMembers(*room)))
	room.fields["totalConnectedPlayers"] = strconv.FormatUint(uint64(count), 10)
	if room.data.Type == "TTLSession" {
		binary.LittleEndian.PutUint32(room.data.Payload[len(room.data.Payload)-13:], count)
	}
}

func (s *sessionRegistry) forPlayer(pid uint32) []quazal.AnyData {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneMigrations()
	ids := []uint32{}
	for id, room := range s.rooms {
		if room.data.Type == "TTLSession" && (room.owner.pid == pid || roomMember(room, pid)) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	v := make([]quazal.AnyData, 0, len(ids))
	for _, id := range ids {
		data := s.rooms[id].data
		data.Payload = append([]byte(nil), data.Payload...)
		v = append(v, data)
	}
	return v
}
func (s *sessionRegistry) invitations(pid uint32) []uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := []uint32{}
	for gid, targets := range s.invites {
		if _, ok := targets[pid]; ok {
			ids = append(ids, gid)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
func (s *sessionRegistry) reportKeyFor(peer prudp.Peer, pid uint32) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneFinished()
	var chosen uint32
	var chosenType string
	choose := func(gid uint32, room sessionRecord) {
		_, member := room.members[ownerOf(peer, pid)]
		if room.data.Type == "TTLRound" {
			member = room.reporters[ownerOf(peer, pid)]
		}
		if member {
			if chosen == 0 || room.data.Type == "TTLRound" && chosenType != "TTLRound" || room.data.Type == chosenType && gid > chosen {
				chosen, chosenType = gid, room.data.Type
			}
		}
	}
	for gid, room := range s.rooms {
		choose(gid, room)
	}
	for gid, round := range s.finished {
		choose(gid, round.room)
	}
	if chosen == 0 {
		return "", false
	}
	return fmt.Sprintf("%s:%d", s.runtime, chosen), true
}

func (s *sessionRegistry) registered(peer prudp.Peer, pid uint32, url string) {
	s.mu.Lock()
	s.urls[ownerOf(peer, pid)] = nativeStationURL(url)
	s.peers[ownerOf(peer, pid)] = peer
	s.mu.Unlock()
}
func (s *sessionRegistry) reportAuthority(peer prudp.Peer, pid, gid uint32) (string, map[uint32]bool, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneFinished()
	room, ok := s.rooms[gid]
	if !ok {
		if round, exists := s.finished[gid]; exists {
			room, ok = round.room, true
		}
	}
	owner := ownerOf(peer, pid)
	if room.data.Type == "TTLRound" {
		if !ok || !room.reporters[owner] {
			return "", nil, false, false
		}
		ids := make(map[uint32]bool, len(room.reportPIDs))
		for id := range room.reportPIDs {
			ids[id] = true
		}
		return fmt.Sprintf("%s:%d", s.runtime, gid), ids, room.owner == owner, true
	}
	_, member := room.members[owner]
	if !ok || room.owner != owner && !member {
		return "", nil, false, false
	}
	ids := map[uint32]bool{}
	for _, id := range uniqueMembers(room) {
		ids[id] = true
	}
	return fmt.Sprintf("%s:%d", s.runtime, gid), ids, room.owner == owner, true
}
