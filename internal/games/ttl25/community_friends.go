package ttl25

import (
	"sort"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// 0x348930 maps these wire values to OSIPlayer flags; the UI filter at
// 0x165420 identifies +0xa0 as incoming, +0xa4 as outgoing and +0x9c as accepted.
const (
	friendIncoming byte = 0
	friendOutgoing byte = 1
	friendAccepted byte = 2
)

func (c *community) friends(pid uint32, request quazal.Request) ([]byte, error) {
	r := rankingReader{b: request.Args}
	if request.Method == 16 {
		r.strLimit(64)
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		targets := map[uint32]byte{}
		for key := range c.state.Friends {
			a, b, err := splitPair(key)
			if err != nil {
				continue
			}
			if a == pid {
				targets[b] = friendAccepted
			}
			if b == pid {
				targets[a] = friendAccepted
			}
		}
		for key := range c.state.Invites {
			a, b, err := splitPair(key)
			if err != nil {
				continue
			}
			if a == pid {
				targets[b] = friendOutgoing
			}
			if b == pid {
				targets[a] = friendIncoming
			}
		}
		ids := make([]uint32, 0, len(targets))
		for id := range targets {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		b := rankingU32(nil, uint32(len(ids)))
		for _, id := range ids {
			b = c.appendFriend(b, pid, id, targets[id])
		}
		return b, nil
	}
	var target uint32
	if request.Method == 11 {
		a, ok := c.accounts.Lookup(r.str())
		if ok {
			target = a.PID
		}
	} else {
		target = r.u32()
	}
	block := false
	if request.Method == 14 {
		block = r.boolean()
	}
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	if target == 0 || target == pid || c.accountName(target) == "" {
		return boolReply(false), nil
	}
	return c.transaction(func(s *communityState) ([]byte, error) {
		edge := friendship(pid, target)
		outgoing, incoming := directed(pid, target), directed(target, pid)
		switch request.Method {
		case 11, 12:
			if s.Blocks[outgoing] || s.Blocks[incoming] || s.Friends[edge] {
				return boolReply(false), nil
			}
			if s.Invites[incoming] {
				delete(s.Invites, incoming)
				s.Friends[edge] = true
			} else {
				s.Invites[outgoing] = true
			}
		case 13:
			if !s.Invites[incoming] && !s.Friends[edge] {
				return boolReply(false), nil
			}
			delete(s.Invites, incoming)
			delete(s.Invites, outgoing)
			s.Friends[edge] = true
		case 14:
			if !s.Invites[incoming] && !s.Blocks[outgoing] {
				return boolReply(false), nil
			}
			delete(s.Invites, incoming)
			if block {
				s.Blocks[outgoing] = true
			}
		case 15:
			delete(s.Friends, edge)
			delete(s.Invites, outgoing)
			delete(s.Invites, incoming)
			delete(s.Blocks, outgoing)
		}
		return boolReply(true), nil
	})
}

func (c *community) appendFriend(b []byte, viewer, pid uint32, relationship byte) []byte {
	b = rankingU32(b, pid)
	b = rankingString(b, c.accountName(pid))
	b = append(b, relationship)
	status, online := c.presence(pid)
	details := uint32(0)
	if online {
		details = 1
	}
	b = rankingU32(b, details)
	b = rankingString(b, status)
	abbr := ""
	if clan, ok := c.clanOf(&c.state, pid); ok {
		abbr = clan.Abbr
	}
	b = rankingString(b, abbr)
	rooms := c.sessions.forPlayer(pid)
	b = rankingU32(b, uint32(len(rooms)))
	for _, room := range rooms {
		b = rankingAny(b, room)
	}
	b = append(b, 0) // looking-to-play preference defaults false
	return b
}

func (c *community) alerts(pid uint32) []byte {
	friends := map[uint32]bool{}
	clans := map[uint32]bool{}
	for key := range c.state.Invites {
		from, to, err := splitPair(key)
		if err == nil && to == pid {
			friends[from] = true
		}
	}
	for _, inv := range c.state.ClanInvites {
		if inv.To == pid {
			clans[inv.From] = true
		}
	}
	b := appendIDs(nil, sortedPIDs(friends))
	b = appendIDs(b, sortedPIDs(clans))
	return appendIDs(b, c.sessions.invitations(pid))
}
