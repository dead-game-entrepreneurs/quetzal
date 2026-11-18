package ttl25

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

type clanRecord struct {
	ID      uint32          `json:"id"`
	Name    string          `json:"name"`
	Abbr    string          `json:"abbr"`
	Leader  uint32          `json:"leader"`
	Members map[uint32]bool `json:"members"` // bool is officer, leader tracked separately
	Notices []clanNotice    `json:"notices"`
}
type clanNotice struct{ From, Body string }
type clanInvitation struct {
	Clan, From, To uint32
	Read           bool
}
type playerStatistics struct {
	Values  []float32 `json:"values"`
	Updated int64     `json:"updated"`
}
type communityState struct {
	Version       int                         `json:"version"`
	NextClan      uint32                      `json:"next_clan"`
	Friends       map[string]bool             `json:"friends"`
	Invites       map[string]bool             `json:"friend_invites"`
	Blocks        map[string]bool             `json:"blocks"`
	Clans         map[uint32]clanRecord       `json:"clans"`
	ClanInvites   map[string]clanInvitation   `json:"clan_invites"`
	ClanBlocks    map[string]bool             `json:"clan_blocks"`
	AcceptInvites map[uint32]bool             `json:"accept_invites"`
	Kicked        map[uint32]bool             `json:"kicked"`
	Stats         map[uint32]playerStatistics `json:"statistics"`
	Reports       map[string]string           `json:"report_digests"`
	Competitors   map[uint32]bool             `json:"ranking_participants"`
}
type community struct {
	mu       sync.Mutex
	path     string
	state    communityState
	accounts *quazal.Store
	sessions *sessionRegistry
	now      func() time.Time
	online   map[sessionOwner]string
}

func initialCommunity() communityState {
	return communityState{Version: 1, NextClan: 1, Friends: map[string]bool{}, Invites: map[string]bool{}, Blocks: map[string]bool{}, Clans: map[uint32]clanRecord{}, ClanInvites: map[string]clanInvitation{}, ClanBlocks: map[string]bool{}, AcceptInvites: map[uint32]bool{}, Kicked: map[uint32]bool{}, Stats: map[uint32]playerStatistics{}, Reports: map[string]string{}, Competitors: map[uint32]bool{}}
}
func openCommunity(path string, accounts *quazal.Store, sessions *sessionRegistry) (*community, error) {
	c := &community{path: path, accounts: accounts, sessions: sessions, now: time.Now, state: initialCommunity(), online: make(map[sessionOwner]string)}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := c.persist(c.state); err != nil {
			return nil, err
		}
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16<<20 {
		return nil, errors.New("invalid community state file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, (16<<20)+1))
	d.DisallowUnknownFields()
	c.state = communityState{}
	if err := d.Decode(&c.state); err != nil {
		return nil, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF || c.state.Version != 1 || c.state.NextClan == 0 {
		return nil, errors.New("invalid community state")
	}
	if err := validateCommunity(c.state); err != nil {
		return nil, err
	}
	return c, nil
}
func validateCommunity(s communityState) error {
	if s.Friends == nil || s.Invites == nil || s.Blocks == nil || s.Clans == nil || s.ClanInvites == nil || s.ClanBlocks == nil || s.AcceptInvites == nil || s.Kicked == nil || s.Stats == nil || s.Reports == nil || s.Competitors == nil {
		return errors.New("missing community maps")
	}
	members := map[uint32]bool{}
	for id, clan := range s.Clans {
		if id == 0 || id >= s.NextClan || clan.ID != id || clan.Members == nil || !acceptsText(clan.Name) || !acceptsText(clan.Abbr) {
			return errors.New("invalid stored clan")
		}
		if _, ok := clan.Members[clan.Leader]; !ok {
			return errors.New("missing clan leader")
		}
		for pid := range clan.Members {
			if pid < 1000 || members[pid] {
				return errors.New("invalid clan membership")
			}
			members[pid] = true
		}
	}
	for pid, stats := range s.Stats {
		if pid < 1000 || len(stats.Values) != 102 || !finiteValues(stats.Values) {
			return errors.New("invalid stored statistics")
		}
	}
	return nil
}
func (c *community) persist(s communityState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(b) > 16<<20 {
		return errors.New("community state capacity exceeded")
	}
	f, err := os.CreateTemp(filepath.Dir(c.path), ".community-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
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
	if err := os.Rename(f.Name(), c.path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(c.path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
func (c *community) transaction(fn func(*communityState) ([]byte, error)) ([]byte, error) {
	b, err := json.Marshal(c.state)
	if err != nil {
		return nil, err
	}
	var next communityState
	if err := json.Unmarshal(b, &next); err != nil {
		return nil, err
	}
	out, err := fn(&next)
	if err != nil {
		return nil, err
	}
	after, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(b, after) {
		if err := c.persist(next); err != nil {
			return nil, err
		}
		c.state = next
	}
	return out, nil
}
func directed(a, b uint32) string { return fmt.Sprintf("%d:%d", a, b) }
func friendship(a, b uint32) string {
	if a > b {
		a, b = b, a
	}
	return directed(a, b)
}
func splitPair(key string) (uint32, uint32, error) {
	var a, b uint32
	n, err := fmt.Sscanf(key, "%d:%d", &a, &b)
	if err != nil || n != 2 || a < 1000 || b < 1000 || a == b {
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
func (r *rankingReader) boolean() bool {
	b := r.take(1)
	if b == nil {
		return false
	}
	if b[0] > 1 {
		r.err = quazal.ErrRMC
	}
	return b[0] != 0
}
func (c *community) accountName(pid uint32) string {
	a, ok := c.accounts.LookupPID(pid)
	if !ok {
		return ""
	}
	return a.Name
}
func (c *community) clanOf(s *communityState, pid uint32) (clanRecord, bool) {
	for _, clan := range s.Clans {
		if _, ok := clan.Members[pid]; ok {
			return clan, true
		}
	}
	return clanRecord{}, false
}
func (c *community) acceptsInvites(s *communityState, pid uint32) bool {
	v, exists := s.AcceptInvites[pid]
	return !exists || v
}
func sortedPIDs(m map[uint32]bool) []uint32 {
	ids := make([]uint32, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
func appendIDs(b []byte, ids []uint32) []byte {
	b = rankingU32(b, uint32(len(ids)))
	for _, id := range ids {
		b = rankingU32(b, id)
	}
	return b
}
func reportDigest(values []float32) string {
	sum := sha256.Sum256(floatBytes(values))
	return hex.EncodeToString(sum[:])
}
func clanInviteKey(cid, pid uint32) string {
	return strconv.FormatUint(uint64(cid), 10) + ":" + strconv.FormatUint(uint64(pid), 10)
}

func (c *community) handle(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
	if request.Protocol != 101 {
		return nil, errors.New("unsupported community protocol")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.online[ownerOf(peer, pid)]; !ok {
		c.online[ownerOf(peer, pid)] = "Online"
	}
	switch {
	case request.Method == 2:
		return c.sessions.searchFor(peer, pid, request)
	case request.Method == 1 || request.Method == 3:
		return c.findRooms(request)
	case request.Method >= 11 && request.Method <= 16:
		return c.friends(pid, request)
	case request.Method >= 17 && request.Method <= 37:
		return c.clans(pid, request)
	case request.Method >= 4 && request.Method <= 9:
		return c.statistics(peer, pid, request)
	case request.Method == 39:
		return c.accountInfo(pid, request.Args)
	default:
		return socialBootstrap(request)
	}
}

func (c *community) status(peer prudp.Peer, pid uint32, status string) {
	c.mu.Lock()
	c.online[ownerOf(peer, pid)] = status
	c.mu.Unlock()
}
func (c *community) disconnect(peer prudp.Peer, pid uint32) {
	c.mu.Lock()
	delete(c.online, ownerOf(peer, pid))
	c.mu.Unlock()
}
func (c *community) presence(pid uint32) (string, bool) {
	for owner, status := range c.online {
		if owner.pid == pid {
			return status, true
		}
	}
	return "", false
}

func (c *community) competition(pid uint32, args []byte) ([]byte, error) {
	r := rankingReader{b: args}
	gid := r.u32()
	if !r.done() {
		return nil, quazal.ErrRMC
	}
	if gid != 1 {
		return boolReply(false), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transaction(func(s *communityState) ([]byte, error) { s.Competitors[pid] = true; return boolReply(true), nil })
}
