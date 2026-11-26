package rsvpsp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// FriendsProtocol is the older ten-method Friends protocol (client class
// 0x08bd9a3c). The request stubs 0x08bdae94…0x08bdbe78 give the arguments;
// the server-side stubs in 0x08bdc03c give the replies: methods 1–8 return a
// bool, 9 List<u32> and 10 List<FriendData>.
const FriendsProtocol = 20

// eventFriend is the friend notification (14/1). NotificationManager
// 0x08bac9d8 handles every type with type/1000 == 1: param1 is the other
// player's PID and param2 the new relationship, shown as FriendshipRequest
// (2), AcceptFriendship (1) or DeclineFriendship (0, "Friendship declined or
// deleted").
const (
	eventFriend       = 1000
	friendDeclined    = 0
	friendAccepted    = 1
	friendRequested   = 2
	maxRelationships  = 100
	maxFriendName     = 64
	maxFriendMessage  = 256
	maxFriendsStorage = 4 << 20
)

// friendState is what friends.json holds. Friendships are keyed "low:high"
// and requests "from:to".
type friendState struct {
	Friends  map[string]bool `json:"friends"`
	Requests map[string]bool `json:"requests"`
}

// social keeps friendships and friend requests. Online status, the games
// players are in and notifications come from the hosted-games registry.
type social struct {
	mu     sync.Mutex
	path   string
	state  friendState
	games  *games
	lookup func(name string) (uint32, bool)
	rating func(pid uint32) float32 // for the friends list; nil means 0
}

func openSocial(path string, g *games, lookup func(string) (uint32, bool)) (*social, error) {
	s := &social{path: path, games: g, lookup: lookup,
		state: friendState{Friends: map[string]bool{}, Requests: map[string]bool{}}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.state); err != nil {
		return nil, fmt.Errorf("rsvpsp: friends state: %w", err)
	}
	if s.state.Friends == nil || s.state.Requests == nil {
		return nil, errors.New("rsvpsp: friends state: missing maps")
	}
	for key := range s.state.Friends {
		if a, b, err := splitPair(key); err != nil || a > b {
			return nil, errors.New("rsvpsp: friends state: invalid friendship")
		}
	}
	for key := range s.state.Requests {
		if _, _, err := splitPair(key); err != nil {
			return nil, errors.New("rsvpsp: friends state: invalid request")
		}
	}
	return s, nil
}

func directed(a, b uint32) string { return fmt.Sprintf("%d:%d", a, b) }

func pair(a, b uint32) string {
	if a > b {
		a, b = b, a
	}
	return directed(a, b)
}

func splitPair(key string) (uint32, uint32, error) {
	var a, b uint32
	if n, err := fmt.Sscanf(key, "%d:%d", &a, &b); err != nil || n != 2 || a == 0 || b == 0 || a == b || directed(a, b) != key {
		return 0, 0, errors.New("invalid relationship")
	}
	return a, b, nil
}

func boolReply(v bool) []byte {
	if v {
		return []byte{1}
	}
	return []byte{0}
}

// relation is one player's view of another.
type relation struct{ friend, incoming, outgoing bool }

func (s *social) relationLocked(pid, other uint32) relation {
	return relation{friend: s.state.Friends[pair(pid, other)],
		incoming: s.state.Requests[directed(other, pid)],
		outgoing: s.state.Requests[directed(pid, other)]}
}

// relatedLocked lists everyone pid has a friendship or a request with.
func (s *social) relatedLocked(pid uint32) map[uint32]relation {
	out := map[uint32]relation{}
	for key := range s.state.Friends {
		a, b, _ := splitPair(key)
		if a == pid || b == pid {
			out[a^b^pid] = s.relationLocked(pid, a^b^pid)
		}
	}
	for key := range s.state.Requests {
		a, b, _ := splitPair(key)
		if a == pid || b == pid {
			out[a^b^pid] = s.relationLocked(pid, a^b^pid)
		}
	}
	return out
}

// friends handles protocol 20. Blacklists (5, 6) and the plain lists (9, 10)
// have no callers in the menus; they are refused like other unknown calls.
func (s *social) friends(pid uint32, request quazal.Request) ([]byte, bool, error) {
	r := reader{b: request.Args}
	var target uint32
	switch request.Method {
	case 1: // AddFriend(u32 PID, u32 details, String message)
		target = r.u32()
		r.u32()
		r.str(maxFriendMessage)
	case 2: // AddFriendByName(String name, u32 details, String message)
		name := r.str(maxFriendName)
		r.u32()
		r.str(maxFriendMessage)
		if r.err == nil {
			target, _ = s.lookup(name)
		}
	case 3, 4, 7: // AcceptFriendship, DeclineFriendship, ClearRelationship(u32 PID)
		target = r.u32()
	case 8: // UpdateDetails(u32 PID, u32 details)
		target = r.u32()
		r.u32()
	default:
		return nil, false, nil
	}
	if !r.done() {
		return nil, true, quazal.ErrRMC
	}
	if target == 0 || target == pid || s.games.name(target) == "" {
		s.games.log.Info("rsvpsp friend request refused", "method", request.Method, "pid", pid, "target", target)
		return boolReply(false), true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := friendState{Friends: maps(s.state.Friends), Requests: maps(s.state.Requests)}
	rel := s.relationLocked(pid, target)
	ok, event := true, -1
	switch request.Method {
	case 1, 2:
		switch {
		case rel.friend, rel.outgoing:
		case rel.incoming:
			// Both asked: that is an acceptance.
			delete(next.Requests, directed(target, pid))
			next.Friends[pair(pid, target)] = true
			event = friendAccepted
		case len(s.relatedLocked(pid)) >= maxRelationships || len(s.relatedLocked(target)) >= maxRelationships:
			ok = false
		default:
			next.Requests[directed(pid, target)] = true
			event = friendRequested
		}
	case 3:
		ok = rel.incoming
		if ok {
			delete(next.Requests, directed(target, pid))
			next.Friends[pair(pid, target)] = true
			event = friendAccepted
		}
	case 4:
		ok = rel.incoming
		if ok {
			delete(next.Requests, directed(target, pid))
			event = friendDeclined
		}
	case 7:
		ok = rel.friend || rel.incoming || rel.outgoing
		if ok {
			delete(next.Friends, pair(pid, target))
			delete(next.Requests, directed(pid, target))
			delete(next.Requests, directed(target, pid))
			event = friendDeclined
		}
	case 8:
		ok = rel.friend
	}
	if !equalState(s.state, next) {
		if err := s.persist(next); err != nil {
			return nil, true, err
		}
		s.state = next
	}
	if event >= 0 {
		s.games.notifyPlayer(target, pid, eventFriend, pid, uint32(event), s.games.name(pid))
	}
	s.games.log.Info("rsvpsp friends", "method", request.Method, "pid", pid, "target", target, "ok", ok, "event", event)
	return boolReply(ok), true, nil
}

// list is Spark 60/10: List of (u32 PID, String name, String status,
// bool friend, bool incoming, bool outgoing, float rating, String playing),
// serialized by 0x08bc26b8. FriendsState (0x08bb75a0) shows "Online" status
// as online, gives rows with either pending flag their own icon and no rating
// or game, and shows the playing String after its ")" (a game description
// such as "(1)TRIS"). The two pending flags are taken in the order the
// player-actions reply uses (friend, incoming, outgoing).
func (s *social) list(pid uint32, args []byte) ([]byte, error) {
	if len(args) != 0 {
		return nil, quazal.ErrRMC
	}
	s.mu.Lock()
	related := s.relatedLocked(pid)
	s.mu.Unlock()
	ids := make([]uint32, 0, len(related))
	for id := range related {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	b := putU32(nil, uint32(len(ids)))
	for _, id := range ids {
		rel := related[id]
		status := "Offline"
		if s.games.online(id) {
			status = "Online"
		}
		playing := ""
		if rel.friend {
			playing, _, _ = s.games.gameOf(id)
		}
		b = putU32(b, id)
		b = putString(b, s.games.name(id))
		b = putString(b, status)
		b = append(b, boolByte(rel.friend), boolByte(rel.incoming), boolByte(rel.outgoing))
		rating := float32(0)
		if s.rating != nil {
			rating = s.rating(id)
		}
		b = putU32(b, math.Float32bits(rating))
		b = putString(b, playing)
	}
	return b, nil
}

// friendsOf lists pid's accepted friends.
func (s *social) friendsOf(pid uint32) []uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []uint32
	for other, rel := range s.relatedLocked(pid) {
		if rel.friend {
			out = append(out, other)
		}
	}
	slices.Sort(out)
	return out
}

// info is Spark 60/11 (u32 PID), for the player-actions menu. The reply
// (0x08bc2a50) is bool friend, bool incoming request, bool outgoing request,
// bool game invitation, u32 game ID, String, bool joinable. PlayerActionsState
// offers RequestFriendship unless friend (0x08bbd9c0), Accept/Deny with an
// incoming request (0x08bbdad0, 0x08bbdbb8), Delete for a friend or an
// outgoing request (0x08bbdca0), Accept/Decline game invite with an invitation
// (0x08bbdd94, 0x08bbde94), and JoinGame when the game ID is set and the game
// is joinable (0x08bbdfbc). The String is not read there.
func (s *social) info(pid uint32, args []byte) ([]byte, error) {
	r := reader{b: args}
	target := r.u32()
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	s.mu.Lock()
	rel := s.relationLocked(pid, target)
	s.mu.Unlock()
	_, id, joinable := s.games.gameOf(target)
	b := []byte{boolByte(rel.friend), boolByte(rel.incoming), boolByte(rel.outgoing), 0}
	b = putU32(b, id)
	b = putString(b, "")
	return append(b, boolByte(joinable && target != pid)), nil
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

func maps(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func equalState(a, b friendState) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// persist writes friends.json atomically.
func (s *social) persist(state friendState) error {
	return writeJSON(s.path, state, maxFriendsStorage)
}

// writeJSON replaces path with v as JSON through a synced temporary file.
func writeJSON(path string, v any, limit int) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > limit {
		return fmt.Errorf("rsvpsp: %s capacity exceeded", filepath.Base(path))
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Helpers on the games registry used by social.

// online reports whether pid has a live registration.
func (g *games) online(pid uint32) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.peers[pid]
	return ok
}

// gameOf returns the description and ID of the game pid is in, and whether
// another player could join it (not full; games in play can be joined).
func (g *games) gameOf(pid uint32) (string, uint32, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if m := g.matchOfLocked(pid); m != nil {
		return description(m.record), m.id, len(m.players) < maxPlayers
	}
	return "", 0, false
}

// notifyPlayer queues a 14/1 notification to one player if they are online.
func (g *games) notifyPlayer(pid, source, kind, param1, param2 uint32, text string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.notifyPlayerLocked(pid, source, kind, param1, param2, text)
}

func (g *games) notifyPlayerLocked(pid, source, kind, param1, param2 uint32, text string) {
	peer, live := g.peers[pid]
	if !live || len(g.calls) >= maxCalls {
		return
	}
	args := putU32(nil, source)
	args = putU32(args, kind)
	args = putU32(args, param1)
	args = putU32(args, param2)
	args = putString(args, text)
	g.calls = append(g.calls, quazal.ServerCall{Peer: peer, Protocol: 14, Method: 1, Args: args})
}
