package ttl25

import (
	"sort"
	"strings"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

const maxClanMembers = 64 // replacement policy; retail server limit not recovered

func (c *community) appendClan(b []byte, clan clanRecord, rank uint32) []byte {
	rank = c.clanRank(clan.ID)
	b = rankingU32(b, clan.ID)
	b = rankingString(b, clan.Abbr)
	b = rankingString(b, clan.Name)
	b = rankingU32(b, rank)
	b = rankingU32(b, uint32(c.clanRating(clan)))
	b = rankingU32(b, 0)
	b = rankingU32(b, uint32(len(clan.Members)))
	return rankingString(b, c.accountName(clan.Leader))
}
func (c *community) appendMember(b []byte, viewer, pid uint32, clan clanRecord) []byte {
	b = rankingU32(b, pid)
	b = rankingString(b, c.accountName(pid))
	b = rankingString(b, clan.Abbr)
	b = append(b, boolReply(clan.Leader == pid && pid != 0)...)
	b = append(b, boolReply(clan.Members[pid])...)
	b = append(b, boolReply(c.acceptsInvites(&c.state, pid))...)
	rooms := c.sessions.forPlayer(pid)
	b = rankingU32(b, uint32(len(rooms)))
	for _, room := range rooms {
		b = rankingAny(b, room)
	}
	b = append(b, boolReply(c.state.Friends[friendship(viewer, pid)])...)
	_, online := c.presence(pid)
	return append(b, boolReply(online)...)
}
func (c *community) appendClanInvitation(b []byte, inv clanInvitation) []byte {
	clan := c.state.Clans[inv.Clan]
	b = rankingU32(b, clan.ID)
	b = rankingString(b, clan.Abbr)
	b = rankingString(b, clan.Name)
	b = rankingString(b, c.accountName(inv.From))
	b = rankingU32(b, c.clanRank(clan.ID))
	b = rankingU32(b, uint32(len(clan.Members)))
	b = rankingString(b, c.accountName(inv.To))
	return append(b, boolReply(inv.Read)...)
}
func (c *community) clans(pid uint32, request quazal.Request) ([]byte, error) {
	r := rankingReader{b: request.Args}
	s := &c.state
	clan, inClan := c.clanOf(s, pid)
	status := func(code uint32) []byte { return rankingU32(nil, code) }
	switch request.Method {
	case 17:
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		b := c.appendClan(status(0), clan, 0)
		if !inClan {
			return append(b, emptyClanMember()...), nil
		}
		return c.appendMember(b, pid, pid, clan), nil
	case 19:
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		ids := sortedPIDs(clan.Members)
		b := rankingU32(status(0), uint32(len(ids)))
		for _, id := range ids {
			b = c.appendMember(b, pid, id, clan)
		}
		return b, nil
	case 20:
		target := r.u32()
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		other, ok := c.clanOf(s, target)
		if !ok {
			return appendIDs(status(0), nil), nil
		}
		return appendIDs(status(0), []uint32{other.ID}), nil
	case 21:
		id := r.u32()
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		other, ok := s.Clans[id]
		code := uint32(0)
		if !ok {
			code = 4
		}
		return c.appendClan(status(code), other, 0), nil
	case 22:
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		incoming, outgoing := []clanInvitation{}, []clanInvitation{}
		for _, inv := range s.ClanInvites {
			if inv.To == pid {
				incoming = append(incoming, inv)
			}
			if inv.From == pid {
				outgoing = append(outgoing, inv)
			}
		}
		sortInvites := func(v []clanInvitation) {
			sort.Slice(v, func(i, j int) bool {
				if v[i].Clan != v[j].Clan {
					return v[i].Clan < v[j].Clan
				}
				return v[i].To < v[j].To
			})
		}
		sortInvites(incoming)
		sortInvites(outgoing)
		b := rankingU32(status(0), uint32(len(incoming)))
		for _, inv := range incoming {
			b = c.appendClanInvitation(b, inv)
		}
		b = rankingU32(b, uint32(len(outgoing)))
		for _, inv := range outgoing {
			b = c.appendClanInvitation(b, inv)
		}
		return b, nil
	case 35:
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		b := rankingU32(status(0), uint32(len(clan.Notices)))
		for _, notice := range clan.Notices {
			b = rankingString(b, notice.From)
			b = rankingString(b, notice.Body)
		}
		return b, nil
	case 37:
		count := r.u32()
		if !r.done() || count > 100 {
			return nil, quazal.ErrRMC
		}
		clans := c.rankedClans()
		if count < uint32(len(clans)) {
			clans = clans[:count]
		}
		b := rankingU32(nil, uint32(len(clans)))
		for i, clan := range clans {
			b = c.appendClan(b, clan, uint32(i+1))
		}
		return b, nil
	}
	var target, cid uint32
	var first, second string
	var flag bool
	switch request.Method {
	case 18:
		first = r.strLimit(64)
		second = r.strLimit(16)
	case 23, 24:
		first = r.str()
	case 25, 26, 27:
		cid = r.u32()
		first = r.str()
	case 28:
		flag = r.boolean()
	case 29, 31, 32, 33:
		target = r.u32()
	case 30, 36:
	case 34:
		first = r.strLimit(1024)
	default:
		return nil, quazal.ErrRMC
	}
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	return c.transaction(func(s *communityState) ([]byte, error) {
		clan, inClan := c.clanOf(s, pid)
		canManage := inClan && (clan.Leader == pid || clan.Members[pid])
		switch request.Method {
		case 18:
			if inClan {
				if strings.EqualFold(clan.Name, first) && strings.EqualFold(clan.Abbr, second) {
					return status(0), nil
				}
				return status(8), nil
			}
			if !acceptsText(first) || !acceptsText(second) {
				return status(14), nil
			}
			for _, other := range s.Clans {
				if strings.EqualFold(other.Abbr, second) {
					return status(6), nil
				}
				if strings.EqualFold(other.Name, first) {
					return status(7), nil
				}
			}
			if len(s.Clans) >= 10000 {
				return status(1), nil
			}
			id := s.NextClan
			s.NextClan++
			s.Clans[id] = clanRecord{ID: id, Name: first, Abbr: second, Leader: pid, Members: map[uint32]bool{pid: false}, Notices: []clanNotice{}}
		case 23, 24:
			if !canManage {
				return status(2), nil
			}
			account, ok := c.accounts.Lookup(first)
			if !ok {
				return status(5), nil
			}
			key := clanInviteKey(clan.ID, account.PID)
			if request.Method == 24 {
				delete(s.ClanInvites, key)
				return status(0), nil
			}
			if _, ok := clan.Members[account.PID]; ok {
				return status(15), nil
			}
			if _, exists := s.ClanInvites[key]; exists || s.ClanBlocks[key] {
				return status(9), nil
			}
			if !c.acceptsInvites(s, account.PID) {
				return status(10), nil
			}
			if len(clan.Members) >= maxClanMembers {
				return status(13), nil
			}
			s.ClanInvites[key] = clanInvitation{Clan: clan.ID, From: pid, To: account.PID}
		case 25, 26, 27:
			key := clanInviteKey(cid, pid)
			inv, exists := s.ClanInvites[key]
			if !exists || !strings.EqualFold(c.accountName(inv.From), first) {
				return status(11), nil
			}
			other, ok := s.Clans[cid]
			if !ok {
				return status(4), nil
			}
			if request.Method == 25 {
				if inClan {
					return status(8), nil
				}
				if len(other.Members) >= maxClanMembers {
					return status(13), nil
				}
				if _, ok := other.Members[inv.From]; !ok {
					return status(11), nil
				}
				other.Members[pid] = false
				s.Clans[cid] = other
			}
			delete(s.ClanInvites, key)
			if request.Method == 27 {
				s.ClanBlocks[key] = true
			}
		case 28:
			s.AcceptInvites[pid] = flag
		case 29:
			if !canManage {
				return status(2), nil
			}
			officer, exists := clan.Members[target]
			if !exists {
				return status(3), nil
			}
			if target == clan.Leader || officer && clan.Leader != pid {
				return status(2), nil
			}
			delete(clan.Members, target)
			s.Clans[clan.ID] = clan
			s.Kicked[target] = true
		case 30:
			if !inClan {
				return status(0), nil
			}
			if clan.Leader == pid && len(clan.Members) > 1 {
				return status(12), nil
			}
			delete(clan.Members, pid)
			if len(clan.Members) == 0 {
				delete(s.Clans, clan.ID)
				for key, inv := range s.ClanInvites {
					if inv.Clan == clan.ID {
						delete(s.ClanInvites, key)
					}
				}
			} else {
				s.Clans[clan.ID] = clan
			}
		case 31, 32, 33:
			if !inClan || clan.Leader != pid {
				return status(2), nil
			}
			officer, exists := clan.Members[target]
			if !exists {
				return status(3), nil
			}
			if target == pid {
				return status(2), nil
			}
			if request.Method == 33 {
				if !officer {
					return status(2), nil
				}
				clan.Members[pid] = true
				clan.Leader = target
				clan.Members[target] = false
			} else {
				clan.Members[target] = request.Method == 31
			}
			s.Clans[clan.ID] = clan
		case 34:
			if !canManage {
				return status(2), nil
			}
			if !acceptsText(first) {
				return status(14), nil
			}
			clan.Notices = append(clan.Notices, clanNotice{From: c.accountName(pid), Body: first})
			if len(clan.Notices) > 100 {
				clan.Notices = clan.Notices[len(clan.Notices)-100:]
			}
			s.Clans[clan.ID] = clan
		case 36:
			delete(s.Kicked, pid)
		}
		return status(0), nil
	})
}

func emptyClanMember() []byte {
	b := rankingU32(nil, 0)
	b = rankingString(b, "")
	b = rankingString(b, "")
	b = append(b, 0, 0, 0)
	b = rankingU32(b, 0)
	return append(b, 0, 0)
}
