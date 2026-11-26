package rsvpsp

import (
	"bytes"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// friendEvent checks a 14/1 friend notification.
func friendEvent(t *testing.T, call quazal.Request, source, relationship uint32, name string) {
	t.Helper()
	r := reader{b: call.Args}
	if call.Protocol != 14 || call.Method != 1 || r.u32() != source || r.u32() != eventFriend ||
		r.u32() != source || r.u32() != relationship || r.str(64) != name || !r.done() {
		t.Fatalf("friend event %d/%d %x", call.Protocol, call.Method, call.Args)
	}
}

// friendRow is one 60/10 entry.
type friendRow struct {
	pid                        uint32
	name, status               string
	friend, incoming, outgoing bool
	rating                     uint32
	playing                    string
}

func friendRows(t *testing.T, b []byte) []friendRow {
	t.Helper()
	r := reader{b: b}
	rows := make([]friendRow, r.u32())
	for i := range rows {
		rows[i] = friendRow{pid: r.u32(), name: r.str(64), status: r.str(64), friend: r.bool(), incoming: r.bool(),
			outgoing: r.bool(), rating: r.u32(), playing: r.str(64)}
	}
	if !r.done() {
		t.Fatalf("friends list %x", b)
	}
	return rows
}

// TestFriendRequestFlowOverUDP covers the field call (AddFriendByName with
// the captured arguments), the request and acceptance notifications, the
// friends list (60/10), player info (60/11), deletion and persistence.
func TestFriendRequestFlowOverUDP(t *testing.T) {
	a := start(t)
	tris, trisPID := account(t, a, "Tris", 10)
	sneed, sneedPID := account(t, a, "Sneed", 20)

	// Captured 20/2 from the field: "TRIS", details 0, empty message.
	field, _ := hex.DecodeString("0500545249530000000000010000")
	if got := sneed.rpc(20, 2, field); !bytes.Equal(got, []byte{1}) {
		t.Fatalf("AddFriendByName reply %x", got)
	}
	friendEvent(t, tris.serverCall(), sneedPID, friendRequested, "Sneed")
	// Asking again changes nothing and sends nothing.
	sneed.rpc(20, 2, field)
	for _, bad := range [][]byte{
		cat(str("Nobody"), u32(0), str("")),
		cat(str("Sneed"), u32(0), str("")),
	} {
		if got := sneed.rpc(20, 2, bad); !bytes.Equal(got, []byte{0}) {
			t.Fatalf("bad AddFriendByName reply %x", got)
		}
	}
	if sneed.exchange(20, 2, field[:len(field)-1]).Success {
		t.Fatal("truncated AddFriendByName accepted")
	}

	rows := friendRows(t, tris.rpc(60, 10, nil))
	if len(rows) != 1 || rows[0] != (friendRow{pid: sneedPID, name: "Sneed", status: "Online", incoming: true}) {
		t.Fatalf("Tris's list %+v", rows)
	}
	rows = friendRows(t, sneed.rpc(60, 10, nil))
	if len(rows) != 1 || rows[0] != (friendRow{pid: trisPID, name: "Tris", status: "Online", outgoing: true}) {
		t.Fatalf("Sneed's list %+v", rows)
	}
	if got := tris.rpc(60, 11, u32(sneedPID)); !bytes.Equal(got, cat([]byte{0, 1, 0, 0}, u32(0), str(""), []byte{0})) {
		t.Fatalf("player info with a request %x", got)
	}

	// Only the asked player can accept.
	if got := sneed.rpc(20, 3, u32(trisPID)); !bytes.Equal(got, []byte{0}) {
		t.Fatalf("requester accepted own request: %x", got)
	}
	if got := tris.rpc(20, 3, u32(sneedPID)); !bytes.Equal(got, []byte{1}) {
		t.Fatalf("AcceptFriendship reply %x", got)
	}
	friendEvent(t, sneed.serverCall(), trisPID, friendAccepted, "Tris")

	// A friend hosting a game shows it and can be joined.
	id := tris.rpc(60, 4, createArgs(t))
	rows = friendRows(t, sneed.rpc(60, 10, nil))
	if len(rows) != 1 || !rows[0].friend || rows[0].incoming || rows[0].outgoing || rows[0].playing != "VEGAS" {
		t.Fatalf("Sneed's list with a friend %+v", rows)
	}
	if got := sneed.rpc(60, 11, u32(trisPID)); !bytes.Equal(got, cat([]byte{1, 0, 0, 0}, id, str(""), []byte{1})) {
		t.Fatalf("player info for a hosting friend %x", got)
	}

	// Deleting tells the other player and empties both lists.
	if got := sneed.rpc(20, 7, u32(trisPID)); !bytes.Equal(got, []byte{1}) {
		t.Fatalf("ClearRelationship reply %x", got)
	}
	friendEvent(t, tris.serverCall(), sneedPID, friendDeclined, "Sneed")
	if rows := friendRows(t, tris.rpc(60, 10, nil)); len(rows) != 0 {
		t.Fatalf("list after delete %+v", rows)
	}

	// A declined request is gone and the requester is told.
	sneed.rpc(20, 2, field)
	friendEvent(t, tris.serverCall(), sneedPID, friendRequested, "Sneed")
	if got := tris.rpc(20, 4, u32(sneedPID)); !bytes.Equal(got, []byte{1}) {
		t.Fatalf("DeclineFriendship reply %x", got)
	}
	friendEvent(t, sneed.serverCall(), trisPID, friendDeclined, "Tris")

	// A request survives a restart (friends.json).
	sneed.rpc(20, 2, field)
	b, err := os.ReadFile(filepath.Join(a.state, "friends.json"))
	if err != nil || !bytes.Contains(b, []byte(directed(sneedPID, trisPID))) {
		t.Fatalf("friends.json %s %v", b, err)
	}
}

func TestFriendsCrossRequestsAndStoredState(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	names := map[uint32]string{1000: "Tris", 1001: "Sneed"}
	g := newGames(log, func(pid uint32) string { return names[pid] })
	path := filepath.Join(t.TempDir(), "friends.json")
	lookup := func(name string) (uint32, bool) {
		for pid, n := range names {
			if n == name {
				return pid, true
			}
		}
		return 0, false
	}
	s, err := openSocial(path, g, lookup)
	if err != nil {
		t.Fatal(err)
	}
	g.registered(prudp.Peer{ID: 1}, 1000, "")
	call := func(pid, method uint32, args []byte) []byte {
		t.Helper()
		reply, ok, err := s.friends(pid, quazal.Request{Protocol: FriendsProtocol, Method: method, Args: args})
		if !ok || err != nil {
			t.Fatal(ok, err)
		}
		return reply
	}
	call(1000, 1, cat(u32(1001), u32(0), str("")))
	// Both asking makes them friends.
	if got := call(1001, 2, cat(str("Tris"), u32(0), str(""))); !bytes.Equal(got, []byte{1}) {
		t.Fatalf("cross request %x", got)
	}
	calls := g.serverCalls()
	if len(calls) != 1 {
		t.Fatalf("notifications %+v", calls)
	}
	friendEvent(t, quazal.Request{Protocol: 14, Method: 1, Args: calls[0].Args}, 1001, friendAccepted, "Sneed")
	if rel := s.relationLocked(1000, 1001); rel != (relation{friend: true}) {
		t.Fatalf("relation %+v", rel)
	}
	if got := call(1000, 8, cat(u32(1001), u32(5))); !bytes.Equal(got, []byte{1}) {
		t.Fatalf("UpdateDetails %x", got)
	}
	if _, ok, _ := s.friends(1000, quazal.Request{Protocol: FriendsProtocol, Method: 5, Args: cat(u32(1001), u32(0))}); ok {
		t.Fatal("blacklist answered")
	}
	reopened, err := openSocial(path, g, lookup)
	if err != nil || !reopened.state.Friends[pair(1000, 1001)] || len(reopened.state.Requests) != 0 {
		t.Fatalf("reopened %+v %v", reopened, err)
	}
	if err := os.WriteFile(path, []byte(`{"friends":{"1001:1000":true},"requests":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openSocial(path, g, lookup); err == nil {
		t.Fatal("unordered friendship key accepted")
	}
}
