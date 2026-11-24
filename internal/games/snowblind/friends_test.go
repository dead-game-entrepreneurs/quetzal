package snowblind

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func friendFixture(t *testing.T) (*startup, []quazal.Account, string) {
	t.Helper()
	s, players, clanPath := clanFixture(t)
	path := filepath.Join(filepath.Dir(clanPath), "friends.json")
	friends, err := openFriends(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.friends = friends
	s.friends.clan = s.clans.abbreviation
	return s, players, path
}

func friendCall(t *testing.T, s *startup, pid, method uint32, args []byte) []byte {
	t.Helper()
	b, err := s.handle(prudp.Peer{}, pid, quazal.Request{Protocol: 103, Method: method, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func friendBool(t *testing.T, b []byte, want bool) {
	t.Helper()
	r := quazal.NewReader(b)
	if r.Bool() != want || !r.Done() {
		t.Fatalf("friend result %x, want %v", b, want)
	}
}

func friendQuery(t *testing.T, s *startup, pid uint32) []byte {
	t.Helper()
	return friendCall(t, s, pid, 5, encode(func(w *quazal.Writer) { w.String("en-US") }))
}

func TestFriendInvitationAcceptanceNativeRowsAndDisconnectPresence(t *testing.T) {
	s, players, path := friendFixture(t)
	a, b, unrelated := players[0].PID, players[1].PID, players[2].PID
	registry, peers, urls := sessionFixture(t)
	s.friends.online = registry.online
	clanWantStatus(t, clanCall(t, s, b, 3, clanCreateArgs("TAG", "Synthetic Clan")), 0)
	friendBool(t, friendCall(t, s, a, 1, noticeArgs("sEcOnD")), true)
	friendBool(t, friendCall(t, s, a, 1, noticeArgs("Second")), true)
	fixture, err := os.ReadFile("testdata/friends-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Replies map[string]string `json:"query_replies_hex"`
	}
	if err := json.Unmarshal(fixture, &native); err != nil {
		t.Fatal(err)
	}
	assertNative := func(key string) {
		t.Helper()
		want, err := hex.DecodeString(native.Replies[key])
		if err != nil || !bytes.Equal(friendQuery(t, s, a), want) {
			t.Fatal("friend row differs from native field codecs", key)
		}
	}
	assertNative("1_online") // Sender sees an outgoing request.
	r := quazal.NewReader(friendQuery(t, s, b))
	if r.U32() != 1 || r.U32() != a || r.String(15) != players[0].Name || r.U8() != friendIncoming || r.String(128) != "||" || r.String(3) != "" || r.U32() != 0 || !r.Done() {
		t.Fatal("recipient did not receive an incoming invitation row")
	}
	if !bytes.Equal(friendQuery(t, s, unrelated), []byte{0, 0, 0, 0}) {
		t.Fatal("friend requests leaked to an unrelated account")
	}
	friendBool(t, friendCall(t, s, a, 2, gidArgs(b)), false) // Sender cannot accept for recipient.
	friendBool(t, friendCall(t, s, b, 2, gidArgs(a)), true)
	assertNative("2_online")
	registry.disconnect(peers[1], b)
	assertNative("2_offline")
	registry.registered(peers[1], b, urls[1])
	assertNative("2_online")
	loaded, err := openFriends(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	loaded.clan = s.clans.abbreviation
	s.friends = loaded
	assertNative("2_offline") // Presence is never restored from persistent friend state.
	friendBool(t, friendCall(t, s, a, 4, gidArgs(b)), true)
	if len(s.friends.state.Edges) != 0 || !bytes.Equal(friendQuery(t, s, b), []byte{0, 0, 0, 0}) {
		t.Fatal("friend deletion did not affect both players")
	}
}

func TestFriendRejectBlockCancellationAndReciprocalConsent(t *testing.T) {
	s, players, path := friendFixture(t)
	a, b := players[0].PID, players[1].PID
	invite := func() { friendBool(t, friendCall(t, s, a, 1, noticeArgs("Second")), true) }
	invite()
	friendBool(t, friendCall(t, s, b, 1, noticeArgs("Founder")), true)
	if len(s.friends.state.Edges) != 1 || s.friends.state.Edges[0].Accepted {
		t.Fatal("reciprocal invite bypassed acceptance")
	}
	friendBool(t, friendCall(t, s, b, 4, gidArgs(a)), false)
	friendBool(t, friendCall(t, s, a, 4, gidArgs(b)), true)
	invite()
	reject := func(block bool) []byte { return encode(func(w *quazal.Writer) { w.U32(a); w.Bool(block) }) }
	friendBool(t, friendCall(t, s, b, 3, reject(false)), true)
	invite()
	friendBool(t, friendCall(t, s, b, 3, reject(true)), true)
	loaded, err := openFriends(path, s.accounts)
	if err != nil {
		t.Fatal(err)
	}
	s.friends = loaded
	friendBool(t, friendCall(t, s, a, 1, noticeArgs("Second")), false)
	if len(s.friends.state.Edges) != 0 || len(s.friends.state.Blocks) != 1 {
		t.Fatal("blocked friend request was re-created")
	}
}

func TestFriendMalformedUnauthorizedAndFailedMutationDoNotChangeState(t *testing.T) {
	s, players, path := friendFixture(t)
	pid := players[0].PID
	for _, name := range []string{"Missing", players[0].Name} {
		friendBool(t, friendCall(t, s, pid, 1, noticeArgs(name)), false)
	}
	for _, request := range []quazal.Request{{Protocol: 103, Method: 1, Args: []byte{0}}, {Protocol: 103, Method: 2, Args: []byte{1}}, {Protocol: 103, Method: 3, Args: append(gidArgs(players[1].PID), 2)}, {Protocol: 103, Method: 4, Args: append(gidArgs(players[1].PID), 0)}, {Protocol: 103, Method: 5, Args: append(noticeArgs(""), 0)}} {
		if _, err := s.friends.handle(pid, request); err == nil {
			t.Fatal("accepted malformed friend request", request.Method)
		}
	}
	for _, actor := range []uint32{quazal.GuestPID, 999999} {
		if _, err := s.handle(prudp.Peer{}, actor, quazal.Request{Protocol: 103, Method: 1, Args: noticeArgs("Second")}); err == nil {
			t.Fatal("unknown account sent friend request")
		}
	}
	s.friends.path = filepath.Join(filepath.Dir(path), "missing", "friends.json")
	if _, err := s.friends.handle(pid, quazal.Request{Protocol: 103, Method: 1, Args: noticeArgs("Second")}); err == nil || len(s.friends.state.Edges) != 0 {
		t.Fatal("failed persistence awarded an invitation")
	}
	s.friends.path = path
	friendBool(t, friendCall(t, s, pid, 1, noticeArgs("Second")), true)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("friend state is not private", err)
	}
	before, _ := os.ReadFile(path)
	s.friends.path = filepath.Join(filepath.Dir(path), "missing", "friends.json")
	if _, err := s.friends.handle(players[1].PID, quazal.Request{Protocol: 103, Method: 2, Args: gidArgs(pid)}); err == nil || s.friends.state.Edges[0].Accepted {
		t.Fatal("failed acceptance changed live state")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed acceptance changed persistent state")
	}
}

func TestFriendStateRejectsMissingFieldsDuplicateEdgesAndInsecureFiles(t *testing.T) {
	s, _, path := friendFixture(t)
	for _, text := range []string{`{}`, `{"version":1,"edges":null,"blocks":[]}`, `{"version":1,"edges":[],"blocks":[]} {}`, `{"version":1,"edges":[{"from":1000,"to":999999}],"blocks":[]}`, `{"version":1,"edges":[{"from":1000,"to":1001},{"from":1001,"to":1000}],"blocks":[]}`} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := openFriends(path, s.accounts); err == nil {
			t.Fatal("accepted corrupt friend state")
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := openFriends(path, s.accounts); err == nil {
		t.Fatal("accepted public friend state")
	}
}
