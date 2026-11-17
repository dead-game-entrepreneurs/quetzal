package quazal

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func TestContactUpdateRequiresOwnProofAndCommitsBeforeSuccess(t *testing.T) {
	f := newFixture(t)
	initial := quazalcrypto.InitialKey([]byte("synthetic-password"))
	a, err := f.store.CreateWithContact("ContactOne", initial, "before@example.test")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.store.CreateWithContact("ContactTwo", quazalcrypto.Key{2}, "other@example.test")
	if err != nil {
		t.Fatal(err)
	}
	public, private := AnyData{Type: "Public", Payload: []byte{1}}, AnyData{Type: "Private", Payload: []byte{2}}
	if err := f.store.UpdateData(a.PID, public, private); err != nil {
		t.Fatal(err)
	}
	pid, ticket := startAuth(t, f, authPeer(1), a.Name, quazalcrypto.ExtendInitial(initial, a.PID))
	startSecure(t, f, securePeer(2), pid, ticket, 12)
	args := arguments(func(w *writer) { w.str("after@example.test") })
	if rpc(t, f.secure, securePeer(2), 25, 12, 20, args).Success {
		t.Fatal("unregistered contact update")
	}
	registerPeer(t, f, securePeer(2))
	got := rpc(t, f.secure, securePeer(2), 25, 12, 21, args)
	if !got.Success || !bytes.Equal(got.Payload, []byte{1, 0, 1, 0}) {
		t.Fatal("contact update qResult")
	}
	loaded, err := OpenStore(f.path)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := loaded.Lookup(a.Name)
	untouched, _ := loaded.Lookup(other.Name)
	if after.Contact != "after@example.test" || after.PID != a.PID || after.InitialKey != initial || !bytes.Equal(after.PublicData.Payload, public.Payload) || !bytes.Equal(after.PrivateData.Payload, private.Payload) || untouched.Contact != other.Contact {
		t.Fatal("contact update changed another account field or did not persist")
	}
	for i, bad := range [][]byte{append(bytes.Clone(args), 0), {20, 0, 'x'}, arguments(func(w *writer) { w.str(strings.Repeat("x", 256)) })} {
		if rpc(t, f.secure, securePeer(2), 25, 12, uint32(22+i), bad).Success {
			t.Fatal("malformed contact accepted")
		}
	}
	guestPID, guestTicket := startAuth(t, f, authPeer(3), "guest", quazalcrypto.PasswordKey([]byte(guestPassword), GuestPID))
	startSecure(t, f, securePeer(4), guestPID, guestTicket, 13)
	registerPeer(t, f, securePeer(4))
	if rpc(t, f.secure, securePeer(4), 25, 12, 21, args).Success {
		t.Fatal("guest changed contact")
	}
	// A failed disk write must not publish the new value in memory.
	f.store.path = filepath.Join(t.TempDir(), "missing", "accounts.json")
	if err := f.store.UpdateContact(a.PID, "failed@example.test"); err == nil {
		t.Fatal("failed persistence reported success")
	}
	retained, _ := f.store.Lookup(a.Name)
	if retained.Contact != after.Contact {
		t.Fatal("failed write changed live contact")
	}
}

func TestStatusHasNoApplicationOutput(t *testing.T) {
	f := newFixture(t)
	key := quazalcrypto.PasswordKey([]byte(guestPassword), GuestPID)
	pid, ticket := startAuth(t, f, authPeer(1), "guest", key)
	startSecure(t, f, securePeer(2), pid, ticket, 11)
	args := arguments(func(w *writer) { w.str("Online") })
	if rpc(t, f.secure, securePeer(2), 25, 17, 21, args).Success {
		t.Fatal("status before registration")
	}
	registerPeer(t, f, securePeer(2))
	got := rpc(t, f.secure, securePeer(2), 25, 17, 22, args)
	if !got.Success || len(got.Payload) != 0 {
		t.Fatal("status reply contains unexpected application fields")
	}
	if f.secure.peers[peerKey(securePeer(2))].status != "Online" {
		t.Fatal("status not retained")
	}
}

func TestProfileUpdateBindsProofAndPersistsExactly(t *testing.T) {
	f := newFixture(t)
	initial := quazalcrypto.InitialKey([]byte("synthetic-password"))
	a, err := f.store.Create("PlayerOne", initial)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Create("PlayerTwo", quazalcrypto.Key{2})
	if err != nil {
		t.Fatal(err)
	}
	key := quazalcrypto.ExtendInitial(initial, a.PID)
	pid, ticket := startAuth(t, f, authPeer(1), a.Name, key)
	startSecure(t, f, securePeer(2), pid, ticket, 12)
	registerPeer(t, f, securePeer(2))
	f.secure.options.ValidateAccountData = func(public, private AnyData) error { return nil }
	public := []byte{4, 0, 0, 0, 1, 2, 3, 4}
	private := []byte{1, 0, 0, 0, 0}
	args := arguments(func(w *writer) { w.str("PublicFixture"); w.buffer(public); w.str("PrivateFixture"); w.buffer(private) })
	got := rpc(t, f.secure, securePeer(2), 25, 13, 21, args)
	if !got.Success || !bytes.Equal(got.Payload, []byte{1, 0, 1, 0}) {
		t.Fatal("profile update qResult")
	}
	loaded, err := OpenStore(f.path)
	if err != nil {
		t.Fatal(err)
	}
	after, ok := loaded.Lookup(a.Name)
	if !ok || after.PID != a.PID || after.InitialKey != initial || after.PublicData.Type != "PublicFixture" || !bytes.Equal(after.PublicData.Payload, public) || !bytes.Equal(after.PrivateData.Payload, private) {
		t.Fatal("profile or identity changed on disk")
	}
	other, _ := loaded.Lookup("PlayerTwo")
	if other.PublicData != nil || other.PrivateData != nil {
		t.Fatal("another account modified")
	}
	after.PublicData.Payload[0] = 255
	immutable, _ := loaded.Lookup(a.Name)
	if immutable.PublicData.Payload[0] != 4 {
		t.Fatal("lookup exposed mutable profile storage")
	}
	if rpc(t, f.secure, securePeer(2), 25, 13, 22, append(args, 0)).Success {
		t.Fatal("profile trailing data accepted")
	}
	guestPID, guestTicket := startAuth(t, f, authPeer(3), "guest", quazalcrypto.PasswordKey([]byte(guestPassword), GuestPID))
	startSecure(t, f, securePeer(4), guestPID, guestTicket, 13)
	registerPeer(t, f, securePeer(4))
	if rpc(t, f.secure, securePeer(4), 25, 13, 21, args).Success {
		t.Fatal("guest modified a profile")
	}
}

func TestLegacyAccountMigrationPreservesCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	key := quazalcrypto.Key{7}
	disk := diskStore{Version: 1, NextPID: 1001, Accounts: []diskAccount{{Name: "Legacy", PID: 1000, InitialHash: hex.EncodeToString(key[:])}}}
	b, _ := json.Marshal(disk)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	err = store.UpdateData(1000, AnyData{Type: "Public", Payload: []byte{1}}, AnyData{Type: "Private", Payload: []byte{2}})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := reloaded.Lookup("Legacy")
	if a.PID != 1000 || a.InitialKey != key {
		t.Fatal("migration changed identity or password hash")
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &disk); err != nil || disk.Version != 2 {
		t.Fatal("migration did not write version 2")
	}
}
