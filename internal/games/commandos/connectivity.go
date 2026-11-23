package commandos

import (
	"sort"
	"strconv"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal/peerrelay"
)

func relayOwner(p participant) peerrelay.Owner {
	return peerrelay.Owner{LocalPort: p.Port, PeerID: p.Peer, PID: p.PID}
}
func (s *sessions) relayIdentity(p participant, url string) peerrelay.Identity {
	reg := s.peers[p]
	cid, _ := strconv.ParseUint(peerrelay.Field(reg.url, "RVCID"), 10, 32)
	return peerrelay.Identity{Owner: relayOwner(p), Peer: reg.peer, CID: uint32(cid), URL: url}
}

// The SDK groups addresses by RVCID and tries them in order. Keep all
// candidates for each participant contiguous. Prefer direct unless its IP
// matches the relay: this browser merges equal address/RVCID probes even when
// ports differ, so it must receive only the virtual endpoint in that case.
// Caller holds s.mu; only proven registrations and this room's members enter.
func (s *sessions) participantURLs(caller participant, id uint32, room *room) []string {
	endpoints := map[participant]string{}
	if room.url != "" {
		endpoints[room.owner] = room.url
	}
	for member := range room.members {
		if member == room.owner && room.url != "" {
			continue
		}
		if reg, ok := s.peers[member]; ok {
			endpoints[member] = reg.url
		}
	}
	ordered := make([]participant, 0, len(endpoints))
	for p := range endpoints {
		ordered = append(ordered, p)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].PID < ordered[j].PID })
	var result []string
	for _, remote := range ordered {
		direct := endpoints[remote]
		if remote == caller || s.relay == nil {
			result = append(result, direct)
			continue
		}
		candidates := s.relay.Offer(id, s.relayIdentity(caller, s.peers[caller].url), s.relayIdentity(remote, direct), direct)
		if len(candidates) == 2 && peerrelay.Field(candidates[0], "address") == peerrelay.Field(candidates[1], "address") {
			candidates = candidates[1:]
		}
		result = append(result, candidates...)
	}
	return result
}
func (s *sessions) removeRoomRoutes(id uint32, room *room) {
	s.relay.RemoveParticipant(relayOwner(room.owner), id)
	for member := range room.members {
		s.relay.RemoveParticipant(relayOwner(member), id)
	}
}
