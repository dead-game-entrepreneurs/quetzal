package commandos

import (
	"bytes"
	"errors"
	"sort"
	"sync"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal/peerrelay"
)

type participant struct {
	Port uint16
	Peer uint64
	PID  uint32
}

func identity(peer prudp.Peer, pid uint32) participant {
	return participant{peer.LocalPort, peer.ID, pid}
}

type registration struct {
	peer prudp.Peer
	url  string
}
type room struct {
	owner   participant
	data    gathering
	input   []byte
	members map[participant]bool
	url     string
}
type sessions struct {
	mu        sync.Mutex
	next      uint32
	rooms     map[uint32]*room
	peers     map[participant]registration
	relay     *peerrelay.Relay
	relayMode string
}

func newSessions() *sessions {
	return &sessions{next: 10000, rooms: map[uint32]*room{}, peers: map[participant]registration{}}
}
func (s *sessions) registered(peer prudp.Peer, pid uint32, url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers[identity(peer, pid)] = registration{peer, quazal.WithRVCID(url)}
}
func (s *sessions) disconnect(peer prudp.Peer, pid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	who := identity(peer, pid)
	delete(s.peers, who)
	s.relay.RemoveParticipant(relayOwner(who), 0)
	for id, room := range s.rooms {
		if room.owner == who {
			s.removeRoomRoutes(id, room)
			delete(s.rooms, id)
		} else {
			delete(room.members, who)
		}
	}
}
func boolResult(v bool) ([]byte, error) {
	var w quazal.Writer
	w.Bool(v)
	return w.Bytes()
}
func (s *sessions) handle(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
	if pid < 1000 {
		return nil, errors.New("Commandos requires a named account")
	}
	who := identity(peer, pid)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.peers[who]; !ok {
		return nil, errors.New("Commandos connection is not registered")
	}
	if request.Protocol == 21 && (request.Method == 1 || request.Method == 4) {
		return s.put(who, request)
	}
	if request.Protocol == 21 && request.Method == 19 {
		return s.search(request.Args)
	}
	if request.Protocol != 21 && !(request.Protocol == 50 && request.Method == 2) {
		return nil, errors.New("unsupported Commandos protocol")
	}
	r := quazal.NewReader(request.Args)
	id := r.U32()
	var text string
	var state uint32
	if request.Protocol == 50 {
		r.Bool()
	} else {
		switch request.Method {
		case 11, 12, 26, 27:
			text = r.String(512)
		case 30:
			state = r.U32()
		case 13, 16:
		default:
			return nil, errors.New("unsupported Commandos matchmaking method")
		}
	}
	if !r.Done() {
		return nil, quazal.ErrRMC
	}
	room, exists := s.rooms[id]
	if request.Protocol == 50 || request.Method == 13 {
		var ids []uint32
		if exists {
			for member := range room.members {
				ids = append(ids, member.PID)
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var w quazal.Writer
		w.U32(uint32(len(ids)))
		for _, pid := range ids {
			w.U32(pid)
		}
		return w.Bytes()
	}
	if request.Method == 16 {
		var list []string
		if exists {
			list = s.participantURLs(who, id, room)
		}
		var w quazal.Writer
		w.U32(uint32(len(list)))
		for _, url := range list {
			w.String(url)
		}
		return w.Bytes()
	}
	if !exists {
		return boolResult(false)
	}
	switch request.Method {
	case 11:
		if room.members[who] {
			return boolResult(true)
		}
		if len(room.members) >= 8 {
			return boolResult(false)
		}
		for member := range room.members {
			if member.PID == pid {
				return boolResult(false)
			}
		}
		room.members[who] = true
		return boolResult(true)
	case 12:
		if room.owner == who {
			s.removeRoomRoutes(id, room)
			delete(s.rooms, id)
		} else {
			s.relay.RemoveParticipant(relayOwner(who), id)
			delete(room.members, who)
		}
		return boolResult(true)
	case 26, 27:
		if room.owner != who {
			return boolResult(false)
		}
		// The host passes an empty String to RegisterLocalURL (0x464610).
		// In that form publish the connection's proven observed endpoint.
		if text == "" {
			text = s.peers[who].url
		}
		endpoint, err := quazal.ParseStationURL(text)
		if err != nil || endpoint.PID != pid || endpoint.Address.Addr() != peer.Addr.Addr() {
			return boolResult(false)
		}
		registered, err := quazal.ParseStationURL(s.peers[who].url)
		if err != nil || endpoint.Fields["CID"] != registered.Fields["CID"] ||
			(endpoint.Fields["RVCID"] != "" && endpoint.Fields["RVCID"] != registered.Fields["RVCID"]) {
			return boolResult(false)
		}
		published := quazal.WithRVCID(text)
		if len(published) > 512 {
			return boolResult(false)
		}
		room.url = published
		return boolResult(true)
	case 30:
		if room.owner != who {
			return boolResult(false)
		}
		room.data.State = state
		return boolResult(true)
	}
	return nil, errors.New("unsupported Commandos matchmaking method")
}

// Caller holds s.mu. Counts are native host reports, not a backend capacity.
func (s *sessions) put(who participant, request quazal.Request) ([]byte, error) {
	g, err := decodeGathering(request.Args)
	if err != nil {
		return nil, err
	}
	if (g.OwnerPID != 0 && g.OwnerPID != who.PID) || (g.HostPID != 0 && g.HostPID != who.PID) {
		return nil, errors.New("foreign Commandos gathering identity")
	}
	if request.Method == 4 {
		room, ok := s.rooms[g.ID]
		if !ok || room.owner != who {
			return boolResult(false)
		}
		g.OwnerPID = who.PID
		g.HostPID = who.PID
		room.data = g
		return boolResult(true)
	}
	if g.ID != 0 {
		return nil, quazal.ErrRMC
	}
	for id, room := range s.rooms {
		if room.owner.PID == who.PID {
			if room.owner == who && bytes.Equal(room.input, request.Args) {
				var w quazal.Writer
				w.U32(id)
				return w.Bytes()
			}
			return nil, errors.New("account already hosts a Commandos gathering")
		}
	}
	if len(s.rooms) >= 1024 || s.next == 0 {
		return nil, errors.New("Commandos gathering capacity reached")
	}
	id := s.next
	s.next++
	g.ID = id
	g.OwnerPID = who.PID
	g.HostPID = who.PID
	s.rooms[id] = &room{owner: who, data: g, input: bytes.Clone(request.Args), members: map[participant]bool{}}
	var w quazal.Writer
	w.U32(id)
	return w.Bytes()
}
func (s *sessions) search(args []byte) ([]byte, error) {
	r := quazal.NewReader(args)
	pattern := r.String(6)
	offset, count := r.U32(), r.U32()
	if !r.Done() || count > 100 {
		return nil, quazal.ErrRMC
	}
	match, err := descriptionFilter(pattern)
	if err != nil {
		return nil, err
	}
	if offset == 0xffffffff {
		offset = 0
	}
	var ids []uint32
	for id, room := range s.rooms {
		if match(room.data.Description) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	start := min(uint64(offset), uint64(len(ids)))
	end := min(start+uint64(count), uint64(len(ids)))
	var w quazal.Writer
	w.U32(uint32(end - start))
	for _, id := range ids[start:end] {
		encoded, err := s.rooms[id].data.encode()
		if err != nil {
			return nil, err
		}
		w.Raw(encoded)
	}
	return w.Bytes()
}

// Introductions can target only a currently published host or room participant.
// The shared NAT service sends to the proven connection, never a supplied IP.
func (s *sessions) probe(peer prudp.Peer, pid uint32, urls []string) ([]prudp.Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	who := identity(peer, pid)
	caller, ok := s.peers[who]
	if !ok {
		return nil, errors.New("unregistered Commandos probe caller")
	}
	seen := map[participant]bool{}
	var targets []prudp.Peer
	for _, url := range urls {
		url = quazal.WithRVCID(url)
		if url == caller.url {
			continue
		}
		if target, gid, issued := s.relay.ProbeTarget(relayOwner(who), url); issued {
			room, exists := s.rooms[gid]
			remote := participant{Port: target.LocalPort, Peer: target.PeerID, PID: target.PID}
			_, live := s.peers[remote]
			if !exists || !live || (room.owner != remote && !room.members[remote]) {
				return nil, errors.New("Commandos relay target is no longer accessible")
			}
			s.relay.RequestProbe(relayOwner(who), url)
			continue
		}
		found := false
		for _, room := range s.rooms {
			if room.url == url {
				if s.relayMode == "relay" && room.owner != who {
					return nil, errors.New("forced Commandos relay requires the issued virtual URL")
				}
				found = true
				if room.owner != who && !seen[room.owner] {
					seen[room.owner] = true
					targets = append(targets, s.peers[room.owner].peer)
				}
				break
			}
			if !room.members[who] && room.owner != who {
				continue
			}
			for member := range room.members {
				if reg, ok := s.peers[member]; ok && reg.url == url {
					if s.relayMode == "relay" && member != who {
						return nil, errors.New("forced Commandos relay requires the issued virtual URL")
					}
					found = true
					if member != who && !seen[member] {
						seen[member] = true
						targets = append(targets, reg.peer)
					}
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return nil, errors.New("Commandos probe target is not published")
		}
	}
	return targets, nil
}
func (s *sessions) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rooms)
}
