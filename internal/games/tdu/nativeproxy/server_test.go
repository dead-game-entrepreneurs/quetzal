package nativeproxy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/proxycrypto"
)

func startServer(t *testing.T, change func(*Options)) (*Server, context.CancelFunc, <-chan error) {
	t.Helper()
	opts := Options{MasterBind: netip.MustParseAddrPort("127.0.0.1:0"), ProxyBind: netip.MustParseAddrPort("127.0.0.1:0"), PublicIP: netip.MustParseAddr("127.0.0.1"), CleanupInterval: 10 * time.Millisecond}
	if change != nil {
		change(&opts)
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(func() { cancel(); s.Close() })
	return s, cancel, done
}
func socket(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}
func send(t *testing.T, conn *net.UDPConn, to netip.AddrPort, wire []byte) {
	t.Helper()
	if _, err := conn.WriteToUDPAddrPort(wire, to); err != nil {
		t.Fatal(err)
	}
}
func receive(t *testing.T, conn *net.UDPConn) []byte {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	wire := make([]byte, 1600)
	n, _, err := conn.ReadFromUDPAddrPort(wire)
	if err != nil {
		t.Fatal(err)
	}
	return wire[:n]
}
func noReply(t *testing.T, conn *net.UDPConn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
	wire := make([]byte, 1600)
	_, _, err := conn.ReadFromUDPAddrPort(wire)
	if err == nil {
		t.Fatal("unexpected reply")
	}
	if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatalf("expected timeout: %v", err)
	}
}

// Test-side client messages use independent standard-library HMAC over the
// recovered transcript; only the shared protocol constant is repeated here.
func signClientMessage(command, session uint16, payload, context []byte) []byte {
	key, _ := hex.DecodeString("f2296c20e809a0cf98464de7b2ee75c2af1c24f50a83b3ce0d19d5c11cc1f70a")
	wire := control(session, command, payload)
	mac := hmac.New(sha256.New, key)
	mac.Write(context)
	mac.Write(wire)
	return append(wire, mac.Sum(nil)...)
}
func verifyServerMessage(t *testing.T, wire []byte, command, session uint16, context []byte) []byte {
	t.Helper()
	if len(wire) < 42 || binary.BigEndian.Uint16(wire[:2]) != 4 || binary.BigEndian.Uint16(wire[2:4]) != session || binary.BigEndian.Uint16(wire[4:6]) != command {
		t.Fatalf("bad server handshake: %x", wire)
	}
	payload := wire[10 : len(wire)-32]
	want := signClientMessage(command, session, payload, context)
	if !bytes.Equal(wire, want) {
		t.Fatalf("server transcript MAC mismatch: %x", wire)
	}
	return payload
}

type peer struct {
	conn                     *net.UDPConn
	server                   netip.AddrPort
	id                       uint16
	hello, keyPacket, finish []byte
	sender                   *proxycrypto.Sender
	receiver                 *proxycrypto.Receiver
}

func handshake(t *testing.T, s *Server) *peer {
	t.Helper()
	p := &peer{conn: socket(t), server: s.ProxyAddr()}
	var nonce [32]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	p.hello = proxycrypto.EncodeClientHello(proxycrypto.RegionUS, nonce)
	send(t, p.conn, p.server, p.hello)
	hello := receive(t, p.conn)
	p.id = binary.BigEndian.Uint16(hello[2:4])
	serverPublic := verifyServerMessage(t, hello, 2, p.id, nonce[:])
	if len(serverPublic) != 140 {
		t.Fatal("wrong server public key size")
	}
	send(t, p.conn, p.server, p.hello)
	if again := receive(t, p.conn); !bytes.Equal(again, hello) {
		t.Fatal("hello retry changed session or ephemeral key")
	}
	var exponent [128]byte
	exponent[127] = 3
	private, err := proxycrypto.NewDHPrivate(exponent[:])
	if err != nil {
		t.Fatal(err)
	}
	clientPublic := private.Public()
	transcript := append(bytes.Clone(nonce[:]), serverPublic...)
	p.keyPacket = signClientMessage(3, p.id, clientPublic[:], transcript)
	send(t, p.conn, p.server, p.keyPacket)
	p.finish = receive(t, p.conn)
	transcript = append(transcript, clientPublic[:]...)
	verifyServerMessage(t, p.finish, 8, p.id, transcript)
	secret, err := private.Shared(serverPublic)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := proxycrypto.DeriveKeys(secret)
	if err != nil {
		t.Fatal(err)
	}
	p.sender, _ = proxycrypto.NewSender(keys, proxycrypto.ClientToProxy)
	p.receiver, _ = proxycrypto.NewReceiver(keys, proxycrypto.ProxyToClient)
	return p
}
func (p *peer) packet(t *testing.T, command uint16, payload []byte) []byte {
	t.Helper()
	wire, err := p.sender.Seal(control(p.id, command, payload))
	if err != nil {
		t.Fatal(err)
	}
	return wire
}
func (p *peer) activate(t *testing.T) []byte {
	t.Helper()
	wire := p.packet(t, 29, nil)
	send(t, p.conn, p.server, wire)
	opened, _, err := p.receiver.Open(receive(t, p.conn))
	if err != nil || len(opened) != 10 || binary.BigEndian.Uint16(opened[4:6]) != 30 {
		t.Fatalf("activation reply: %x %v", opened, err)
	}
	return wire
}
func (p *peer) application(t *testing.T, to netip.AddrPort, payload []byte) []byte {
	t.Helper()
	plain := make([]byte, 10+len(payload))
	binary.BigEndian.PutUint16(plain[:2], 2)
	binary.BigEndian.PutUint16(plain[2:4], p.id)
	binary.BigEndian.PutUint16(plain[4:6], to.Port())
	ip := to.Addr().As4()
	copy(plain[6:10], ip[:])
	copy(plain[10:], payload)
	wire, err := p.sender.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestMasterBuildAssignmentAndUnsupportedBuild(t *testing.T) {
	s, _, _ := startServer(t, nil)
	conn := socket(t)
	request := proxycrypto.MasterRequest{Region: proxycrypto.RegionUS, Revision: proxycrypto.RetailRevision}
	send(t, conn, s.MasterAddr(), request.MarshalBinary())
	reply, err := proxycrypto.ParseMasterReply(receive(t, conn))
	if err != nil || reply.Proxy != s.opts.PublicIP || reply.DirectoryIP != s.opts.PublicIP {
		t.Fatalf("assignment %+v %v", reply, err)
	}
	request.Revision++
	send(t, conn, s.MasterAddr(), request.MarshalBinary())
	reply, err = proxycrypto.ParseMasterReply(receive(t, conn))
	if err != nil || !reply.Proxy.IsUnspecified() || !reply.DirectoryIP.IsUnspecified() {
		t.Fatalf("unsupported build %+v %v", reply, err)
	}
	send(t, conn, s.MasterAddr(), []byte{1, 2, 3})
	noReply(t, conn)
}
func TestHandshakeActivationAndRetriesRetainCounters(t *testing.T) {
	s, _, _ := startServer(t, nil)
	p := handshake(t, s)
	if stats := s.Stats(); stats.KeyExchanged != 1 || stats.Activated != 0 {
		t.Fatalf("before activation %+v", stats)
	}
	p.activate(t)
	send(t, p.conn, p.server, p.keyPacket)
	if finish := receive(t, p.conn); !bytes.Equal(finish, p.finish) {
		t.Fatal("key retry changed cached finish")
	}
	p.activate(t) // Receiver rejects this if the server reset its sender counter.
	if stats := s.Stats(); stats.Activated != 1 {
		t.Fatalf("after activation %+v", stats)
	}
	region := make([]byte, 4)
	binary.BigEndian.PutUint32(region, proxycrypto.RegionUS)
	send(t, p.conn, p.server, p.packet(t, 38, region))
	noReply(t, p.conn)
}
func TestTamperReplayEndpointAndPlaintextRejected(t *testing.T) {
	s, _, _ := startServer(t, nil)
	p := handshake(t, s)
	activation := p.activate(t)
	send(t, p.conn, p.server, activation)
	noReply(t, p.conn)
	fresh := p.packet(t, 29, nil)
	tampered := bytes.Clone(fresh)
	tampered[7] ^= 1
	send(t, p.conn, p.server, tampered)
	noReply(t, p.conn)
	other := socket(t)
	send(t, other, p.server, fresh)
	noReply(t, other)
	send(t, p.conn, p.server, control(p.id, 29, nil))
	noReply(t, p.conn)
	send(t, p.conn, p.server, fresh)
	if _, _, err := p.receiver.Open(receive(t, p.conn)); err != nil {
		t.Fatalf("rejections advanced state: %v", err)
	}
}
func TestActivationRequiresExactEmptyControl(t *testing.T) {
	s, _, _ := startServer(t, nil)
	p := handshake(t, s)
	send(t, p.conn, p.server, p.packet(t, 29, []byte{1}))
	noReply(t, p.conn)
	if s.Stats().Activated != 0 {
		t.Fatal("invalid activation changed transport state")
	}
	p.activate(t)
}
func TestOnlyRegisteredServerOwnedServicesRun(t *testing.T) {
	var calls atomic.Int32
	s, _, _ := startServer(t, func(o *Options) {
		o.Services = map[uint16]DataHandler{30000: func(ctx context.Context, info SessionInfo, to netip.AddrPort, payload []byte) ([]byte, error) {
			if !info.TransportActivated || info.ID == 0xffff || to != netip.MustParseAddrPort("127.0.0.1:30000") {
				return nil, errors.New("invalid service context")
			}
			calls.Add(1)
			if string(payload) == "write" {
				return nil, ErrUnsupported
			}
			return append([]byte("public:"), payload...), nil
		}}
	})
	p := handshake(t, s)
	to := netip.MustParseAddrPort("127.0.0.1:30000")
	send(t, p.conn, p.server, p.application(t, to, []byte("before")))
	noReply(t, p.conn)
	if calls.Load() != 0 {
		t.Fatal("application ran before transport activation")
	}
	p.activate(t)
	for _, bad := range []netip.AddrPort{netip.MustParseAddrPort("198.51.100.1:30000"), netip.MustParseAddrPort("127.0.0.1:30001")} {
		send(t, p.conn, p.server, p.application(t, bad, []byte("blocked")))
		noReply(t, p.conn)
	}
	if calls.Load() != 0 {
		t.Fatal("unregistered destination reached application")
	}
	send(t, p.conn, p.server, p.application(t, to, []byte("read")))
	opened, _, err := p.receiver.Open(receive(t, p.conn))
	if err != nil || binary.BigEndian.Uint16(opened[:2]) != 3 || !bytes.Equal(opened[10:], []byte("public:read")) || !bytes.Equal(opened[6:10], []byte{127, 0, 0, 1}) {
		t.Fatalf("service response %x %v", opened, err)
	}
	send(t, p.conn, p.server, p.application(t, to, []byte("write")))
	noReply(t, p.conn)
	if calls.Load() != 2 || s.Stats().ApplicationRequests != 1 {
		t.Fatalf("unexpected service accounting: calls=%d stats=%+v", calls.Load(), s.Stats())
	}
}
func TestExpiryCapacityAndFreshExchange(t *testing.T) {
	s, _, _ := startServer(t, func(o *Options) {
		o.MaxSessions = 1
		o.HandshakeTTL = 60 * time.Millisecond
		o.SessionTTL = 60 * time.Millisecond
		o.CleanupInterval = 5 * time.Millisecond
	})
	first := socket(t)
	second := socket(t)
	hello := proxycrypto.EncodeClientHello(proxycrypto.RegionUS, [32]byte{})
	send(t, first, s.ProxyAddr(), hello)
	initial := receive(t, first)
	send(t, second, s.ProxyAddr(), hello)
	noReply(t, second)
	deadline := time.Now().Add(time.Second)
	for s.Stats().Sessions != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Stats().Sessions != 0 {
		t.Fatal("pending exchange did not expire")
	}
	send(t, second, s.ProxyAddr(), hello)
	fresh := receive(t, second)
	if binary.BigEndian.Uint16(initial[2:4]) == binary.BigEndian.Uint16(fresh[2:4]) {
		t.Fatal("expired exchange reused its session ID")
	}
}
func TestContextCancellationAndClose(t *testing.T) {
	s, cancel, done := startServer(t, nil)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Serve(context.Background()); !errors.Is(err, ErrServing) {
		t.Fatalf("second Serve: %v", err)
	}
}
func TestNewRejectsInvalidOptionsAndClosesPartialBind(t *testing.T) {
	base := Options{MasterBind: netip.MustParseAddrPort("127.0.0.1:0"), ProxyBind: netip.MustParseAddrPort("127.0.0.1:0"), PublicIP: netip.MustParseAddr("127.0.0.1")}
	for _, change := range []func(*Options){func(o *Options) { o.PublicIP = netip.IPv4Unspecified() }, func(o *Options) { o.MaxSessions = -1 }, func(o *Options) { o.SessionTTL = -1 }, func(o *Options) {
		o.Services = map[uint16]DataHandler{0: func(context.Context, SessionInfo, netip.AddrPort, []byte) ([]byte, error) { return nil, nil }}
	}} {
		o := base
		change(&o)
		if s, err := New(o); err == nil {
			s.Close()
			t.Fatal("invalid options accepted")
		}
	}
	held := socket(t)
	base.ProxyBind = held.LocalAddr().(*net.UDPAddr).AddrPort()
	if s, err := New(base); err == nil {
		s.Close()
		t.Fatal("duplicate proxy bind accepted")
	}
}

func TestMasterDirectoryAddressOverride(t *testing.T) {
	directory := netip.MustParseAddr("203.0.113.77")
	s, _, _ := startServer(t, func(options *Options) { options.DirectoryIP = directory })
	conn := socket(t)
	send(t, conn, s.MasterAddr(), (proxycrypto.MasterRequest{Region: proxycrypto.RegionUS, Revision: proxycrypto.RetailRevision}).MarshalBinary())
	wire := receive(t, conn)
	if len(wire) != 8 || !bytes.Equal(wire[4:], []byte{203, 0, 113, 77}) {
		t.Fatalf("directory wire byte order %x", wire)
	}
	reply, err := proxycrypto.ParseMasterReply(wire)
	if err != nil || reply.Proxy != s.opts.PublicIP || reply.DirectoryIP != directory {
		t.Fatalf("separate proxy/directory assignment %+v %v", reply, err)
	}
	for _, address := range []string{"0.0.0.0", "::1", "239.1.2.3", "255.255.255.255"} {
		_, err := New(Options{MasterBind: netip.MustParseAddrPort("127.0.0.1:0"), ProxyBind: netip.MustParseAddrPort("127.0.0.1:0"), PublicIP: netip.MustParseAddr("127.0.0.1"), DirectoryIP: netip.MustParseAddr(address)})
		if err == nil {
			t.Fatalf("accepted unusable directory address %s", address)
		}
	}
}
