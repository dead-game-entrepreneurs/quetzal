package quazal

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func TestRegisteredURLScheme(t *testing.T) {
	for _, test := range []struct {
		name, option, want string
	}{
		{"default", "", "prudp"},
		{"native", "prudp", "prudp"},
		{"secure", "prudps", "prudps"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			options := f.secure.options
			options.RegisteredURLScheme = test.option
			var err error
			f.secure, err = New(options)
			if err != nil {
				t.Fatal(err)
			}
			guestKey := quazalcrypto.PasswordKey([]byte(guestPassword), GuestPID)
			pid, ticket := startAuth(t, f, authPeer(1), "guest", guestKey)
			peer := securePeer(2)
			startSecure(t, f, peer, pid, ticket, 1)
			response := rpc(t, f.secure, peer, 11, 1, 10, arguments(func(w *writer) {
				w.u32(1)
				w.str("prudp:/address=192.0.2.100;port=3658;stream=3;sid=1;type=2")
			}))
			if !response.Success {
				t.Fatal("registration failed")
			}
			r := reader{data: response.Payload}
			if r.u32() != Success {
				t.Fatal("registration qResult")
			}
			cid := r.u32()
			url := r.str(512)
			want := fmt.Sprintf("%s:/address=127.0.0.1;port=45001;stream=3;sid=1;PID=%d;CID=%d;type=2", test.want, GuestPID, cid)
			if cid == 0 || url != want || !r.done() {
				t.Fatalf("registration StationURL %q, want %q", url, want)
			}
			if state := f.secure.peers[peerKey(peer)]; state.stationURL != want {
				t.Fatal("stored StationURL differs from returned URL")
			}
		})
	}
	f := newFixture(t)
	for _, scheme := range []string{"http", "PRUDP", "prudp:/", "prudp;PID=999"} {
		options := f.secure.options
		options.RegisteredURLScheme = scheme
		if _, err := New(options); err == nil {
			t.Fatalf("accepted invalid registered scheme %q", scheme)
		}
	}
}

func TestGuestGameRPCOptInIsPerOperationAndRequiresRegistration(t *testing.T) {
	f := newFixture(t)
	calls, policyCalls := 0, 0
	options := f.secure.options
	options.AllowGuestGameRPC = func(request Request) bool {
		policyCalls++
		return request.Protocol == 76 && request.Method == 4
	}
	options.GameRPC = func(peer prudp.Peer, pid uint32, request Request) ([]byte, error) {
		calls++
		if peer != securePeer(2) || pid != GuestPID || request.Protocol != 76 || request.Method != 4 {
			t.Fatal("unexpected guest callback identity or operation")
		}
		if len(request.Args) != 0 {
			return nil, ErrRMC
		}
		return []byte{0x12, 0x34}, nil
	}
	var err error
	f.secure, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	guestKey := quazalcrypto.PasswordKey([]byte(guestPassword), GuestPID)
	pid, ticket := startAuth(t, f, authPeer(1), "guest", guestKey)
	startSecure(t, f, securePeer(2), pid, ticket, 1)
	if rpc(t, f.secure, securePeer(2), 76, 4, 3, nil).Success || calls != 0 || policyCalls != 0 {
		t.Fatal("guest policy bypassed mandatory registration")
	}
	registerPeer(t, f, securePeer(2))
	for i, request := range []Request{{Protocol: 76, Method: 5}, {Protocol: 77, Method: 4}} {
		if rpc(t, f.secure, securePeer(2), request.Protocol, request.Method, uint32(11+i), nil).Success || calls != 0 {
			t.Fatal("guest opt-in authorized a different operation")
		}
	}
	response := rpc(t, f.secure, securePeer(2), 76, 4, 13, nil)
	if !response.Success || !bytes.Equal(response.Payload, []byte{0x12, 0x34}) || calls != 1 || policyCalls != 3 {
		t.Fatalf("allowed guest operation failed: %+v, calls %d, policy calls %d", response, calls, policyCalls)
	}
	if rpc(t, f.secure, securePeer(2), 76, 4, 14, []byte{1}).Success || calls != 2 {
		t.Fatal("title handler could not reject malformed allowed operation")
	}
	// The predicate only applies to GameRPC; it does not grant account-only
	// access to shared operations such as contact updates.
	if rpc(t, f.secure, securePeer(2), 25, 12, 15, arguments(func(w *writer) { w.str("fixture@example.test") })).Success || calls != 2 || policyCalls != 4 {
		t.Fatal("guest policy escaped the game RPC boundary")
	}
}

func TestTicketsDoNotCrossStoresWithIdenticalPrincipalKeyAndPID(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	initial := quazalcrypto.InitialKey([]byte("same-synthetic-password"))
	accountA, err := a.store.Create("SamePlayer", initial)
	if err != nil {
		t.Fatal(err)
	}
	accountB, err := b.store.Create("SamePlayer", initial)
	if err != nil {
		t.Fatal(err)
	}
	if accountA.PID != accountB.PID || a.serverKey != b.serverKey {
		t.Fatal("test requires identical principal key and account PID across stores")
	}
	clientKey := quazalcrypto.ExtendInitial(initial, accountA.PID)
	pid, ticket := startAuth(t, a, authPeer(1), "SamePlayer", clientKey)
	wire, err := quazalcrypto.BuildConnect(ticket.Ticket, ticket.SessionKey, quazalcrypto.Challenge{PrincipalID: pid, Nonce: 42})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := quazalcrypto.OpenConnect(b.serverKey, wire); err != nil {
		t.Fatalf("ticket must be cryptographically valid for destination key: %v", err)
	}
	if _, err := b.secure.Connect(context.Background(), securePeer(2), wire); err == nil || !strings.Contains(err.Error(), "not issued") {
		t.Fatalf("another store's ticket was not rejected by issuance binding: %v", err)
	}
	// Rejection must not create a peer or consume a valid local handshake.
	localPID, localTicket := startAuth(t, b, authPeer(1), "SamePlayer", clientKey)
	startSecure(t, b, securePeer(2), localPID, localTicket, 42)
	startSecure(t, a, securePeer(2), pid, ticket, 42)
}
