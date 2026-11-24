package snowblind

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"sync"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// Native 0x229a98 preserves these values; 0x1f7ed0 and the Friends UI use
// incoming invitations, outgoing requests and accepted friends respectively.
const (
	friendIncoming byte = iota
	friendOutgoing
	friendAccepted
	maxFriendsPerPlayer = 100
	maxFriendEdges      = 10000
	maxFriendStateBytes = 2 << 20
)

type friendEdge struct {
	From     uint32 `json:"from"`
	To       uint32 `json:"to"`
	Accepted bool   `json:"accepted"`
}

type friendBlock struct {
	From uint32 `json:"from"`
	To   uint32 `json:"to"`
}

type friendDisk struct {
	Version int           `json:"version"`
	Edges   []friendEdge  `json:"edges"`
	Blocks  []friendBlock `json:"blocks"`
}

type friendStore struct {
	mu       sync.Mutex
	path     string
	accounts *quazal.Store
	state    friendDisk
	online   func(uint32) bool
	clan     func(uint32) string
}

func openFriends(path string, accounts *quazal.Store) (*friendStore, error) {
	if path == "" || accounts == nil {
		return nil, errors.New("Snowblind friend path and account store are required")
	}
	s := &friendStore{path: path, accounts: accounts, state: friendDisk{Version: 1, Edges: []friendEdge{}, Blocks: []friendBlock{}}}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxFriendStateBytes {
		return nil, errors.New("invalid Snowblind friend state file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	s.state = friendDisk{}
	if err := d.Decode(&s.state); err != nil {
		return nil, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF || s.state.Version != 1 || s.state.Edges == nil || s.state.Blocks == nil || len(s.state.Edges) > maxFriendEdges || len(s.state.Blocks) > maxFriendEdges {
		return nil, errors.New("invalid Snowblind friend state")
	}
	validPair := func(a, b uint32) bool {
		_, first := accounts.LookupPID(a)
		_, second := accounts.LookupPID(b)
		return a >= 1000 && b >= 1000 && a != b && first && second
	}
	pairs, blocks := map[[2]uint32]bool{}, map[[2]uint32]bool{}
	counts := map[uint32]int{}
	for _, edge := range s.state.Edges {
		key := friendPair(edge.From, edge.To)
		if !validPair(edge.From, edge.To) || pairs[key] {
			return nil, errors.New("invalid Snowblind friend edge")
		}
		pairs[key] = true
		counts[edge.From]++
		counts[edge.To]++
		if counts[edge.From] > maxFriendsPerPlayer || counts[edge.To] > maxFriendsPerPlayer {
			return nil, errors.New("Snowblind friend limit exceeded")
		}
	}
	for _, block := range s.state.Blocks {
		key := [2]uint32{block.From, block.To}
		if !validPair(block.From, block.To) || blocks[key] || pairs[friendPair(block.From, block.To)] {
			return nil, errors.New("invalid Snowblind friend block")
		}
		blocks[key] = true
	}
	return s, nil
}

func friendPair(a, b uint32) [2]uint32 {
	if a > b {
		a, b = b, a
	}
	return [2]uint32{a, b}
}

func (s *friendStore) handle(pid uint32, request quazal.Request) ([]byte, error) {
	if _, ok := s.accounts.LookupPID(pid); pid < 1000 || !ok || request.Protocol != 103 {
		return nil, quazal.ErrRMC
	}
	r := quazal.NewReader(request.Args)
	var target uint32
	var block bool
	if request.Method == 5 {
		r.String(2048) // Locale/presence query String; never executed as SQL.
	} else if request.Method == 1 {
		account, ok := s.accounts.Lookup(r.String(255))
		if ok {
			target = account.PID
		}
	} else if request.Method >= 2 && request.Method <= 4 {
		target = r.U32()
		if request.Method == 3 {
			block = r.Bool()
		}
	} else {
		return nil, quazal.ErrRMC
	}
	if !r.Done() {
		return nil, quazal.ErrRMC
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.Method == 5 {
		return s.query(pid)
	}
	if _, ok := s.accounts.LookupPID(target); !ok || target < 1000 || target == pid {
		return boolean(false)
	}
	next := friendDisk{Version: 1, Edges: append([]friendEdge{}, s.state.Edges...), Blocks: append([]friendBlock{}, s.state.Blocks...)}
	index, fromCount, toCount := -1, 0, 0
	for i, edge := range next.Edges {
		if friendPair(edge.From, edge.To) == friendPair(pid, target) {
			index = i
		}
		if edge.From == pid || edge.To == pid {
			fromCount++
		}
		if edge.From == target || edge.To == target {
			toCount++
		}
	}
	switch request.Method {
	case 1: // FriendInvite: neither a new invite nor a reciprocal invite is acceptance.
		for _, blocked := range next.Blocks {
			if friendPair(blocked.From, blocked.To) == friendPair(pid, target) {
				return boolean(false)
			}
		}
		if index >= 0 {
			return boolean(true) // Idempotent retry; never overwrites a pending direction.
		}
		if fromCount >= maxFriendsPerPlayer || toCount >= maxFriendsPerPlayer || len(next.Edges) >= maxFriendEdges {
			return boolean(false)
		}
		next.Edges = append(next.Edges, friendEdge{From: pid, To: target})
	case 2: // FriendAccept: only the recipient may accept an invitation.
		if index < 0 {
			return boolean(false)
		}
		if !next.Edges[index].Accepted && next.Edges[index].To != pid {
			return boolean(false)
		}
		next.Edges[index].Accepted = true
	case 3: // FriendReject(pid, blockFutureRequests).
		if index < 0 || next.Edges[index].Accepted || next.Edges[index].To != pid {
			return boolean(false)
		}
		next.Edges = append(next.Edges[:index], next.Edges[index+1:]...)
		if block {
			if len(next.Blocks) >= maxFriendEdges {
				return boolean(false)
			}
			next.Blocks = append(next.Blocks, friendBlock{From: pid, To: target})
		}
	case 4: // FriendDelete: accepted friendship or the sender's pending request.
		if index < 0 {
			return boolean(true)
		}
		if !next.Edges[index].Accepted && next.Edges[index].From != pid {
			return boolean(false)
		}
		next.Edges = append(next.Edges[:index], next.Edges[index+1:]...)
	}
	if err := saveState(s.path, ".friends-*", next, maxFriendStateBytes); err != nil {
		return nil, err
	}
	s.state = next
	return boolean(true)
}

func (s *friendStore) query(pid uint32) ([]byte, error) {
	types := map[uint32]byte{}
	for _, edge := range s.state.Edges {
		if edge.From != pid && edge.To != pid {
			continue
		}
		target, relation := edge.From, friendIncoming
		if edge.From == pid {
			target, relation = edge.To, friendOutgoing
		}
		if edge.Accepted {
			relation = friendAccepted
		}
		types[target] = relation
	}
	ids := make([]uint32, 0, len(types))
	for id := range types {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var w quazal.Writer
	w.U32(uint32(len(ids)))
	for _, id := range ids {
		account, _ := s.accounts.LookupPID(id)
		w.U32(id)
		w.String(account.Name)
		w.U8(types[id])
		presence, clan := "", ""
		if s.online != nil && s.online(id) {
			// 0x229a98 parses three pipe-separated metadata fields and marks
			// details/online available. Keep unspecified profile fields empty.
			presence = "||"
		}
		if s.clan != nil {
			clan = s.clan(id)
		}
		w.String(presence)
		w.String(clan)
		w.U32(0) // No gathering attachment yet; native row has no trailing bool.
	}
	return w.Bytes()
}
