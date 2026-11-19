package ttl25

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
)

// Admit only currently published room endpoints. Private rooms require an
// invitation or membership; no supplied URL becomes an arbitrary UDP target.
func (s *sessionRegistry) probeTargets(peer prudp.Peer, pid uint32, urls []string) ([]prudp.Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	caller := ownerOf(peer, pid)
	if _, ok := s.peers[caller]; !ok {
		return nil, errors.New("probe requester is unavailable")
	}
	seen := map[sessionOwner]bool{}
	var targets []prudp.Peer
	for _, url := range urls {
		url = nativeStationURL(url)
		// GetParticipantsURLs includes the requester after participation. A
		// retry may ask to probe that same endpoint; no self-introduction is needed.
		if url == s.urls[caller] {
			continue
		}
		if remote, gid, issued := s.relay.probeTarget(caller, url); issued {
			room, exists := s.rooms[gid]
			_, invited := s.invites[gid][pid]
			_, liveCaller := s.peers[caller]
			_, liveRemote := s.peers[remote]
			if !exists || !liveCaller || !liveRemote || room.owner != remote && !roomMember(room, remote.pid) || room.fields["isPrivate"] == "1" && room.owner != caller && !roomMember(room, pid) && !invited {
				return nil, errors.New("virtual probe target is no longer accessible")
			}
			s.relay.requestProbe(caller, url)
			continue // never introduce the real endpoint for a virtual target
		}
		found := false
		for gid, room := range s.rooms {
			_, invited := s.invites[gid][pid]
			if room.fields["isPrivate"] == "1" && room.owner != caller && !roomMember(room, pid) && !invited {
				continue
			}
			remote := room.owner
			matched := room.url != "" && nativeStationURL(room.url) == url
			if !matched {
				for member := range room.members {
					if s.urls[member] != "" && s.urls[member] == url {
						remote, matched = member, true
						break
					}
				}
			}
			if !matched {
				continue
			}
			if s.relay != nil && s.relay.cfg.Mode == "relay" && remote != caller {
				return nil, errors.New("forced relay requires the issued virtual probe target")
			}
			target, ok := s.peers[remote]
			if !ok {
				continue
			}
			found = true
			if !seen[remote] {
				seen[remote] = true
				targets = append(targets, target)
			}
			break
		}
		if !found {
			return nil, errors.New("probe target is not a published accessible room")
		}
	}
	return targets, nil
}

// Forced mode selects the virtual endpoint even before SecureConnection 11/3.
// Caller holds s.mu throughout lookup and relay candidate publication.
func (s *sessionRegistry) forcedURL(caller, remote sessionOwner, gid uint32, direct string) string {
	if s.relay == nil || s.relay.cfg.Mode != "relay" || caller == remote {
		return direct
	}
	aPeer, aLive := s.peers[caller]
	bPeer, bLive := s.peers[remote]
	if !aLive || !bLive {
		return ""
	}
	aCID, _ := strconv.ParseUint(stationField(s.urls[caller], "CID"), 10, 32)
	bCID, _ := strconv.ParseUint(stationField(s.urls[remote], "CID"), 10, 32)
	a := relayIdentity{caller, aPeer, uint32(aCID), s.urls[caller]}
	b := relayIdentity{remote, bPeer, uint32(bCID), nativeStationURL(direct)}
	urls := s.relay.offer(gid, a, b, direct)
	if len(urls) != 1 {
		return ""
	}
	return urls[0]
}

// Browse requires a successful native ping for every participant, before 11/3.
// Keep alternatives adjacent and within the protocol-3 URL budget.
func (s *sessionRegistry) participantURLsFor(caller sessionOwner, gid uint32, room sessionRecord) []string {
	list := s.participantURLs(room)
	if s.relay == nil {
		return list
	}
	var result []string
	spare := max(0, 32-len(list))
	for _, url := range list {
		remote := room.owner
		if room.url == "" || nativeStationURL(room.url) != url {
			for member := range room.members {
				if s.urls[member] == url {
					remote = member
					break
				}
			}
		}
		if s.relay.cfg.Mode == "adaptive" {
			candidates := []string{url}
			aPeer, aLive := s.peers[caller]
			bPeer, bLive := s.peers[remote]
			// Preserve every direct participant, and keep alternate URLs for a
			// participant adjacent: retail groups them by PID/RVCID. Protocol 3/1
			// accepts at most 32 URLs; the stock 16-player room fits both per peer.
			if caller != remote && aLive && bLive && spare > 0 {
				aCID, _ := strconv.ParseUint(stationField(s.urls[caller], "CID"), 10, 32)
				bCID, _ := strconv.ParseUint(stationField(s.urls[remote], "CID"), 10, 32)
				a := relayIdentity{caller, aPeer, uint32(aCID), s.urls[caller]}
				b := relayIdentity{remote, bPeer, uint32(bCID), url}
				candidates = s.relay.offer(gid, a, b, url)
				spare -= len(candidates) - 1
			}
			result = append(result, candidates...)
		} else if routed := s.forcedURL(caller, remote, gid, url); routed != "" {
			result = append(result, routed)
		}
	}
	return result
}

// RVCID identifies the native NAT probe record. Secure registration assigns
// CID; the retail host adds the same value when publishing its native URL.
func nativeStationURL(url string) string {
	var cid string
	for _, part := range strings.Split(url, ";") {
		if strings.HasPrefix(part, "RVCID=") {
			return url
		}
		if strings.HasPrefix(part, "CID=") {
			cid = strings.TrimPrefix(part, "CID=")
		}
	}
	if n, err := strconv.ParseUint(cid, 10, 32); err == nil && n != 0 {
		return url + ";RVCID=" + cid
	}
	return url
}

func (s *sessionRegistry) connectionURLs(peer prudp.Peer, pid, cid, targetPID uint32) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	caller := ownerOf(peer, pid)
	if _, ok := s.peers[caller]; !ok {
		return nil, errors.New("connection requester is unavailable")
	}
	if cid == 0 && targetPID == 0 {
		return nil, nil
	}
	type endpoint struct {
		gid   uint32
		owner sessionOwner
	}
	urls := map[string]endpoint{}
	for gid, room := range s.rooms {
		if room.url == "" || targetPID != 0 && room.owner.pid != targetPID {
			continue
		}
		if _, live := s.peers[room.owner]; !live {
			continue
		}
		_, invited := s.invites[gid][pid]
		if room.fields["isPrivate"] == "1" && room.owner != caller && !roomMember(room, pid) && !invited {
			continue
		}
		if cid != 0 {
			matched := false
			for _, part := range strings.Split(room.url, ";") {
				if part == "CID="+strconv.FormatUint(uint64(cid), 10) {
					matched = true
				}
			}
			if !matched {
				continue
			}
		}
		urls[room.url] = endpoint{gid, room.owner}
	}
	result := make([]string, 0, len(urls))
	for url := range urls {
		result = append(result, url)
	}
	sort.Strings(result)
	if s.relay != nil {
		var candidates []string
		for _, url := range result {
			e := urls[url]
			aID, _ := strconv.ParseUint(stationField(s.urls[caller], "CID"), 10, 32)
			bID, _ := strconv.ParseUint(stationField(s.urls[e.owner], "CID"), 10, 32)
			a := relayIdentity{caller, peer, uint32(aID), s.urls[caller]}
			b := relayIdentity{e.owner, s.peers[e.owner], uint32(bID), nativeStationURL(url)}
			candidates = append(candidates, s.relay.offer(e.gid, a, b, url)...)
		}
		if len(candidates) <= 32 {
			result = candidates
		}
	}
	return result, nil
}
