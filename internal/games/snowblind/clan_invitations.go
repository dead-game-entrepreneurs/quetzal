package snowblind

import (
	"errors"
	"sort"
	"strings"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

const (
	maxClanMembers               = 64
	maxClanInvitations           = 4096
	clanPermission        uint32 = 0x67
	clanInvalidPlayer     uint32 = 0xca
	clanAlreadyInvited    uint32 = 0xce
	clanNotAccepting      uint32 = 0xcf
	clanInvalidInvitation uint32 = 0xd0
	clanFull              uint32 = 0xd2
)

type clanInvitation struct {
	Clan string `json:"clan"`
	From uint32 `json:"from"`
	To   uint32 `json:"to"`
	Seen bool   `json:"seen,omitempty"`
}

func containsPID(ids []uint32, pid uint32) bool {
	for _, id := range ids {
		if id == pid {
			return true
		}
	}
	return false
}

func withoutPID(ids []uint32, pid uint32) []uint32 {
	var result []uint32
	for _, id := range ids {
		if id != pid {
			result = append(result, id)
		}
	}
	return result
}

func clanHasMember(clan clanRecord, pid uint32) bool {
	return clan.FounderPID == pid || containsPID(clan.Members, pid)
}

func clanMembers(clan clanRecord) []uint32 {
	ids := append([]uint32{clan.FounderPID}, clan.Members...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (s *clanStore) memberClan(pid uint32) int {
	for i, clan := range s.records {
		if clanHasMember(clan, pid) {
			return i
		}
	}
	return -1
}

func (s *clanStore) accepting(pid uint32) bool { return !containsPID(s.declined, pid) }

func (s *clanStore) writeMember(w *quazal.Writer, pid uint32) {
	account, _ := s.accounts.LookupPID(pid)
	index := s.memberClan(pid)
	leader := index >= 0 && s.records[index].FounderPID == pid
	officer := index >= 0 && containsPID(s.records[index].Officers, pid)
	online := s.online != nil && s.online(pid)
	writeClanMember(w, account.Name, leader, officer, s.accepting(pid), online)
}

func (s *clanStore) writeAlerts(w *quazal.Writer, pid uint32) {
	invitation := ""
	for _, inv := range s.invitations {
		if inv.To == pid && !inv.Seen {
			invitation = inv.Clan
			break
		}
	}
	index := s.memberClan(pid)
	newNotices := index >= 0 && containsPID(s.records[index].Unread, pid)
	w.Bool(invitation != "")
	w.Bool(newNotices)
	w.Bool(false)
	w.String(invitation)
	w.String("")
}

func (s *clanStore) fetchClan(pid uint32) ([]byte, error) {
	var w quazal.Writer
	index := s.memberClan(pid)
	if index < 0 {
		w.U32(clanNotMember)
		w.String("")
		w.String("")
		w.U32(0)
		w.U32(0)
		s.writeAlerts(&w, pid)
		w.U32(0)
		s.writeMember(&w, pid)
		return w.Bytes()
	}
	clan := s.records[index]
	w.U32(0)
	w.String(clan.Abbreviation)
	w.String(clan.Name)
	w.U32(0xffffffff)
	w.U32(uint32(len(clan.Notices)))
	for _, notice := range clan.Notices {
		w.String(notice.Author)
		w.String(notice.Body)
	}
	s.writeAlerts(&w, pid)
	ids := clanMembers(clan)
	w.U32(uint32(len(ids)))
	for _, id := range ids {
		s.writeMember(&w, id)
	}
	s.writeMember(&w, pid)
	return w.Bytes()
}

func (s *clanStore) invitationRPC(pid uint32, request quazal.Request) ([]byte, error) {
	r := quazal.NewReader(request.Args)
	var first, second string
	var accepting bool
	switch request.Method {
	case 4:
	case 5, 6:
		first = r.String(255)
	case 7, 8, 9:
		first, second = r.String(255), r.String(255)
	case 10:
		accepting = r.Bool()
	default:
		return nil, quazal.ErrRMC
	}
	if !r.Done() {
		return nil, quazal.ErrRMC
	}
	if request.Method == 4 {
		var w quazal.Writer
		w.U32(0)
		incoming, outgoing := []clanInvitation{}, []clanInvitation{}
		for _, inv := range s.invitations {
			if inv.To == pid {
				incoming = append(incoming, inv)
			}
			if inv.From == pid {
				outgoing = append(outgoing, inv)
			}
		}
		for _, list := range [][]clanInvitation{incoming, outgoing} {
			w.U32(uint32(len(list)))
			for _, inv := range list {
				s.writeInvitation(&w, inv)
			}
		}
		s.writeMember(&w, pid) // Fourth output is MemberData, not a standalone bool.
		return w.Bytes()
	}
	records := append([]clanRecord{}, s.records...)
	invitations := append([]clanInvitation{}, s.invitations...)
	declined := append([]uint32{}, s.declined...)
	if request.Method == 10 {
		declined = withoutPID(declined, pid)
		if !accepting {
			declined = append(declined, pid)
		}
	} else if request.Method == 5 || request.Method == 6 {
		index := s.memberClan(pid)
		if index < 0 {
			return clanStatus(clanNotMember)
		}
		clan := records[index]
		if !clanCanRecruit(clan, pid) {
			return clanStatus(clanPermission)
		}
		target, ok := s.accounts.Lookup(first)
		if !ok || target.PID < 1000 {
			return clanStatus(clanInvalidPlayer)
		}
		found := -1
		for i, inv := range invitations {
			if inv.Clan == clan.Abbreviation && inv.To == target.PID {
				found = i
				break
			}
		}
		if request.Method == 5 {
			if s.memberClan(target.PID) >= 0 {
				return clanStatus(clanAlreadyMember)
			}
			if !s.accepting(target.PID) {
				return clanStatus(clanNotAccepting)
			}
			if found >= 0 {
				if invitations[found].From == pid {
					return clanStatus(0)
				}
				return clanStatus(clanAlreadyInvited)
			}
			if len(clan.Members)+1 >= maxClanMembers {
				return clanStatus(clanFull)
			}
			if len(invitations) >= maxClanInvitations {
				return clanStatus(clanAlreadyInvited)
			}
			invitations = append(invitations, clanInvitation{Clan: clan.Abbreviation, From: pid, To: target.PID})
		} else {
			if found < 0 {
				return clanStatus(clanInvalidInvitation)
			}
			if invitations[found].From != pid && clan.FounderPID != pid {
				return clanStatus(clanPermission)
			}
			invitations = append(invitations[:found], invitations[found+1:]...)
		}
	} else {
		// Native actions identify an incoming invitation by clan abbreviation
		// and inviter name; the authenticated recipient supplies the target.
		found, clanIndex := -1, -1
		for i, inv := range invitations {
			from, _ := s.accounts.LookupPID(inv.From)
			if inv.To == pid && strings.EqualFold(inv.Clan, first) && strings.EqualFold(from.Name, second) {
				found = i
				for k, clan := range records {
					if clan.Abbreviation == inv.Clan {
						clanIndex = k
						break
					}
				}
				break
			}
		}
		if found < 0 || clanIndex < 0 {
			return clanStatus(clanInvalidInvitation)
		}
		switch request.Method {
		case 7:
			if s.memberClan(pid) >= 0 {
				return clanStatus(clanAlreadyMember)
			}
			if len(records[clanIndex].Members)+1 >= maxClanMembers {
				return clanStatus(clanFull)
			}
			records[clanIndex].Members = append(append([]uint32{}, records[clanIndex].Members...), pid)
			if len(records[clanIndex].Notices) > 0 {
				records[clanIndex].Unread = append(append([]uint32{}, records[clanIndex].Unread...), pid)
			}
			// Joining one clan invalidates all of this recipient's other offers.
			kept := []clanInvitation{}
			for _, inv := range invitations {
				if inv.To != pid {
					kept = append(kept, inv)
				}
			}
			invitations = kept
		case 8:
			invitations = append(invitations[:found], invitations[found+1:]...)
		case 9:
			invitations[found].Seen = true
		}
	}
	if err := saveState(s.path, ".clans-*", clanDisk{Version: 1, Clans: records, Invitations: invitations, Declined: declined}, maxClanStateBytes); err != nil {
		return nil, err
	}
	s.records, s.invitations, s.declined = records, invitations, declined
	return clanStatus(0)
}

func (s *clanStore) writeInvitation(w *quazal.Writer, inv clanInvitation) {
	var clan clanRecord
	for _, record := range s.records {
		if record.Abbreviation == inv.Clan {
			clan = record
			break
		}
	}
	from, _ := s.accounts.LookupPID(inv.From)
	to, _ := s.accounts.LookupPID(inv.To)
	// Native 0x173f10/0x173fa0: clan tag/name, inviter, rank,
	// member count, recipient, active-offer bool.
	w.String(clan.Abbreviation)
	w.String(clan.Name)
	w.String(from.Name)
	w.U32(0xffffffff)
	w.U32(uint32(len(clan.Members) + 1))
	w.String(to.Name)
	w.Bool(true)
}

func (s *clanStore) validateInvitationsAndMembers() error {
	memberOf := map[uint32]string{}
	byTag := map[string]clanRecord{}
	validPID := func(pid uint32) bool { _, ok := s.accounts.LookupPID(pid); return pid >= 1000 && ok }
	for _, clan := range s.records {
		if len(clan.Members)+1 > maxClanMembers {
			return errors.New("invalid Snowblind clan member count")
		}
		byTag[clan.Abbreviation] = clan
		for _, pid := range clanMembers(clan) {
			if !validPID(pid) || memberOf[pid] != "" {
				return errors.New("invalid Snowblind clan membership")
			}
			memberOf[pid] = clan.Abbreviation
		}
		officers := map[uint32]bool{}
		for _, pid := range clan.Officers {
			if pid == clan.FounderPID || !containsPID(clan.Members, pid) || officers[pid] {
				return errors.New("invalid Snowblind clan officer")
			}
			officers[pid] = true
		}
		seen := map[uint32]bool{}
		for _, pid := range clan.Unread {
			if !clanHasMember(clan, pid) || seen[pid] {
				return errors.New("invalid Snowblind notice recipient")
			}
			seen[pid] = true
		}
	}
	if len(s.invitations) > maxClanInvitations {
		return errors.New("invalid Snowblind clan invitation count")
	}
	seenInvites := map[struct {
		Tag string
		PID uint32
	}]bool{}
	for _, inv := range s.invitations {
		clan, ok := byTag[inv.Clan]
		key := struct {
			Tag string
			PID uint32
		}{inv.Clan, inv.To}
		if !ok || !clanHasMember(clan, inv.From) || !validPID(inv.To) || memberOf[inv.To] != "" || seenInvites[key] {
			return errors.New("invalid Snowblind clan invitation")
		}
		seenInvites[key] = true
	}
	seenPref := map[uint32]bool{}
	for _, pid := range s.declined {
		if !validPID(pid) || seenPref[pid] {
			return errors.New("invalid Snowblind invitation preference")
		}
		seenPref[pid] = true
	}
	return nil
}
