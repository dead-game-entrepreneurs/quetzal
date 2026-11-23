package quazal

import (
	"bytes"
	"context"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func TestTitleSignupPrincipalAndParameterStayScoped(t *testing.T) {
	f := newFixture(t)
	const principal = "**CuentaCrearCSFv1**"
	policy := SignupPolicy{Name: principal, Password: principal, Parameter: 0}
	opts := f.auth.options
	opts.Signup = &policy
	var err error
	f.auth, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Role = Secure
	f.secure, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	policy.Name = "mutated" // Constructor must own a copy of the policy.
	pid, ticket := startAuth(t, f, authPeer(1), principal, quazalcrypto.PasswordKey([]byte(principal), GuestPID))
	startSecure(t, f, securePeer(2), pid, ticket, 1)
	registerPeer(t, f, securePeer(2))
	initial := quazalcrypto.InitialKey([]byte("synthetic password"))
	signup := func(parameter uint32) []byte {
		return arguments(func(w *writer) { w.str("commando"); w.str(hex.EncodeToString(initial[:])); w.u32(parameter); w.str("") })
	}
	if rpc(t, f.secure, securePeer(2), 25, 1, 11, signup(0x20)).Success {
		t.Fatal("accepted another title's signup parameter")
	}
	if !rpc(t, f.secure, securePeer(2), 25, 1, 12, signup(0)).Success {
		t.Fatal("title signup rejected")
	}
	if rpc(t, f.secure, securePeer(2), 21, 1, 13, nil).Success {
		t.Fatal("bootstrap reached gameplay")
	}
	if _, err = f.auth.Connect(context.Background(), authPeer(3), nil); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"guest", "**CuentaCrearCSFv2**", "another_long_username"} {
		if rpc(t, f.auth, authPeer(3), 10, 1, uint32(i+1), arguments(func(w *writer) { w.str(name) })).Success {
			t.Fatalf("accepted %q", name)
		}
	}
	// The default title still accepts only its own bootstrap identity.
	defaultTitle := newFixture(t)
	if _, err = defaultTitle.auth.Connect(context.Background(), authPeer(1), nil); err != nil {
		t.Fatal(err)
	}
	if rpc(t, defaultTitle.auth, authPeer(1), 10, 1, 1, arguments(func(w *writer) { w.str(principal) })).Success {
		t.Fatal("title principal leaked into defaults")
	}
}

func TestPasswordChangePersistsAndRevokesTicketsAndCachedProofs(t *testing.T) {
	f := newFixture(t)
	initial := quazalcrypto.InitialKey([]byte("old synthetic password"))
	account, err := f.store.Create("commando", initial)
	if err != nil {
		t.Fatal(err)
	}
	pid, ticket := startAuth(t, f, authPeer(1), account.Name, quazalcrypto.ExtendInitial(initial, account.PID))
	startSecure(t, f, securePeer(2), pid, ticket, 1)
	registerPeer(t, f, securePeer(2))
	startSecure(t, f, securePeer(3), pid, ticket, 2)
	registerPeer(t, f, securePeer(3))
	next := quazalcrypto.InitialKey([]byte("new synthetic password"))
	args := arguments(func(w *writer) { w.str(hex.EncodeToString(next[:])) })
	if rpc(t, f.secure, securePeer(2), 25, 4, 11, args).Success {
		t.Fatal("password change enabled without title opt-in")
	}
	f.secure.options.EnablePasswordChange = true
	if rpc(t, f.secure, securePeer(2), 25, 4, 12, arguments(func(w *writer) { w.str("not a hash") })).Success {
		t.Fatal("malformed password accepted")
	}
	if !rpc(t, f.secure, securePeer(3), 25, 17, 11, arguments(func(w *writer) { w.str("Online") })).Success {
		t.Fatal("second proof rejected before change")
	}
	disconnects := 0
	f.secure.options.GameDisconnect = func(_ prudp.Peer, pid uint32) {
		if pid != account.PID {
			t.Fatal("wrong identity")
		}
		disconnects++
	}
	changed := rpc(t, f.secure, securePeer(2), 25, 4, 13, args)
	if !changed.Success || !bytes.Equal(changed.Payload, []byte{1}) {
		t.Fatalf("password result %+v", changed)
	}
	if disconnects != 2 {
		t.Fatalf("revoked %d registrations", disconnects)
	}
	if rpc(t, f.secure, securePeer(3), 25, 17, 11, arguments(func(w *writer) { w.str("Online") })).Success {
		t.Fatal("revoked session served cached success")
	}
	if rpc(t, f.secure, securePeer(2), 25, 17, 14, arguments(func(w *writer) { w.str("Online") })).Success {
		t.Fatal("changing session retained proof")
	}
	connect, err := quazalcrypto.BuildConnect(ticket.Ticket, ticket.SessionKey, quazalcrypto.Challenge{PrincipalID: pid, Nonce: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.secure.Connect(context.Background(), securePeer(4), connect); err == nil {
		t.Fatal("old ticket still accepted")
	}
	reloaded, err := OpenStore(f.path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Lookup(account.Name)
	if !ok || got.InitialKey != next {
		t.Fatal("new initial hash not persisted")
	}
	pid, newTicket := startAuth(t, f, authPeer(5), account.Name, quazalcrypto.ExtendInitial(next, pid))
	startSecure(t, f, securePeer(6), pid, newTicket, 4)
	registerPeer(t, f, securePeer(6))
	f.secure.Disconnect(securePeer(2))
	f.secure.Disconnect(securePeer(3))
	if disconnects != 2 {
		t.Fatal("revoked connection cleanup repeated")
	}
}

func TestFailedPasswordPersistencePreservesOldProof(t *testing.T) {
	f := newFixture(t)
	f.secure.options.EnablePasswordChange = true
	initial := quazalcrypto.InitialKey([]byte("synthetic password"))
	account, err := f.store.Create("commando", initial)
	if err != nil {
		t.Fatal(err)
	}
	pid, ticket := startAuth(t, f, authPeer(1), account.Name, quazalcrypto.ExtendInitial(initial, account.PID))
	startSecure(t, f, securePeer(2), pid, ticket, 1)
	registerPeer(t, f, securePeer(2))
	f.store.path = filepath.Join(t.TempDir(), "missing", "accounts.json")
	next := quazalcrypto.InitialKey([]byte("different synthetic password"))
	if rpc(t, f.secure, securePeer(2), 25, 4, 11, arguments(func(w *writer) { w.str(hex.EncodeToString(next[:])) })).Success {
		t.Fatal("failed persistence reported success")
	}
	got, _ := f.store.Lookup(account.Name)
	if got.InitialKey != initial {
		t.Fatal("failed write changed password")
	}
	if !rpc(t, f.secure, securePeer(2), 25, 17, 12, arguments(func(w *writer) { w.str("Online") })).Success {
		t.Fatal("failed write revoked proof")
	}
}
