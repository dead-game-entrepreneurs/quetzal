// Transport integration cases adapted from sibling tdu-server.
package prudp

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type testHandler struct {
	connectCount atomic.Int32
	messages     chan []byte
	disconnected chan Peer
	reject       bool
	respond      func([]byte) [][]byte
	push         func(Peer, []byte) []OutboundMessage
	outbox       []OutboundMessage
	async        chan OutboundMessage
}

func (h *testHandler) Connect(_ context.Context, _ Peer, b []byte) (ConnectResult, error) {
	h.connectCount.Add(1)
	if h.reject || bytes.Equal(b, []byte("rejected")) {
		return ConnectResult{}, errors.New("invalid secure ticket")
	}
	return ConnectResult{Payload: append([]byte("accepted:"), b...)}, nil
}
func (h *testHandler) Handle(_ context.Context, peer Peer, b []byte) ([][]byte, error) {
	h.messages <- append([]byte(nil), b...)
	if h.push != nil {
		h.outbox = append(h.outbox, h.push(peer, b)...)
	}
	if h.respond != nil {
		return h.respond(b), nil
	}
	return nil, nil
}
func (h *testHandler) Disconnect(p Peer) { h.disconnected <- p }
func (h *testHandler) DrainOutbound() []OutboundMessage {
	out := h.outbox
	h.outbox = nil
	if h.async != nil {
		select {
		case message := <-h.async:
			out = append(out, message)
		default:
		}
	}
	return out
}

func TestIdleTransportDrainsAsynchronousIntroduction(t *testing.T) {
	queued := make(chan OutboundMessage, 1)
	registered := make(chan Peer, 1)
	s, _ := startTestServer(t, func(o *Options) {
		h := o.Handler.(*testHandler)
		h.async = queued
		h.push = func(peer Peer, _ []byte) []OutboundMessage { registered <- peer; return nil }
	})
	c := newClient(t, s)
	c.connect(t)
	c.send(t, c.data(2, 0, []byte("registered")))
	c.read(t)
	target := <-registered
	queued <- OutboundMessage{Peer: target, Payload: []byte("asynchronous relay introduction")}
	p, _ := c.read(t)
	plain, err := decodePayload(p.Payload, 1000)
	if err != nil || string(plain) != "asynchronous relay introduction" {
		t.Fatal("idle outbound delivery", err)
	}
	c.ack(t, p)
}
func startTestServer(t *testing.T, adjust func(*Options)) (*Server, *testHandler) {
	t.Helper()
	h := &testHandler{messages: make(chan []byte, 16), disconnected: make(chan Peer, 16)}
	opts := Options{ChecksumSeed: 0xff, Bind: netip.MustParseAddrPort("127.0.0.1:0"), Handler: h, RetryInterval: 30 * time.Millisecond, IdleTimeout: 2 * time.Second}
	if adjust != nil {
		adjust(&opts)
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		s.Close()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("Serve did not stop")
		}
	})
	return s, h
}

type wireClient struct {
	conn          *net.UDPConn
	cookie        uint32
	session       byte
	serverSession byte
}

func newClient(t *testing.T, s *Server) *wireClient {
	t.Helper()
	c, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(s.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &wireClient{conn: c, session: 7}
}
func (c *wireClient) send(t *testing.T, p Packet) []byte {
	t.Helper()
	b, err := p.Marshal(0xff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.conn.Write(b); err != nil {
		t.Fatal(err)
	}
	return b
}
func (c *wireClient) read(t *testing.T) (Packet, []byte) {
	t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 65535)
	n, err := c.conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	p, used, err := ParseFrame(buf[:n], 0xff)
	if err != nil || used != n {
		t.Fatal(used, err)
	}
	return p, append([]byte(nil), buf[:n]...)
}
func (c *wireClient) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(d))
	b := make([]byte, 65535)
	n, err := c.conn.Read(b)
	if err == nil {
		t.Fatalf("unexpected datagram %x", b[:n])
	}
	if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatal(err)
	}
}
func (c *wireClient) syn(t *testing.T) {
	c.send(t, Packet{Source: 0x31, Destination: 0x31, Type: SYN, Flags: FlagNeedsAck, Session: c.session})
	p, _ := c.read(t)
	if p.Type != SYN || p.Flags != FlagAck || p.Signature != 0 || p.Session != 0 || p.Sequence != 0 {
		t.Fatalf("SYNACK %+v", p)
	}
	c.cookie = p.ConnectionSignature
}
func (c *wireClient) requestConnect() Packet {
	return Packet{Source: 0x31, Destination: 0x31, Type: CONNECT, Flags: FlagReliable | FlagNeedsAck, Session: c.session, Signature: c.cookie, Sequence: 1, ConnectionSignature: 0x12345678, Payload: encodePayload([]byte("ticket"))}
}
func (c *wireClient) connect(t *testing.T) []byte {
	c.syn(t)
	c.send(t, c.requestConnect())
	p, wire := c.read(t)
	plain, err := decodePayload(p.Payload, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != CONNECT || p.Flags != FlagAck || p.Signature != 0x12345678 || p.Session == 0 || p.Sequence != 1 || !bytes.Equal(plain, []byte("accepted:ticket")) {
		t.Fatalf("CONNECTACK %+v %q", p, plain)
	}
	c.serverSession = p.Session
	return wire
}
func (c *wireClient) data(seq uint16, fragment byte, b []byte) Packet {
	return Packet{Source: 0x31, Destination: 0x31, Type: DATA, Flags: FlagReliable | FlagNeedsAck | FlagSize, Signature: c.cookie, Session: c.session, Sequence: seq, Fragment: fragment, Payload: encodePayload(b)}
}
func (c *wireClient) ack(t *testing.T, p Packet) {
	c.send(t, Packet{Source: 0x31, Destination: 0x31, Type: p.Type, Flags: FlagAck, Session: c.session, Signature: c.cookie, Sequence: p.Sequence, Fragment: p.Fragment})
}
func TestHandshakeCookieAdmissionAndDuplicate(t *testing.T) {
	s, h := startTestServer(t, nil)
	c := newClient(t, s)
	c.syn(t)
	p := c.requestConnect()
	p.Signature++
	c.send(t, p)
	c.quiet(t, 25*time.Millisecond)
	if h.connectCount.Load() != 0 {
		t.Fatal("handler ran before cookie validation")
	}
	c.send(t, c.requestConnect())
	_, first := c.read(t)
	c.send(t, c.requestConnect())
	_, second := c.read(t)
	if !bytes.Equal(first, second) || h.connectCount.Load() != 1 {
		t.Fatal("duplicate CONNECT was not idempotent")
	}
	bad := c.data(2, 0, []byte("never"))
	bad.Session++
	c.send(t, bad)
	c.quiet(t, 25*time.Millisecond)
	select {
	case <-h.messages:
		t.Fatal("wrong session delivered")
	default:
	}
}

func TestServerInitiatedMessageUsesTargetConnectionAndReliability(t *testing.T) {
	var target Peer // only accessed by the transport's handler loop
	s, _ := startTestServer(t, func(o *Options) {
		o.Handler.(*testHandler).push = func(peer Peer, b []byte) []OutboundMessage {
			switch string(b) {
			case "target":
				target = peer
			case "probe":
				return []OutboundMessage{{Peer: target, Payload: []byte("probe instruction")}}
			case "stale":
				stale := target
				stale.ID += 100
				return []OutboundMessage{{Peer: stale, Payload: []byte("never")}}
			}
			return nil
		}
	})
	host, guest := newClient(t, s), newClient(t, s)
	host.connect(t)
	guest.connect(t)
	host.send(t, host.data(2, 0, []byte("target")))
	host.read(t) // target request ACK
	guest.send(t, guest.data(2, 0, []byte("probe")))
	guest.read(t) // guest request ACK
	first, wire := host.read(t)
	plain, err := decodePayload(first.Payload, 1000)
	if err != nil || string(plain) != "probe instruction" || first.Session != host.serverSession || first.Signature != 0x12345678 {
		t.Fatal("wrong target connection envelope", err)
	}
	_, retry := host.read(t)
	if !bytes.Equal(wire, retry) {
		t.Fatal("server message retry changed")
	}
	host.ack(t, first)
	host.quiet(t, 70*time.Millisecond)
	guest.send(t, guest.data(3, 0, []byte("stale")))
	guest.read(t)
	host.quiet(t, 40*time.Millisecond)
}
func TestConnectCannotFakeTicketAcceptance(t *testing.T) {
	s, h := startTestServer(t, func(o *Options) { o.Handler.(*testHandler).reject = true })
	c := newClient(t, s)
	c.syn(t)
	c.send(t, c.requestConnect())
	c.quiet(t, 30*time.Millisecond)
	if h.connectCount.Load() != 1 {
		t.Fatal("CONNECT handler not called")
	}
	c.send(t, c.data(2, 0, []byte("unauthorized")))
	c.quiet(t, 30*time.Millisecond)
	select {
	case <-h.messages:
		t.Fatal("rejected connection dispatched")
	default:
	}
}

func TestRebootReplacesEndpointOnlyAfterFreshValidatedHandshake(t *testing.T) {
	s, h := startTestServer(t, func(o *Options) { o.MaxPeers = 1 })
	c := newClient(t, s)
	c.connect(t)
	oldConnect := c.requestConnect()
	oldData := c.data(2, 0, []byte("old"))
	// A different CONNECT without a fresh challenge cannot replace the peer.
	c.session++
	c.send(t, c.requestConnect())
	c.quiet(t, 20*time.Millisecond)
	c.syn(t)
	newCookie := c.cookie
	c.syn(t)
	if c.cookie != newCookie || c.cookie == oldConnect.Signature {
		t.Fatal("fresh challenge missing or SYN retry changed it")
	}
	select {
	case <-h.disconnected:
		t.Fatal("SYN dropped the active connection")
	default:
	}
	// Malformed proof must also leave the active connection intact.
	bad := c.requestConnect()
	bad.Payload = []byte{0xff}
	c.send(t, bad)
	c.quiet(t, 20*time.Millisecond)
	if h.connectCount.Load() != 1 {
		t.Fatal("malformed proof reached handler")
	}
	c.send(t, c.requestConnect())
	_, first := c.read(t)
	select {
	case <-h.disconnected:
	case <-time.After(time.Second):
		t.Fatal("old peer was not removed after replacement")
	}
	c.send(t, c.requestConnect())
	_, second := c.read(t)
	if h.connectCount.Load() != 2 || !bytes.Equal(first, second) {
		t.Fatal("replacement was not idempotent")
	}
	// Neither old CONNECT nor DATA can take over the replacement.
	c.send(t, oldConnect)
	c.send(t, oldData)
	c.quiet(t, 20*time.Millisecond)
	c.send(t, c.data(2, 0, []byte("new")))
	c.read(t)
	select {
	case got := <-h.messages:
		if string(got) != "new" {
			t.Fatal("old traffic delivered")
		}
	case <-time.After(time.Second):
		t.Fatal("replacement did not deliver data")
	}
}

func TestReconnectRejectedProofKeepsActivePeer(t *testing.T) {
	s, h := startTestServer(t, func(o *Options) { o.OpaquePayload = true })
	c := newClient(t, s)
	c.syn(t)
	p := c.requestConnect()
	p.Payload = []byte("accepted")
	c.send(t, p)
	c.read(t)
	oldCookie := c.cookie
	c.session++
	c.syn(t)
	p = c.requestConnect()
	p.Payload = []byte("rejected")
	// The handler rejects this payload without mutating its shared test state.
	// See testHandler.Connect for the synthetic rejected-proof sentinel.
	c.send(t, p)
	c.quiet(t, 20*time.Millisecond)
	select {
	case <-h.disconnected:
		t.Fatal("rejected replacement dropped active peer")
	default:
	}
	c.cookie, c.session = oldCookie, c.session-1
	p = c.data(2, 0, nil)
	p.Payload = []byte("still active")
	c.send(t, p)
	c.read(t)
	select {
	case got := <-h.messages:
		if string(got) != "still active" {
			t.Fatal("unexpected data")
		}
	case <-time.After(time.Second):
		t.Fatal("active peer was lost")
	}
}
func TestReliableReorderFragmentsDuplicatesAndPing(t *testing.T) {
	s, h := startTestServer(t, nil)
	c := newClient(t, s)
	c.connect(t)
	// The final fragment arrives first, and must wait for the missing sequence.
	c.send(t, c.data(3, 0, []byte("world")))
	p, _ := c.read(t)
	if p.Type != DATA || p.Flags != FlagAck || p.Sequence != 3 {
		t.Fatal(p)
	}
	select {
	case <-h.messages:
		t.Fatal("out-of-order fragment dispatched")
	default:
	}
	first := c.data(2, 1, []byte("hello "))
	c.send(t, first)
	p, _ = c.read(t)
	if p.Sequence != 2 || p.Fragment != 1 {
		t.Fatal(p)
	}
	select {
	case b := <-h.messages:
		if string(b) != "hello world" {
			t.Fatalf("assembled %q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("missing assembled message")
	}
	c.send(t, first)
	c.read(t)
	select {
	case <-h.messages:
		t.Fatal("duplicate delivered twice")
	default:
	}
	// Reliable control packets consume a reliable sequence, even without RMC.
	c.send(t, Packet{Source: 0x31, Destination: 0x31, Type: PING, Flags: FlagReliable | FlagNeedsAck, Signature: c.cookie, Session: c.session, Sequence: 4})
	p, _ = c.read(t)
	if p.Type != PING || p.Flags != FlagAck {
		t.Fatal(p)
	}
	c.send(t, c.data(5, 0, []byte("after ping")))
	c.read(t)
	select {
	case b := <-h.messages:
		if string(b) != "after ping" {
			t.Fatalf("got%q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("PING stalled reliable queue")
	}
	c.send(t, Packet{Source: 0x31, Destination: 0x31, Type: DISCONNECT, Flags: FlagNeedsAck, Signature: c.cookie, Session: c.session, Sequence: 6})
	p, _ = c.read(t)
	if p.Type != DISCONNECT || p.Flags != FlagAck {
		t.Fatal(p)
	}
	select {
	case peer := <-h.disconnected:
		if peer.LocalPort != s.Addr().Port() || peer.ID == 0 {
			t.Fatal(peer)
		}
	case <-time.After(time.Second):
		t.Fatal("Disconnect callback missing")
	}
}
func TestRetransmitAckAndTimeout(t *testing.T) {
	s, h := startTestServer(t, func(o *Options) {
		o.MaxRetries = 1
		o.RetryInterval = 20 * time.Millisecond
		o.Handler.(*testHandler).respond = func(b []byte) [][]byte { return [][]byte{append([]byte("reply:"), b...)} }
	})
	c := newClient(t, s)
	c.connect(t)
	c.send(t, c.data(2, 0, []byte("request")))
	ack, _ := c.read(t)
	if ack.Flags != FlagAck {
		t.Fatal(ack)
	}
	response, wire := c.read(t)
	if response.Type != DATA || response.Sequence != 1 || response.Flags&(FlagReliable|FlagNeedsAck) != (FlagReliable|FlagNeedsAck) {
		t.Fatal(response)
	}
	plain, err := decodePayload(response.Payload, 1000)
	if err != nil || string(plain) != "reply:request" {
		t.Fatal(string(plain), err)
	}
	_, retry := c.read(t)
	if !bytes.Equal(wire, retry) {
		t.Fatal("retry changed wire bytes")
	}
	c.ack(t, response)
	c.quiet(t, 70*time.Millisecond)
	c.send(t, c.data(3, 0, []byte("unacknowledged")))
	c.read(t)
	c.read(t)
	c.read(t)
	select {
	case <-h.disconnected:
	case <-time.After(time.Second):
		t.Fatal("retry exhaustion did not close peer")
	}
}
func TestLimitsIdleAndUnsupported(t *testing.T) {
	s, h := startTestServer(t, func(o *Options) { o.MaxPeers = 1; o.IdleTimeout = 100 * time.Millisecond; o.MaxMessageSize = 32 })
	c := newClient(t, s)
	c.connect(t)
	other := newClient(t, s)
	other.syn(t)
	other.send(t, other.requestConnect())
	other.quiet(t, 20*time.Millisecond)
	if h.connectCount.Load() != 1 {
		t.Fatal("peer cap ignored")
	}
	bad := c.data(2, 0, []byte("ignored"))
	bad.Flags |= 0x80
	c.send(t, bad)
	bad.Flags &^= 0x80
	bad.Source = 0x21
	c.send(t, bad)
	select {
	case <-h.messages:
		t.Fatal("unsupported packet dispatched")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case <-h.disconnected:
	case <-time.After(time.Second):
		t.Fatal("idle peer not removed")
	}
	other.send(t, other.requestConnect())
	other.read(t)
	if h.connectCount.Load() != 2 {
		t.Fatal("expired capacity not reusable")
	}
}
func TestFragmentAndWindowBounds(t *testing.T) {
	s, h := startTestServer(t, func(o *Options) { o.MaxInFlight = 4; o.MaxMessageSize = 32 })
	c := newClient(t, s)
	c.connect(t)
	c.send(t, c.data(7, 0, []byte("far ahead")))
	c.quiet(t, 25*time.Millisecond)
	c.send(t, c.data(2, 1, bytes.Repeat([]byte{'a'}, 20)))
	c.read(t)
	// Individually bounded fragments cannot exceed the complete message cap.
	c.send(t, c.data(3, 0, bytes.Repeat([]byte{'b'}, 20)))
	c.quiet(t, 25*time.Millisecond)
	select {
	case <-h.messages:
		t.Fatal("oversized assembly delivered")
	default:
	}
}

func TestCompressedRequestAndFragmentedResponse(t *testing.T) {
	expected := bytes.Repeat([]byte("response"), 400)
	s, h := startTestServer(t, func(o *Options) {
		o.Handler.(*testHandler).respond = func([]byte) [][]byte { return [][]byte{expected} }
	})
	c := newClient(t, s)
	c.connect(t)
	var fixtures []struct{ Encoded string }
	raw, err := os.ReadFile("testdata/lzo1x.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	lzo, err := hex.DecodeString(fixtures[3].Encoded)
	if err != nil {
		t.Fatal(err)
	}
	request := c.data(2, 0, nil)
	request.Payload = cryptPayload(append([]byte{1}, lzo...))
	c.send(t, request)
	ack, _ := c.read(t)
	if ack.Flags != FlagAck {
		t.Fatal(ack)
	}
	select {
	case b := <-h.messages:
		if !bytes.Equal(b, bytes.Repeat([]byte("abc"), 100)) {
			t.Fatal("compressed request mismatch")
		}
	case <-time.After(time.Second):
		t.Fatal("missing compressed request")
	}
	var assembled []byte
	for i := 1; i <= 4; i++ {
		p, _ := c.read(t)
		if p.Type != DATA || p.Flags&FlagAck != 0 || p.Sequence != uint16(i) {
			t.Fatal(p)
		}
		wantFragment := byte(i)
		if i == 4 {
			wantFragment = 0
		}
		if p.Fragment != wantFragment {
			t.Fatal("fragment", p.Fragment)
		}
		b, err := decodePayload(p.Payload, 1000)
		if err != nil {
			t.Fatal(err)
		}
		assembled = append(assembled, b...)
		c.ack(t, p)
	}
	if !bytes.Equal(assembled, expected) {
		t.Fatal("fragmented reply mismatch")
	}
}
func TestConcatenatedDatagramAndAssemblyTimeout(t *testing.T) {
	s, h := startTestServer(t, func(o *Options) { o.HandshakeTimeout = 35 * time.Millisecond })
	c := newClient(t, s)
	c.connect(t)
	a, _ := c.data(2, 0, []byte("one")).Marshal(0xff)
	b, _ := c.data(3, 0, []byte("two")).Marshal(0xff)
	if _, err := c.conn.Write(append(a, b...)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		c.read(t)
	}
	for _, want := range []string{"one", "two"} {
		select {
		case b := <-h.messages:
			if string(b) != want {
				t.Fatal(string(b), want)
			}
		case <-time.After(time.Second):
			t.Fatal("missing packed request")
		}
	}
	c.send(t, c.data(4, 1, []byte("unfinished")))
	c.read(t)
	select {
	case <-h.disconnected:
	case <-time.After(time.Second):
		t.Fatal("unfinished assembly did not expire")
	}
}

func TestCancellationBeforeServeReleasesSocket(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		s, err := New(Options{Bind: netip.MustParseAddrPort("127.0.0.1:0"), Handler: &testHandler{}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		address := s.Addr()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if closeFirst {
			s.Close()
		}
		if err := s.Serve(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("closeFirst=%v: canceled startup returned %v", closeFirst, err)
		}
		conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(address))
		if err != nil {
			t.Fatal("canceled startup retained its socket", err)
		}
		conn.Close()
	}
}

func TestImplicitPayloadSizeFragmentedResponse(t *testing.T) {
	expected := bytes.Repeat([]byte("older SDK packet"), 20)
	s, _ := startTestServer(t, func(o *Options) {
		o.ImplicitPayloadSize = true
		o.MaxDatagramSize = 96
		o.Handler.(*testHandler).respond = func([]byte) [][]byte { return [][]byte{expected} }
	})
	c := newClient(t, s)
	c.connect(t)
	c.send(t, c.data(2, 0, []byte("request")))
	if ack, _ := c.read(t); ack.Flags != FlagAck {
		t.Fatal("request acknowledgement")
	}
	var assembled []byte
	for seq := uint16(1); ; seq++ {
		p, wire := c.read(t)
		if p.Type != DATA || p.Flags&FlagSize != 0 || p.Sequence != seq || len(wire) > 96 {
			t.Fatal("implicit packet layout", p, len(wire))
		}
		// Native 0x2fd088 puts payload immediately after DATA's fragment byte.
		if !bytes.Equal(wire[11:len(wire)-1], p.Payload) {
			t.Fatal("extra bytes precede the payload")
		}
		b, err := decodePayload(p.Payload, 1000)
		if err != nil {
			t.Fatal(err)
		}
		assembled = append(assembled, b...)
		c.ack(t, p)
		if p.Fragment == 0 {
			break
		}
		if p.Fragment != byte(seq) || seq > 10 {
			t.Fatal("fragment order", p)
		}
	}
	if !bytes.Equal(assembled, expected) {
		t.Fatal("implicit fragment reassembly")
	}
}
