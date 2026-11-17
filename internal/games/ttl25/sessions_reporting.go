package ttl25

import "time"

// Round reports and EndParticipation run independently on every console.
// Retain only reporting authority briefly after removing a round from discovery.
// Callers hold s.mu; finished rounds never supply browse or connection targets.
func (s *sessionRegistry) pruneFinished() {
	for id, round := range s.finished {
		if !s.now().Before(round.expires) {
			delete(s.finished, id)
		}
	}
}

func (s *sessionRegistry) finishRound(gid uint32, room sessionRecord) {
	if room.data.Type != "TTLRound" {
		return
	}
	s.pruneFinished()
	if len(s.finished) >= 1024 {
		var oldest uint32
		for id := range s.finished {
			if oldest == 0 || id < oldest {
				oldest = id
			}
		}
		delete(s.finished, oldest)
	}
	members := make(map[sessionOwner]string, len(room.members))
	for owner, message := range room.members {
		members[owner] = message
	}
	room.members = members
	s.finished[gid] = finishedRound{room: room, expires: s.now().Add(2 * time.Minute)}
}
