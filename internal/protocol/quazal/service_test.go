// Adapted from sibling tdu-server; title differences are documented in docs/games/25tolife/analysis.md.
package quazal

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

type fixture struct {
	store        *Store
	auth, secure *Service
	now          time.Time
	path         string
	serverKey    quazalcrypto.Key
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{now: time.Unix(1800000000, 0), path: filepath.Join(t.TempDir(), "accounts.json")}
	for i := range f.serverKey {
		f.serverKey[i] = byte(i + 16)
	}
	var err error
	f.store, err = OpenStore(f.path)
	if err != nil {
		t.Fatal(err)
	}
	options := Options{Role: Auth, PublicIP: netip.MustParseAddr("127.0.0.1"), SecurePort: 30121, ServerPID: 2, PrincipalKey: f.serverKey, Store: f.store, Now: func() time.Time { return f.now }, TicketTTL: time.Minute, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	f.auth, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	options.Role = Secure
	options.SecurePort = 0
	f.secure, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func authPeer(id uint64) prudp.Peer {
	return prudp.Peer{ID: id, Addr: netip.MustParseAddrPort("127.0.0.1:45000"), LocalPort: 30120, SourceStream: 0x31, DestinationStream: 0x31, ClientSession: 3}
}
func securePeer(id uint64) prudp.Peer {
	p := authPeer(id)
	p.LocalPort = 30121
	p.Addr = netip.MustParseAddrPort("127.0.0.1:45001")
	return p
}
func arguments(fn func(*writer)) []byte {
	var w writer
	fn(&w)
	if w.err != nil {
		panic(w.err)
	}
	return w.data
}
func rpc(t *testing.T, s *Service, p prudp.Peer, protocol byte, method, call uint32, args []byte) Response {
	t.Helper()
	wire, err := EncodeRequest(Request{Protocol: protocol, CallID: call, Method: method, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	replies, err := s.Handle(context.Background(), p, wire)
	if err != nil || len(replies) != 1 {
		t.Fatalf("RPC: %v, %d replies", err, len(replies))
	}
	response, err := ParseResponse(replies[0])
	if err != nil {
		t.Fatal(err)
	}
	if response.Protocol != protocol || response.CallID != call {
		t.Fatalf("reply misrouted: %+v", response)
	}
	return response
}
func startAuth(t *testing.T, f *fixture, p prudp.Peer, name string, key quazalcrypto.Key) (uint32, quazalcrypto.ServiceTicketEnvelope) {
	t.Helper()
	if _, err := f.auth.Connect(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
	response := rpc(t, f.auth, p, 10, 1, 1, arguments(func(w *writer) { w.str(name) }))
	if !response.Success {
		t.Fatalf("login failure: %+v", response)
	}
	r := reader{data: response.Payload}
	if r.u32() != Success {
		t.Fatal("login qResult")
	}
	pid := r.u32()
	proof := r.buffer(128)
	if _, err := quazalcrypto.Open(key, proof); err != nil {
		t.Fatalf("password proof: %v", err)
	}
	if got := r.str(512); got != "prudps:/address=127.0.0.1;port=30121;stream=3;sid=1;PID=2;CID=1;type=2" {
		t.Fatalf("secure URL %q", got)
	}
	if r.u32() != 0 || r.str(512) != "" || r.str(255) != "" || !r.done() {
		t.Fatal("RVConnectionData/result shape")
	}
	response = rpc(t, f.auth, p, 10, 3, 2, arguments(func(w *writer) { w.u32(pid); w.u32(2) }))
	if !response.Success {
		t.Fatalf("ticket failure: %+v", response)
	}
	r = reader{data: response.Payload}
	if r.u32() != Success {
		t.Fatal("ticket qResult")
	}
	sealed := r.buffer(512)
	if !r.done() {
		t.Fatal("ticket result shape")
	}
	envelope, err := quazalcrypto.OpenServiceTicket(key, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.ServerPID != 2 {
		t.Fatal("destination PID")
	}
	return pid, envelope
}
func startSecure(t *testing.T, f *fixture, p prudp.Peer, pid uint32, envelope quazalcrypto.ServiceTicketEnvelope, nonce uint32) []byte {
	t.Helper()
	wire, err := quazalcrypto.BuildConnect(envelope.Ticket, envelope.SessionKey, quazalcrypto.Challenge{PrincipalID: pid, ConnectionID: 77, Nonce: nonce})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.secure.Connect(context.Background(), p, wire)
	if err != nil {
		t.Fatal(err)
	}
	if err := quazalcrypto.VerifyConnectReply(result.Payload, nonce); err != nil {
		t.Fatal(err)
	}
	return wire
}
func registerPeer(t *testing.T, f *fixture, p prudp.Peer) {
	t.Helper()
	response := rpc(t, f.secure, p, 11, 1, 10, arguments(func(w *writer) { w.u32(1); w.str("prudp:/address=192.0.2.100;port=3658;stream=3;sid=1;type=2") }))
	if !response.Success {
		t.Fatal("registration failed")
	}
	r := reader{data: response.Payload}
	if r.u32() != Success || r.u32() == 0 {
		t.Fatal("registration result")
	}
	url := r.str(512)
	if !strings.HasPrefix(url, "prudp:/") || !strings.Contains(url, "address=127.0.0.1;port=45001") || !r.done() {
		t.Fatalf("must report observed endpoint, got %q", url)
	}
}

func TestGameRPCRequiresRegisteredNamedAccount(t *testing.T) {
	f := newFixture(t)
	calls := 0
	disconnects := 0
	f.secure.options.GameDisconnect = func(peer prudp.Peer, pid uint32) {
		if peer.ID != 4 || pid != 1000 {
			t.Fatal("disconnect identity")
		}
		disconnects++
	}
	f.secure.options.GameRPC = func(_ prudp.Peer, pid uint32, request Request) ([]byte, error) {
		calls++
		if pid != 1000 || request.Protocol != 21 {
			t.Fatal("game RPC identity")
		}
		return []byte{1, 0, 0, 0}, nil
	}
	guestKey := quazalcrypto.PasswordKey([]byte(guestPassword), GuestPID)
	pid, ticket := startAuth(t, f, authPeer(1), "guest", guestKey)
	startSecure(t, f, securePeer(2), pid, ticket, 1)
	registerPeer(t, f, securePeer(2))
	if rpc(t, f.secure, securePeer(2), 21, 18, 11, nil).Success || calls != 0 {
		t.Fatal("guest reached game services")
	}
	var initial quazalcrypto.Key
	initial[0] = 1
	account, err := f.store.Create("example", initial)
	if err != nil {
		t.Fatal(err)
	}
	key := quazalcrypto.ExtendInitial(initial, account.PID)
	pid, ticket = startAuth(t, f, authPeer(3), "example", key)
	startSecure(t, f, securePeer(4), pid, ticket, 2)
	if rpc(t, f.secure, securePeer(4), 21, 18, 11, nil).Success || calls != 0 {
		t.Fatal("unregistered peer reached game services")
	}
	registerPeer(t, f, securePeer(4))
	if !rpc(t, f.secure, securePeer(4), 21, 18, 12, nil).Success || calls != 1 {
		t.Fatal("proven account could not use game services")
	}
	f.secure.Disconnect(securePeer(2))
	f.secure.Disconnect(securePeer(4))
	f.secure.Disconnect(securePeer(4))
	if disconnects != 1 {
		t.Fatal("game disconnect lifecycle")
	}
}

func TestGuestSignupAccountLoginAndRejectTitleSpecificBootstrap(t *testing.T) {
	f := newFixture(t)
	guestKey := quazalcrypto.PasswordKey([]byte(guestPassword), GuestPID)
	pid, guestTicket := startAuth(t, f, authPeer(1), "guest", guestKey)
	startSecure(t, f, securePeer(2), pid, guestTicket, 0xffffffff)
	denied := rpc(t, f.secure, securePeer(2), 104, 1, 8, nil)
	if denied.Success {
		t.Fatal("guest bootstrap permitted")
	}
	registerPeer(t, f, securePeer(2))
	denied = rpc(t, f.secure, securePeer(2), 104, 1, 9, nil)
	if denied.Success {
		t.Fatal("TDU-specific bootstrap enabled for 25 to Life")
	}
	password := []byte("synthetic-password")
	initial := quazalcrypto.InitialKey(password)
	signupArgs := arguments(func(w *writer) {
		w.str("DriverOne")
		w.str(hex.EncodeToString(initial[:]))
		w.u32(0x20)
		w.str("fixture@example.test")
	})
	response := rpc(t, f.secure, securePeer(2), 25, 1, 11, signupArgs)
	if !response.Success || !bytes.Equal(response.Payload, []byte{1, 0, 1, 0}) {
		t.Fatalf("signup: %+v", response)
	}
	// A retransmitted call is idempotent; a new duplicate signup is an error.
	repeated := rpc(t, f.secure, securePeer(2), 25, 1, 11, signupArgs)
	if !bytes.Equal(repeated.Payload, response.Payload) {
		t.Fatal("signup replay changed")
	}
	if rpc(t, f.secure, securePeer(2), 25, 1, 12, signupArgs).Success {
		t.Fatal("duplicate account succeeded")
	}
	account, ok := f.store.Lookup("driverone")
	if !ok || account.PID != 1000 || account.InitialKey != initial {
		t.Fatal("stored account identity/hash")
	}
	info, err := os.Stat(f.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("account file permissions")
	}
	data, err := os.ReadFile(f.path)
	if err != nil || bytes.Contains(data, password) {
		t.Fatal("stored raw password")
	}
	reloaded, err := OpenStore(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := reloaded.Lookup("DRIVERONE"); !ok || a.InitialKey != initial {
		t.Fatal("persistent credential reload")
	}
	accountKey := quazalcrypto.PasswordKey(password, account.PID)
	userPID, userTicket := startAuth(t, f, authPeer(3), "driverone", accountKey)
	startSecure(t, f, securePeer(4), userPID, userTicket, 12)
	if rpc(t, f.secure, securePeer(4), 104, 1, 8, nil).Success {
		t.Fatal("bootstrap before registration")
	}
	registerPeer(t, f, securePeer(4))
	bootstrap := rpc(t, f.secure, securePeer(4), 104, 1, 9, nil)
	if bootstrap.Success {
		t.Fatalf("TDU-specific bootstrap enabled: %+v", bootstrap)
	}
	if rpc(t, f.secure, securePeer(4), 25, 1, 11, signupArgs).Success {
		t.Fatal("non-guest signup permitted")
	}
	if rpc(t, f.secure, securePeer(4), 104, 1, 12, []byte{0}).Success {
		t.Fatal("bootstrap extra arguments permitted")
	}
}

func TestTicketPeerBindingExpiryAndReplay(t *testing.T) {
	f := newFixture(t)
	guestKey := quazalcrypto.PasswordKey([]byte(guestPassword), GuestPID)
	pid, envelope := startAuth(t, f, authPeer(1), "guest", guestKey)
	if _, err := f.auth.Connect(context.Background(), authPeer(9), []byte{1}); err == nil {
		t.Fatal("auth accepted security payload")
	}
	if _, err := f.auth.Connect(context.Background(), authPeer(9), nil); err != nil {
		t.Fatal(err)
	}
	if rpc(t, f.auth, authPeer(9), 10, 3, 1, arguments(func(w *writer) { w.u32(pid); w.u32(2) })).Success {
		t.Fatal("ticket from peer without selected account")
	}
	for _, source := range []uint32{0, pid + 1} {
		if rpc(t, f.auth, authPeer(1), 10, 3, 10+source, arguments(func(w *writer) { w.u32(source); w.u32(2) })).Success {
			t.Fatal("spoofed source PID")
		}
	}
	wire := startSecure(t, f, securePeer(2), pid, envelope, 8)
	// Same connection retry is accepted; a second peer cannot replay the nonce.
	if _, err := f.secure.Connect(context.Background(), securePeer(2), wire); err != nil {
		t.Fatal("CONNECT retry rejected")
	}
	if _, err := f.secure.Connect(context.Background(), securePeer(3), wire); err == nil {
		t.Fatal("cross-peer nonce replay")
	}
	f.secure.Disconnect(securePeer(2))
	if _, err := f.secure.Connect(context.Background(), securePeer(3), wire); err == nil {
		t.Fatal("disconnect erased replay record")
	}
	changed := bytes.Clone(wire)
	changed[4] ^= 1
	if _, err := f.secure.Connect(context.Background(), securePeer(4), changed); err == nil {
		t.Fatal("tampered ticket")
	}
	next, err := quazalcrypto.BuildConnect(envelope.Ticket, envelope.SessionKey, quazalcrypto.Challenge{PrincipalID: pid, Nonce: 9})
	if err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(2 * time.Minute)
	if _, err := f.secure.Connect(context.Background(), securePeer(5), next); err == nil {
		t.Fatal("expired ticket")
	}
}

func TestWrongPasswordUnsupportedAndMalformedRPC(t *testing.T) {
	f := newFixture(t)
	initial := quazalcrypto.InitialKey([]byte("right-password"))
	account, err := f.store.Create("Player", initial)
	if err != nil {
		t.Fatal(err)
	}
	p := authPeer(1)
	if _, err := f.auth.Connect(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
	result := rpc(t, f.auth, p, 10, 1, 1, arguments(func(w *writer) { w.str("Player") }))
	r := reader{data: result.Payload}
	r.u32()
	r.u32()
	proof := r.buffer(256)
	if _, err := quazalcrypto.Open(quazalcrypto.PasswordKey([]byte("wrong-password"), account.PID), proof); err == nil {
		t.Fatal("wrong password opened proof")
	}
	if _, err := quazalcrypto.Open(quazalcrypto.PasswordKey([]byte("right-password"), account.PID), proof); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		protocol byte
		method   uint32
	}{{10, 2}, {11, 4}, {99, 1}, {25, 1}} {
		if rpc(t, f.auth, p, test.protocol, test.method, 10+test.method+uint32(test.protocol), nil).Success {
			t.Fatal("unsupported task returned success")
		}
	}
	// Same call ID with different input must not reuse a prior successful reply.
	if rpc(t, f.auth, p, 10, 1, 1, arguments(func(w *writer) { w.str("guest") })).Success {
		t.Fatal("conflicting call reused cached success")
	}
	bad := []byte{0xff, 0xff, 0xff, 0xff, 0x8a, 0, 0, 0, 0, 1, 0, 0, 0}
	if _, err := f.auth.Handle(context.Background(), p, bad); err == nil {
		t.Fatal("malformed prefix")
	}
	p.ID = 999
	if _, err := f.auth.Handle(context.Background(), p, mustRequest(t, Request{Protocol: 10, CallID: 1, Method: 1})); err == nil {
		t.Fatal("RPC before CONNECT")
	}
}

func mustRequest(t *testing.T, r Request) []byte {
	t.Helper()
	wire, err := EncodeRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestRMCWirePrefixAndFailureOrder(t *testing.T) {
	request := Request{Protocol: 10, CallID: 0x11223344, Method: 1, Args: []byte{0xaa}}
	wire := mustRequest(t, request)
	expected := []byte{10, 0, 0, 0, 0x8a, 0x44, 0x33, 0x22, 0x11, 1, 0, 0, 0, 0xaa}
	if !bytes.Equal(wire, expected) {
		t.Fatalf("request wire %x", wire)
	}
	failure := ErrorReply(request, GenericFailure)
	if !bytes.Equal(failure, []byte{10, 0, 0, 0, 10, 0, 2, 0, 1, 0x80, 0x44, 0x33, 0x22, 0x11}) {
		t.Fatalf("failure ordering %x", failure)
	}
	for n := 0; n < len(wire); n++ {
		if _, err := ParseRequest(wire[:n]); err == nil {
			t.Fatalf("short record %d", n)
		}
	}
	altered := bytes.Clone(wire)
	binary.LittleEndian.PutUint32(altered, 0xffffffff)
	if _, err := ParseRequest(altered); err == nil {
		t.Fatal("declared huge size")
	}
}

func FuzzRMCBoundaries(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{9, 0, 0, 0, 0x8a, 1, 0, 0, 0, 1, 0, 0, 0})
	f.Fuzz(func(t *testing.T, wire []byte) {
		if request, err := ParseRequest(wire); err == nil {
			encoded, err := EncodeRequest(request)
			if err != nil || !bytes.Equal(encoded, wire) {
				t.Fatal("noncanonical request")
			}
		}
		_, _ = ParseResponse(wire)
		r := reader{data: wire}
		_ = r.str(512)
		_ = r.buffer(1024)
		_ = r.u32()
	})
}
