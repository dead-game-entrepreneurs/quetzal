package snowblind

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func clanFixture(t *testing.T) (*startup, []quazal.Account, string) {
	t.Helper()
	dir := t.TempDir()
	accounts, err := quazal.OpenStore(filepath.Join(dir, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	players := []quazal.Account{}
	for _, name := range []string{"Founder", "Second", "Third"} {
		a, err := accounts.Create(name, quazalcrypto.Key{1})
		if err != nil {
			t.Fatal(err)
		}
		players = append(players, a)
	}
	path := filepath.Join(dir, "clans.json")
	clans, err := openClans(path, accounts)
	if err != nil {
		t.Fatal(err)
	}
	return &startup{accounts: accounts, clans: clans}, players, path
}

func clanCreateArgs(abbr, name string) []byte {
	return encode(func(w *quazal.Writer) { w.String(abbr); w.String(name); w.U32(0xffffffff); w.U32(0) })
}

func clanCall(t *testing.T, s *startup, pid, method uint32, args []byte) []byte {
	t.Helper()
	b, err := s.handle(prudp.Peer{}, pid, quazal.Request{Protocol: 102, Method: method, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func clanWantStatus(t *testing.T, b []byte, want uint32) {
	t.Helper()
	r := quazal.NewReader(b)
	if r.U32() != want || !r.Done() {
		t.Fatalf("clan status %x, want %x", b, want)
	}
}

func TestClanCreationPersistsAndReturnsCompleteFounderMembership(t *testing.T) {
	s, players, path := clanFixture(t)
	pid := players[0].PID
	clanWantStatus(t, clanCall(t, s, pid, 3, clanCreateArgs("TAG", "Synthetic Clan")), 0)
	first := clanCall(t, s, pid, 2, nil)
	fixture, err := os.ReadFile("testdata/clans-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Create string `json:"create_args_hex"`
		Fetch  string `json:"fetch_my_clan_reply_hex"`
	}
	if err := json.Unmarshal(fixture, &native); err != nil {
		t.Fatal(err)
	}
	create, err := hex.DecodeString(native.Create)
	if err != nil || !bytes.Equal(create, clanCreateArgs("TAG", "Synthetic Clan")) {
		t.Fatal("creation differs from native ClanDesc writer")
	}
	fetch, err := hex.DecodeString(native.Fetch)
	if err != nil || !bytes.Equal(fetch, first) {
		t.Fatal("membership differs from native descriptor/alert/member codecs")
	}
	r := quazal.NewReader(first)
	if r.U32() != 0 || r.String(3) != "TAG" || r.String(20) != "Synthetic Clan" || r.U32() != 0xffffffff || r.U32() != 0 {
		t.Fatal("clan descriptor")
	}
	if r.Bool() || r.Bool() || r.Bool() || r.String(255) != "" || r.String(255) != "" || r.U32() != 1 {
		t.Fatal("clan alerts and roster count")
	}
	for range 2 { // Roster founder and own-member descriptor are both required.
		if r.String(15) != players[0].Name || r.String(255) != "" || !r.Bool() || r.Bool() || !r.Bool() || r.U32() != 0 || r.Bool() {
			t.Fatal("founder member record")
		}
	}
	if !r.Done() {
		t.Fatal("trailing clan fields")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("clan state is not private", err)
	}
	loaded, err := openClans(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.clans = loaded
	if !bytes.Equal(first, clanCall(t, s, pid, 2, nil)) {
		t.Fatal("membership did not survive reload")
	}
	// Player-stat labels must agree with the newly persisted clan membership.
	b, err := s.handle(prudp.Peer{}, pid, quazal.Request{Protocol: 101, Method: 2, Args: statsArgs(0, players[0].Name, 15)})
	if err != nil {
		t.Fatal(err)
	}
	r = quazal.NewReader(b)
	if r.U32() != 1 || r.String(15) != players[0].Name || r.String(3) != "TAG" {
		t.Fatal("missing clan abbreviation in player stats")
	}
	r = quazal.NewReader(clanCall(t, s, players[1].PID, 2, nil))
	if r.U32() != clanNotMember {
		t.Fatal("foreign account gained membership")
	}
}

func TestClanCreationRetriesUniquenessAndIdentity(t *testing.T) {
	s, players, path := clanFixture(t)
	clanWantStatus(t, clanCall(t, s, players[0].PID, 3, clanCreateArgs("TAG", "Synthetic Clan")), 0)
	before, _ := os.ReadFile(path)
	clanWantStatus(t, clanCall(t, s, players[0].PID, 3, clanCreateArgs("tag", "synthetic clan")), 0)
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || len(s.clans.records) != 1 {
		t.Fatal("retry mutated clan state")
	}
	clanWantStatus(t, clanCall(t, s, players[0].PID, 3, clanCreateArgs("NEW", "Different")), clanAlreadyMember)
	clanWantStatus(t, clanCall(t, s, players[1].PID, 3, clanCreateArgs("tag", "Different")), clanAbbreviationTaken)
	clanWantStatus(t, clanCall(t, s, players[1].PID, 3, clanCreateArgs("NEW", "SYNTHETIC CLAN")), clanNameTaken)
	for _, pid := range []uint32{quazal.GuestPID, 999999} {
		if _, err := s.handle(prudp.Peer{}, pid, quazal.Request{Protocol: 102, Method: 3, Args: clanCreateArgs("NEW", "Different")}); err == nil {
			t.Fatal("unknown account created a clan")
		}
	}
}

func TestClanPresenceFollowsSecureRegistrationAndExactDisconnect(t *testing.T) {
	s, players, path := clanFixture(t)
	pid := players[0].PID
	clanWantStatus(t, clanCall(t, s, pid, 3, clanCreateArgs("TAG", "Synthetic Clan")), 0)
	registry, peers, urls := sessionFixture(t)
	s.clans.online = registry.online
	registry.disconnect(peers[0], pid)
	fixture, err := os.ReadFile("testdata/clans-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Offline string `json:"fetch_my_clan_reply_hex"`
		Online  string `json:"online_fetch_my_clan_reply_hex"`
	}
	if err := json.Unmarshal(fixture, &native); err != nil {
		t.Fatal(err)
	}
	assertPresence := func(online bool) {
		t.Helper()
		wantHex := native.Offline
		if online {
			wantHex = native.Online
		}
		want, err := hex.DecodeString(wantHex)
		if err != nil || !bytes.Equal(clanCall(t, s, pid, 2, nil), want) {
			t.Fatalf("clan presence online=%v differs from native member codecs", online)
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertPresence(false) // Another online account must not mark the founder online.
	registry.registered(peers[0], pid, urls[0])
	registry.registered(peers[0], pid, urls[0]) // Duplicate registration remains idempotent.
	assertPresence(true)
	fresh := peers[0]
	fresh.ID += 100
	registry.registered(fresh, pid, urls[0])
	registry.disconnect(peers[0], pid)
	assertPresence(true) // A delayed old-generation disconnect cannot erase a new login.
	registry.disconnect(fresh, players[1].PID)
	assertPresence(true) // Nor can a disconnect attributed to another PID.
	registry.disconnect(fresh, pid)
	assertPresence(false)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("transient online state changed persistent clan membership")
	}
}

func TestClanCreationRejectsMalformedInputAndRollsBackFailedWrite(t *testing.T) {
	s, players, path := clanFixture(t)
	args := clanCreateArgs("TAG", "Synthetic Clan")
	for n := range len(args) {
		if _, err := s.clans.handle(players[0].PID, quazal.Request{Protocol: 102, Method: 3, Args: args[:n]}); err == nil {
			t.Fatalf("accepted truncated creation %d", n)
		}
	}
	for _, bad := range [][]byte{append(bytes.Clone(args), 0), encode(func(w *quazal.Writer) { w.String("TAG"); w.String("Clan"); w.U32(0); w.U32(1) })} {
		if _, err := s.clans.handle(players[0].PID, quazal.Request{Protocol: 102, Method: 3, Args: bad}); err == nil {
			t.Fatal("accepted malformed clan creation")
		}
	}
	for _, pair := range [][2]string{{"LONG", "Clan"}, {"", "Clan"}, {"TAG", ""}, {"TAG", "Name\n"}, {"TAG", "123456789012345678901"}} {
		clanWantStatus(t, clanCall(t, s, players[0].PID, 3, clanCreateArgs(pair[0], pair[1])), clanIllegalText)
	}
	// A failed commit must not make a clan visible in memory or claim success.
	s.clans.path = filepath.Join(filepath.Dir(path), "missing", "clans.json")
	if _, err := s.clans.handle(players[0].PID, quazal.Request{Protocol: 102, Method: 3, Args: args}); err == nil || len(s.clans.records) != 0 {
		t.Fatal("failed persistence awarded membership")
	}
	s.clans.path = path
	clanWantStatus(t, clanCall(t, s, players[0].PID, 3, args), 0)
}

func TestConcurrentClanNameCollisionHasOneWinner(t *testing.T) {
	s, players, _ := clanFixture(t)
	var wg sync.WaitGroup
	results := make(chan uint32, 2)
	for _, player := range players[:2] {
		wg.Add(1)
		go func(pid uint32) {
			defer wg.Done()
			b, err := s.clans.handle(pid, quazal.Request{Protocol: 102, Method: 3, Args: clanCreateArgs("TAG", "Synthetic Clan")})
			if err != nil {
				t.Error(err)
				return
			}
			results <- quazal.NewReader(b).U32()
		}(player.PID)
	}
	wg.Wait()
	close(results)
	counts := map[uint32]int{}
	for result := range results {
		counts[result]++
	}
	if counts[0] != 1 || counts[clanAbbreviationTaken] != 1 || len(s.clans.records) != 1 {
		t.Fatal("clan collision race", counts)
	}
}

func TestClanStateRejectsCorruptionAndInsecurePermissions(t *testing.T) {
	s, players, path := clanFixture(t)
	clanWantStatus(t, clanCall(t, s, players[0].PID, 3, clanCreateArgs("TAG", "Synthetic Clan")), 0)
	good, _ := os.ReadFile(path)
	for _, bad := range [][]byte{append(bytes.Clone(good), []byte(" {}")...), []byte(`{"version":1,"clans":[{"abbreviation":"TAG","name":"Clan","founder_pid":999999}]}`), []byte(`{"version":1,"clans":null}`)} {
		if err := os.WriteFile(path, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := openClans(path, s.accounts); err == nil {
			t.Fatal("accepted corrupt clan state")
		}
	}
	if err := os.WriteFile(path, good, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := openClans(path, s.accounts); err == nil {
		t.Fatal("accepted public clan state")
	}
}
