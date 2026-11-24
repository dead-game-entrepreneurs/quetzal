package snowblind

import "github.com/princebyyourlogic/quetzal/internal/protocol/quazal"

func clanCanRecruit(clan clanRecord, pid uint32) bool {
	return clan.FounderPID == pid || containsPID(clan.Officers, pid)
}

// Native 0x170848/0x170a08 send a player-name String for Promote/Demote;
// decoder 0x171f98 reads a single u32 status. These change Officer status,
// while leadership transfer is the distinct method 15.
func (s *clanStore) roleRPC(pid uint32, request quazal.Request) ([]byte, error) {
	r := quazal.NewReader(request.Args)
	name := r.String(255)
	if !r.Done() {
		return nil, quazal.ErrRMC
	}
	index := s.memberClan(pid)
	if index < 0 {
		return clanStatus(clanNotMember)
	}
	clan := s.records[index]
	if clan.FounderPID != pid {
		return clanStatus(clanPermission)
	}
	target, ok := s.accounts.Lookup(name)
	if !ok || !clanHasMember(clan, target.PID) {
		return clanStatus(clanInvalidPlayer)
	}
	if target.PID == clan.FounderPID {
		return clanStatus(clanPermission)
	}
	promote := request.Method == 13
	if containsPID(clan.Officers, target.PID) == promote {
		return clanStatus(0)
	}
	next := append([]clanRecord{}, s.records...)
	if promote {
		next[index].Officers = append(append([]uint32{}, clan.Officers...), target.PID)
	} else {
		next[index].Officers = withoutPID(clan.Officers, target.PID)
	}
	if err := s.save(next); err != nil {
		return nil, err
	}
	s.records = next
	return clanStatus(0)
}
