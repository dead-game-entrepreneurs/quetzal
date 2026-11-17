package ttl25

import (
	"errors"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// MatchMakingProtocolExt (50), dispatcher 0x540908, has four methods.
func (s *sessionRegistry) extension(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
	r := rankingReader{b: request.Args}
	if request.Method < 1 || request.Method > 4 {
		return nil, errors.New("unsupported matchmaking extension method")
	}
	if request.Method == 4 {
		return s.gatheringURLs(peer, pid, request.Args)
	}
	gid := r.u32()
	if request.Method == 1 {
		r.strLimit(512)
	} else {
		r.boolean()
	}
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	owner := ownerOf(peer, pid)
	s.mu.Lock()
	room, exists := s.rooms[gid]
	if request.Method == 1 {
		defer s.mu.Unlock()
		if !exists {
			s.pruneFinished()
			if round, ok := s.finished[gid]; ok {
				_, member := round.room.members[owner]
				return boolReply(member || round.room.owner == owner), nil
			}
			return boolReply(false), nil
		}
		if room.owner == owner {
			s.finishRound(gid, room)
			s.relay.removeParticipant(owner, gid)
			delete(s.rooms, gid)
			delete(s.invites, gid)
			return boolReply(true), nil
		}
		if _, member := room.members[owner]; !member {
			return boolReply(false), nil
		}
		s.relay.removeParticipant(owner, gid)
		delete(room.members, owner)
		delete(s.invites[gid], pid)
		s.updateCount(&room)
		s.rooms[gid] = room
		return boolReply(true), nil
	}
	accessible := exists && (room.fields["isPrivate"] != "1" || room.owner == owner || roomMember(room, pid))
	s.mu.Unlock()
	if !accessible {
		return rankingU32(nil, 0), nil
	}
	// All retained members are live registered connections. The SDK's bool
	// filter cannot introduce offline records in this in-memory registry.
	method := uint32(13)
	if request.Method == 3 {
		method = 15
	}
	return s.lobby(peer, pid, quazal.Request{Method: method, Args: rankingU32(nil, gid)})
}

func (s *sessionRegistry) gatheringURLs(peer prudp.Peer, pid uint32, args []byte) ([]byte, error) {
	r := rankingReader{b: args}
	ids := r.ids(32)
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	owner := ownerOf(peer, pid)
	seen := map[uint32]bool{}
	var records [][]byte
	for _, gid := range ids {
		room, exists := s.rooms[gid]
		if seen[gid] || !exists || room.fields["isPrivate"] == "1" && room.owner != owner && !roomMember(room, pid) {
			continue
		}
		seen[gid] = true
		urls := s.participantURLsFor(owner, gid, room)
		b := rankingU32(nil, gid)
		b = rankingU32(b, uint32(len(urls)))
		for _, url := range urls {
			b = rankingString(b, url)
		}
		records = append(records, b)
	}
	b := rankingU32(nil, uint32(len(records)))
	for _, record := range records {
		b = append(b, record...)
	}
	return b, nil
}
