package ttl25

import (
	"strconv"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// Competition 26/2 registers a round with its ranking. The retail round-create
// path calls writer 0x534d78 with Ranking 1 and the new TTLRound's gathering ID.
func (s *sessionRegistry) registerCompetitionRound(peer prudp.Peer, pid uint32, args []byte) ([]byte, error) {
	r := rankingReader{b: args}
	competition, gid := r.u32(), r.u32()
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	room, exists := s.rooms[gid]
	// The competition association is supplied by this RPC, separately from
	// TTLRound's initial fields. Retail creates the round with a zero trailing
	// value before calling 26/2 with the selected Ranking ID.
	if !exists || room.owner != ownerOf(peer, pid) || room.data.Type != "TTLRound" || competition != 1 {
		return boolReply(false), nil
	}
	room.fields["competition"] = strconv.FormatUint(uint64(competition), 10)
	s.rooms[gid] = room
	return boolReply(true), nil
}
