package ttl25

import (
	"encoding/binary"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

const sessionMigrationTTL = 30 * time.Second

// All registry helpers here require s.mu. A migration is a vacancy, not a
// server-selected successor: the native session elects its new host. Only an
// exact, registered participant connection may claim that vacancy with 21/27.
func (s *sessionRegistry) leaveRoom(who sessionOwner, gid uint32, room sessionRecord, reason string) bool {
	_, member := room.members[who]
	if room.owner != who && !member {
		return false
	}
	if room.owner == who {
		s.finishRound(gid, room)
	}
	s.relay.removeParticipant(who, gid)
	delete(room.members, who)
	delete(s.invites[gid], who.pid)
	if room.owner == who && room.migrationUntil.IsZero() {
		if room.data.Type != "TTLSession" || !s.hasLiveMembers(room) {
			s.removeRoom(gid, room, reason)
			return true
		}
		room.url = ""
		room.departedHost = who
		room.owner = sessionOwner{}
		room.migrationUntil = s.now().Add(sessionMigrationTTL)
		s.roomEvent("matchmaking host migration pending", who, gid, room,
			"reason", reason, "expires", room.migrationUntil)
	}
	s.updateCount(&room)
	s.rooms[gid] = room
	if member {
		s.roomEvent("matchmaking participant left", who, gid, room, "reason", reason)
	}
	s.pruneMigrations()
	return true
}

func (s *sessionRegistry) hasLiveMembers(room sessionRecord) bool {
	for who := range room.members {
		if _, live := s.peers[who]; live {
			return true
		}
	}
	return false
}

func (s *sessionRegistry) removeRoom(gid uint32, room sessionRecord, reason string) {
	s.roomEvent("matchmaking room removed", room.owner, gid, room, "reason", reason)
	// Removing a room revokes every candidate for it, including guest-to-guest
	// pairs. Proved routes retain only the existing short native leave drain.
	for who := range room.members {
		s.relay.removeParticipant(who, gid)
	}
	s.relay.removeParticipant(room.owner, gid)
	delete(s.rooms, gid)
	delete(s.invites, gid)
}

func (s *sessionRegistry) pruneMigrations() {
	for gid, room := range s.rooms {
		if !room.migrationUntil.IsZero() && (!s.now().Before(room.migrationUntil) || !s.hasLiveMembers(room)) {
			s.removeRoom(gid, room, "host migration expired or participants left")
		}
	}
}

func (s *sessionRegistry) migrationInstructions() []quazal.ProbeInstruction {
	s.mu.Lock()
	s.pruneMigrations()
	s.mu.Unlock()
	if s.relay != nil {
		return s.relay.drainInstructions()
	}
	return nil
}

// The stock HostMigrationExtension sends an empty StationURL at 0x002fe8c8.
// Resolve it to this connection's authenticated observed endpoint. Never use
// another participant's address or grant ownership from a supplied PID.
func (s *sessionRegistry) updateHost(who sessionOwner, gid uint32, room sessionRecord, method uint32, url string) bool {
	if room.migrationUntil.IsZero() {
		if room.owner != who {
			return false
		}
	} else {
		_, member := room.members[who]
		_, live := s.peers[who]
		if method != 27 || !member || !live || who == room.owner {
			return false
		}
		for id, other := range s.rooms {
			if id != gid && other.owner == who && other.data.Type == room.data.Type && other.migrationUntil.IsZero() {
				return false
			}
		}
	}
	if url == "" && method == 27 {
		url = s.urls[who]
	}
	if !validSessionURL(url, who.pid) {
		return false
	}
	if !room.migrationUntil.IsZero() {
		previous := room.departedHost
		room.owner = who
		room.departedHost = sessionOwner{}
		room.migrationUntil = time.Time{}
		binary.LittleEndian.PutUint32(room.data.Payload[8:], who.pid)
		binary.LittleEndian.PutUint32(room.data.Payload[12:], who.pid)
		s.roomEvent("matchmaking host migrated", who, gid, room,
			"previous_owner_pid", previous.pid, "previous_owner_peer", previous.peer)
	}
	room.url = url
	s.rooms[gid] = room
	s.roomEvent("matchmaking host endpoint published", who, gid, room)
	return true
}
