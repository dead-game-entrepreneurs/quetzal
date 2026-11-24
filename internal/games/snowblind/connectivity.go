package snowblind

import (
	"errors"
	"sort"
	"strconv"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal/peerrelay"
)

// URL publication is limited to the ticket-proven owner and observed source IP.
// CID/RVCID claims must match the secure registration when supplied.
func (s *sessions) validateURL(who owner, text string) (string, error) {
	reg := s.peers[who]
	if text == "" {
		text = reg.url
	}
	endpoint, err := quazal.ParseStationURL(text)
	if err != nil || endpoint.PID != who.pid || endpoint.Address.Addr() != reg.peer.Addr.Addr() {
		return "", quazal.ErrRMC
	}
	original, err := quazal.ParseStationURL(reg.url)
	if err != nil {
		return "", err
	}
	for _, key := range []string{"CID", "RVCID"} {
		if endpoint.Fields[key] != "" && endpoint.Fields[key] != original.Fields[key] {
			return "", quazal.ErrRMC
		}
	}
	published := quazal.WithRVCID(text)
	if len(published) > 512 {
		return "", quazal.ErrRMC
	}
	return published, nil
}

// Browser queries may reach a published host. Participant addresses require
// shared room membership. The same rule governs direct and virtual probes.
func (s *sessions) accessibleInRoom(caller, target owner, room *room) bool {
	if room == nil {
		return false
	}
	if room.owner == target && room.url != "" {
		return true
	}
	if room.owner != caller {
		if _, ok := room.members[caller]; !ok {
			return false
		}
	}
	_, ok := room.members[target]
	return ok
}
func (s *sessions) accessibleTarget(caller, target owner) bool {
	for _, room := range s.rooms {
		if s.accessibleInRoom(caller, target, room) {
			return true
		}
	}
	return false
}
func relayOwner(who owner) peerrelay.Owner {
	return peerrelay.Owner{LocalPort: who.port, PeerID: who.peer, PID: who.pid}
}
func (s *sessions) relayIdentity(who owner, url string) peerrelay.Identity {
	reg := s.peers[who]
	cid, _ := strconv.ParseUint(peerrelay.Field(reg.url, "RVCID"), 10, 32)
	return peerrelay.Identity{Owner: relayOwner(who), Peer: reg.peer, CID: uint32(cid), URL: url}
}

// Caller holds the session lock. Never offer an arbitrary client endpoint.
func (s *sessions) offeredURLs(caller owner, gid uint32, target owner, direct string) []string {
	if direct == "" {
		return nil
	}
	if _, ok := s.peers[target]; !ok {
		return nil
	}
	if caller != target && !s.accessibleInRoom(caller, target, s.rooms[gid]) {
		return nil
	}
	if s.relay == nil || caller == target {
		return []string{direct}
	}
	candidates := s.relay.Offer(gid, s.relayIdentity(caller, s.peers[caller].url), s.relayIdentity(target, direct), direct)
	// Native probe cache 0x35cdc0 keys by address and RVCID, not UDP port.
	// Equal-IP direct/virtual candidates cannot retain separate probe results.
	if len(candidates) == 2 && peerrelay.Field(candidates[0], "address") == peerrelay.Field(candidates[1], "address") {
		candidates = candidates[1:]
	}
	return candidates
}
func (s *sessions) removeRoutes(gid uint32, room *room) {
	if room == nil {
		return
	}
	s.relay.RemoveParticipant(relayOwner(room.owner), gid)
	for who := range room.members {
		s.relay.RemoveParticipant(relayOwner(who), gid)
	}
}
func (s *sessions) probe(peer prudp.Peer, pid uint32, urls []string) ([]prudp.Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	who := identity(peer, pid)
	if _, ok := s.peers[who]; !ok {
		return nil, errors.New("unregistered Snowblind probe caller")
	}
	var result []prudp.Peer
	seen := map[owner]bool{}
	// A mixed direct/relay probe normally introduces both requester URLs to
	// the remote. If the requester shares the relay's IP, those introductions
	// collide in the remote's address/RVCID-only cache (0x35cdc0). Introduce
	// only the virtual counterpart for this batch; the requester can still
	// attempt its direct candidate normally.
	virtualIntroduction := map[owner]bool{}
	for _, url := range urls {
		normalized := quazal.WithRVCID(url)
		if target, _, issued := s.relay.ProbeTarget(relayOwner(who), normalized); issued && peerrelay.Field(normalized, "address") == peer.Addr.Addr().String() {
			virtualIntroduction[owner{port: target.LocalPort, peer: target.PeerID, pid: target.PID}] = true
		}
	}
	for _, url := range urls {
		normalized := quazal.WithRVCID(url)
		if target, gid, issued := s.relay.ProbeTarget(relayOwner(who), normalized); issued {
			remote := owner{port: target.LocalPort, peer: target.PeerID, pid: target.PID}
			if _, live := s.peers[remote]; !live || !s.accessibleInRoom(who, remote, s.rooms[gid]) {
				return nil, errors.New("Snowblind relay target is no longer accessible")
			}
			s.relay.RequestProbe(relayOwner(who), normalized)
			continue
		}
		found := false
		for target, reg := range s.peers {
			matches := reg.url == normalized
			if !matches {
				for _, room := range s.rooms {
					if room.owner == target && room.url == normalized {
						matches = true
						break
					}
				}
			}
			if !matches {
				continue
			}
			if target == who {
				found = true
				break
			}
			if s.accessibleTarget(who, target) {
				if s.relayMode == "relay" {
					return nil, errors.New("forced Snowblind relay requires the issued virtual URL")
				}
				found = true
				if virtualIntroduction[target] {
					break
				}
				if !seen[target] {
					seen[target] = true
					result = append(result, reg.peer)
				}
				break
			}
		}
		if !found {
			return nil, errors.New("Snowblind probe target is not published")
		}
	}
	return result, nil
}
func (s *sessions) connectionURLs(peer prudp.Peer, pid, connectionID, targetPID uint32) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	who := identity(peer, pid)
	if _, ok := s.peers[who]; !ok {
		return nil, errors.New("unregistered Snowblind URL lookup caller")
	}
	var result []string
	for target, reg := range s.peers {
		if target.pid != targetPID || (target != who && !s.accessibleTarget(who, target)) {
			continue
		}
		// Bind both lookup identifiers to this title's proven live registration.
		parsed, err := quazal.ParseStationURL(reg.url)
		if err != nil {
			continue
		}
		if connectionID != 0 && parsed.Fields["CID"] != strconv.FormatUint(uint64(connectionID), 10) {
			continue
		}

		if target == who {
			result = append(result, reg.url)
			continue
		}
		ids := []uint32{}
		for gid, room := range s.rooms {
			if s.accessibleInRoom(who, target, room) {
				ids = append(ids, gid)
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if len(ids) > 0 {
			gid := ids[0]
			url := reg.url
			if room := s.rooms[gid]; room.owner == target && room.url != "" {
				url = room.url
			}
			result = append(result, s.offeredURLs(who, gid, target, url)...)
		}
	}
	return result, nil
}
