package wcc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

// client speaks the retail byte shapes over real loopback UDP: checksum seed
// 0x2b and the plain selector-0 payload wrapper (no transport RC4).
type client struct {
	t        *testing.T
	conn     *net.UDPConn
	cookie   uint32
	session  byte
	sequence uint16
	call     uint32
}

func wrap(plain []byte) []byte {
	if len(plain) == 0 {
		return nil
	}
	return append([]byte{0}, plain...)
}

func (c *client) send(p prudp.Packet, seed byte) {
	c.t.Helper()
	p.Source, p.Destination = 0x3f, 0x31
	if p.Type != prudp.SYN {
		p.Signature, p.Session = c.cookie, c.session
	}
	wire, err := p.Marshal(seed)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.conn.Write(wire); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) read(timeout time.Duration) (prudp.Packet, bool) {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	wire := make([]byte, 65535)
	n, err := c.conn.Read(wire)
	if err != nil {
		return prudp.Packet{}, false
	}
	p, used, err := prudp.ParseFrame(wire[:n], ChecksumSeed)
	if err != nil || used != n {
		c.t.Fatalf("server frame did not use seed 0x2b: %x %v", wire[:n], err)
	}
	return p, true
}

func dial(t *testing.T, addr netip.AddrPort, security []byte) (*client, []byte) {
	t.Helper()
	conn, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(addr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := &client{t: t, conn: conn, session: 0x27, sequence: 2}
	c.send(prudp.Packet{Type: prudp.SYN, Flags: prudp.FlagNeedsAck}, ChecksumSeed)
	syn, ok := c.read(2 * time.Second)
	if !ok || syn.Type != prudp.SYN || syn.Flags&prudp.FlagAck == 0 {
		t.Fatalf("SYN reply %+v", syn)
	}
	c.cookie = syn.ConnectionSignature
	c.send(prudp.Packet{Type: prudp.CONNECT, Flags: prudp.FlagReliable | prudp.FlagNeedsAck, Sequence: 1, ConnectionSignature: 0x12345678, Payload: wrap(security)}, ChecksumSeed)
	ack, ok := c.read(2 * time.Second)
	if !ok || ack.Type != prudp.CONNECT || ack.Flags&prudp.FlagAck == 0 {
		t.Fatalf("CONNECT reply %+v", ack)
	}
	if len(ack.Payload) == 0 {
		return c, nil
	}
	if ack.Payload[0] != 0 {
		t.Fatalf("server used compression selector %d", ack.Payload[0])
	}
	return c, ack.Payload[1:]
}

func (c *client) exchange(protocol byte, method uint32, args []byte) quazal.Response {
	c.t.Helper()
	c.call++
	message, err := quazal.EncodeRequest(quazal.Request{Protocol: protocol, Method: method, CallID: c.call, Args: args})
	if err != nil {
		c.t.Fatal(err)
	}
	c.send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagReliable | prudp.FlagNeedsAck | prudp.FlagSize, Sequence: c.sequence, Payload: wrap(message)}, ChecksumSeed)
	c.sequence++
	for range 8 {
		p, ok := c.read(2 * time.Second)
		if !ok {
			break
		}
		if p.Flags&prudp.FlagAck != 0 {
			continue
		}
		if p.Type != prudp.DATA || p.Fragment != 0 || len(p.Payload) == 0 || p.Payload[0] != 0 {
			c.t.Fatalf("unexpected RPC packet %+v", p)
		}
		c.send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagAck, Sequence: p.Sequence}, ChecksumSeed)
		reply, err := quazal.ParseResponse(p.Payload[1:])
		if err != nil {
			c.t.Fatal(err)
		}
		if reply.Protocol != protocol || reply.CallID != c.call {
			c.t.Fatalf("mismatched reply %+v", reply)
		}
		return reply
	}
	c.t.Fatalf("no reply to %d/%d", protocol, method)
	return quazal.Response{}
}

// serverCall reads one server-initiated request, acknowledges the transport
// and answers it with an empty success, as the retail dispatcher does for 3/2.
func (c *client) serverCall() quazal.Request {
	c.t.Helper()
	for range 8 {
		p, ok := c.read(3 * time.Second)
		if !ok {
			break
		}
		if p.Flags&prudp.FlagAck != 0 || p.Type != prudp.DATA {
			continue
		}
		c.send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagAck, Sequence: p.Sequence}, ChecksumSeed)
		request, err := quazal.ParseRequest(p.Payload[1:])
		if err != nil {
			c.t.Fatalf("server request %x: %v", p.Payload, err)
		}
		reply, err := quazal.SuccessReply(request, nil)
		if err != nil {
			c.t.Fatal(err)
		}
		c.send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagReliable | prudp.FlagNeedsAck | prudp.FlagSize, Sequence: c.sequence, Payload: wrap(reply)}, ChecksumSeed)
		c.sequence++
		return request
	}
	c.t.Fatal("no server-initiated request")
	return quazal.Request{}
}

func (c *client) rpc(protocol byte, method uint32, args []byte) []byte {
	c.t.Helper()
	reply := c.exchange(protocol, method, args)
	if !reply.Success {
		c.t.Fatalf("%d/%d failed: %+v", protocol, method, reply)
	}
	return reply.Payload
}

func str(v string) []byte        { return putString(nil, v) }
func u32(v uint32) []byte        { return putU32(nil, v) }
func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func buffer(t *testing.T, b []byte) ([]byte, []byte) {
	t.Helper()
	if len(b) < 4 || int(binary.LittleEndian.Uint32(b)) > len(b)-4 {
		t.Fatal("truncated Buffer")
	}
	n := int(binary.LittleEndian.Uint32(b))
	return b[4 : 4+n], b[4+n:]
}

type adapter struct {
	auth, secure netip.AddrPort
}

func start(t *testing.T) adapter {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	conns := make([]*net.UDPConn, 2)
	for i := range conns {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		conns[i] = conn
	}
	authPort := uint16(conns[0].LocalAddr().(*net.UDPAddr).Port)
	securePort := uint16(conns[1].LocalAddr().(*net.UDPAddr).Port)
	var key quazalcrypto.Key
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	def, err := definition(log, netip.MustParseAddr("127.0.0.1"), authPort, securePort, t.TempDir(), key)
	if err != nil {
		t.Fatal(err)
	}
	if def.Key != "wcc" || len(def.UDPPorts) != 2 || def.UDPPorts[0] != int(authPort) || def.UDPPorts[1] != int(securePort) {
		t.Fatalf("definition %+v", def)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	for _, conn := range conns {
		service, err := def.NewUDP(conn, nil)
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = service.Serve(ctx); done <- struct{}{} }()
		t.Cleanup(func() { _ = service.Close() })
	}
	t.Cleanup(func() {
		cancel()
		for _, conn := range conns {
			_ = conn.Close()
		}
		for range conns {
			<-done
		}
	})
	return adapter{auth: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), authPort), secure: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), securePort)}
}

// login performs 10/1 and 10/3 and returns the PID and service ticket.
func login(t *testing.T, a adapter, name, password string) (uint32, quazalcrypto.ServiceTicketEnvelope) {
	t.Helper()
	c, _ := dial(t, a.auth, nil)
	reply := c.rpc(10, 1, str(name))
	if binary.LittleEndian.Uint32(reply) != quazal.Success {
		t.Fatal("login result")
	}
	pid := binary.LittleEndian.Uint32(reply[4:])
	proof, rest := buffer(t, reply[8:])
	r := reader{b: rest}
	url := r.str(512)
	if !strings.HasPrefix(url, "prudps:/") || !strings.Contains(url, ";port="+strconv.Itoa(int(a.secure.Port()))+";") {
		t.Fatalf("secure URL %q", url)
	}
	if _, err := quazalcrypto.Open(quazalcrypto.PasswordKey([]byte(password), pid), proof); err != nil {
		t.Fatalf("login proof: %v", err)
	}
	reply = c.rpc(10, 3, cat(u32(pid), u32(2)))
	sealed, rest := buffer(t, reply[4:])
	if len(rest) != 0 {
		t.Fatal("ticket trailing bytes")
	}
	envelope, err := quazalcrypto.OpenServiceTicket(quazalcrypto.PasswordKey([]byte(password), pid), sealed)
	if err != nil {
		t.Fatal(err)
	}
	return pid, envelope
}

func secure(t *testing.T, a adapter, pid uint32, ticket quazalcrypto.ServiceTicketEnvelope, nonce uint32, port int) (*client, string) {
	t.Helper()
	payload, err := quazalcrypto.BuildConnect(ticket.Ticket, ticket.SessionKey, quazalcrypto.Challenge{PrincipalID: pid, Nonce: nonce})
	if err != nil {
		t.Fatal(err)
	}
	c, reply := dial(t, a.secure, payload)
	if err := quazalcrypto.VerifyConnectReply(reply, nonce); err != nil {
		t.Fatal(err)
	}
	local := "prudp:/address=192.168.1.20;port=" + strconv.Itoa(port) + ";stream=3;sid=15;type=2"
	got := c.rpc(11, 1, cat(u32(1), str(local)))
	if binary.LittleEndian.Uint32(got) != quazal.Success {
		t.Fatal("registration failed")
	}
	r := reader{b: got[8:]}
	url := r.str(512)
	if !strings.HasPrefix(url, "prudp:/") || !strings.Contains(url, "PID="+strconv.FormatUint(uint64(pid), 10)) {
		t.Fatalf("registered URL %q", url)
	}
	return c, url
}

// signup creates a named account from a registered guest, as JobCreateAccount does.
func signup(t *testing.T, a adapter, name, password string) {
	t.Helper()
	pid, ticket := login(t, a, "guest", "h7fyctiuucf")
	guest, _ := secure(t, a, pid, ticket, 0x1000+uint32(len(name)), 3658)
	if got := guest.rpc(18, 1, nil); !bytes.Equal(got, []byte{1}) {
		t.Fatalf("guest health ping %x", got)
	}
	if guest.exchange(21, 17, cat(str("WCCGathering"), u32(0), u32(10))).Success {
		t.Fatal("guest reached matchmaking")
	}
	initial := quazalcrypto.InitialKey([]byte(password))
	args := cat(str(name), str(hex.EncodeToString(initial[:])), u32(0x20), str("qwerty@keyboard.com"))
	if got := guest.rpc(25, 1, args); !bytes.Equal(got, u32(quazal.Success)) {
		t.Fatalf("signup %x", got)
	}
}

func wccGathering(id, max uint16, private bool) []byte {
	body := cat(u32(uint32(id)), u32(0), u32(0))
	body = binary.LittleEndian.AppendUint16(body, 2)
	body = binary.LittleEndian.AppendUint16(body, max)
	body = cat(body, u32(0), u32(0), u32(0), u32(0), str("Howard's Game"))
	// gameRules, eyeToyOnly, pad, timeout, isPrivate, rating, numPlayers(i16),
	// isTournament, zStarted, strPassword[16]
	body = append(body, 3, 0, 1, 30, putBool(nil, private)[0], 0, 1, 0, 0, 0)
	body = append(body, make([]byte, 16)...)
	return putAny(nil, quazal.AnyData{Type: "WCCGathering", Payload: cat(u32(uint32(len(body))), body)})
}

func TestRetailFlowOverUDP(t *testing.T) {
	a := start(t)

	// A frame checksummed with another title's seed is dropped.
	stray, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(a.auth))
	if err != nil {
		t.Fatal(err)
	}
	defer stray.Close()
	probe := &client{t: t, conn: stray}
	probe.send(prudp.Packet{Type: prudp.SYN, Flags: prudp.FlagNeedsAck}, 0xff)
	if _, ok := probe.read(300 * time.Millisecond); ok {
		t.Fatal("server answered a frame with checksum seed 0xff")
	}

	signup(t, a, "Howard", "ABCDEFGH")
	signup(t, a, "Annie", "QRSTUVWX")
	hostPID, hostTicket := login(t, a, "Howard", "ABCDEFGH")
	joinPID, joinTicket := login(t, a, "Annie", "QRSTUVWX")
	host, hostURL := secure(t, a, hostPID, hostTicket, 0x2222, 4000)
	join, joinURL := secure(t, a, joinPID, joinTicket, 0x3333, 4001)
	host.exchange(25, 17, str("Online")) // UpdateStatus has an empty reply.

	gid := binary.LittleEndian.Uint32(host.rpc(21, 1, wccGathering(0, 6, false)))
	if gid < 1000 {
		t.Fatalf("gathering ID %d", gid)
	}
	if !bytes.Equal(host.rpc(21, 11, cat(u32(gid), str(""))), []byte{1}) {
		t.Fatal("host participation")
	}
	// The retail host launches with an empty URL (observed 21/26 args).
	if !bytes.Equal(host.rpc(21, 26, cat(u32(gid), str(""))), []byte{1}) {
		t.Fatal("launch session with empty URL")
	}
	// The retail joiner resolves the host (11/3, CID 0) before 21/11.
	lookup := join.rpc(11, 3, cat(u32(0), u32(hostPID)))
	lr := reader{b: lookup[1:]}
	if lookup[0] != 1 || lr.u32() != 1 || lr.str(512) != hostURL || !lr.done() {
		t.Fatalf("host lookup before joining %x", lookup)
	}
	// The lookup makes the server ask the host to probe the joiner (3/2).
	probeCall := host.serverCall()
	pr := reader{b: probeCall.Args}
	if probeCall.Protocol != 3 || probeCall.Method != 2 || pr.str(512) != joinURL+";RVCID="+urlField(joinURL, "CID") || !pr.done() {
		t.Fatalf("host probe instruction %+v", probeCall)
	}
	sessionURL := "prudp:/address=192.168.1.20;port=4000;stream=6;sid=1;PID=" + strconv.FormatUint(uint64(hostPID), 10)
	if !bytes.Equal(host.rpc(21, 27, cat(u32(gid), str(sessionURL))), []byte{1}) {
		t.Fatal("update session URL")
	}

	list := join.rpc(21, 17, cat(str("WCCGathering"), u32(0), u32(0xffffffff)))
	r := reader{b: list}
	if n := r.u32(); n != 1 {
		t.Fatalf("FindByType returned %d gatherings", n)
	}
	found := r.any(maxGatheringPayload)
	if !r.done() || found.Type != "WCCGathering" ||
		binary.LittleEndian.Uint32(found.Payload[offsetID:]) != gid ||
		binary.LittleEndian.Uint32(found.Payload[offsetOwner:]) != hostPID ||
		binary.LittleEndian.Uint32(found.Payload[offsetHost:]) != hostPID {
		t.Fatalf("listed gathering %+v", found)
	}
	if n := binary.LittleEndian.Uint32(join.rpc(21, 17, cat(str("ChatGathering"), u32(0), u32(10)))); n != 0 {
		t.Fatal("type filter ignored")
	}
	single := join.rpc(21, 21, u32(gid))
	if single[0] != 1 {
		t.Fatal("FindBySingleID")
	}

	if !bytes.Equal(join.rpc(21, 11, cat(u32(gid), str(""))), []byte{1}) {
		t.Fatal("join participation")
	}
	urls := join.rpc(21, 16, u32(gid))
	r = reader{b: urls}
	n := r.u32()
	var got []string
	for range n {
		got = append(got, r.str(512))
	}
	if !r.done() || len(got) != 3 || got[0] != sessionURL || !containsAll(got, hostURL, joinURL) {
		t.Fatalf("participant URLs %q", got)
	}

	if !bytes.Equal(join.rpc(21, 30, cat(u32(gid), u32(2))), []byte{0}) {
		t.Fatal("participant changed state")
	}
	if !bytes.Equal(host.rpc(21, 30, cat(u32(gid), u32(2))), []byte{1}) {
		t.Fatal("owner state change")
	}
	if !bytes.Equal(join.rpc(21, 29, u32(gid)), cat([]byte{1}, u32(2))) {
		t.Fatal("GetState")
	}
	if !bytes.Equal(host.rpc(21, 4, wccGathering(uint16(gid), 6, true)), []byte{1}) {
		t.Fatal("owner update")
	}
	if !bytes.Equal(join.rpc(21, 4, wccGathering(uint16(gid), 6, true)), []byte{0}) {
		t.Fatal("participant update accepted")
	}
	if !bytes.Equal(join.rpc(21, 2, u32(gid)), []byte{0}) {
		t.Fatal("participant unregistered the gathering")
	}
	// The host leaves an open table: it hands it over (21/36) and the
	// successor receives an ownership-change notification (14/1, type 4000).
	if !bytes.Equal(host.rpc(21, 36, cat(u32(gid), u32(1), u32(joinPID))), []byte{1}) {
		t.Fatal("handoff")
	}
	event := join.serverCall()
	er := reader{b: event.Args}
	if event.Protocol != 14 || event.Method != 1 || er.u32() != hostPID || er.u32() != ownershipChanged ||
		er.u32() != gid || er.u32() != joinPID || er.str(64) != "" || !er.done() {
		t.Fatalf("ownership notification %+v", event)
	}
	if !bytes.Equal(host.rpc(21, 12, cat(u32(gid), str(""))), []byte{1}) {
		t.Fatal("host leave")
	}
	if !bytes.Equal(join.rpc(21, 2, u32(gid)), []byte{1}) {
		t.Fatal("new owner unregister")
	}
	if n := binary.LittleEndian.Uint32(join.rpc(21, 17, cat(str("WCCGathering"), u32(0), u32(10)))); n != 0 {
		t.Fatal("closed gathering still listed")
	}
	if reply := join.exchange(21, 25, cat(str("x"), u32(0), u32(1))); reply.Success {
		t.Fatal("unsupported FindBySQLQuery succeeded")
	}
}

func containsAll(list []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, v := range list {
			found = found || v == w
		}
		if !found {
			return false
		}
	}
	return true
}

// Three players: the newcomer resolves the host (11/3) and asks for a probe
// from the player already seated (3/1), as observed with three PCSX2 clients.
func TestThirdPlayerReachesSeatedPlayerOverUDP(t *testing.T) {
	a := start(t)
	names := []string{"Hoster", "Seated", "Newbie"}
	clients := make([]*client, 3)
	pids := make([]uint32, 3)
	urls := make([]string, 3)
	for i, name := range names {
		signup(t, a, name, "PASSWORD")
		pid, ticket := login(t, a, name, "PASSWORD")
		clients[i], urls[i] = secure(t, a, pid, ticket, uint32(0x5000+i), 4000+i)
		pids[i] = pid
	}
	host, seated, newbie := clients[0], clients[1], clients[2]
	gid := binary.LittleEndian.Uint32(host.rpc(21, 1, wccGathering(0, 3, false)))
	host.rpc(21, 11, cat(u32(gid), str("")))
	host.rpc(21, 26, cat(u32(gid), str("")))
	seated.rpc(11, 3, cat(u32(0), u32(pids[0])))
	host.serverCall() // host probes the seated player
	seated.rpc(21, 11, cat(u32(gid), str("")))

	newbie.rpc(11, 3, cat(u32(0), u32(pids[0])))
	host.serverCall() // host probes the newcomer
	reply := newbie.exchange(3, 1, cat(u32(1), str(urls[1]+";RVCID=1")))
	if !reply.Success {
		t.Fatal("3/1 toward the seated player rejected")
	}
	probe := seated.serverCall()
	pr := reader{b: probe.Args}
	if probe.Protocol != 3 || probe.Method != 2 || !strings.HasPrefix(pr.str(512), urls[2]+";RVCID=") || !pr.done() {
		t.Fatalf("seated player was not asked to probe the newcomer: %+v", probe)
	}
	if !bytes.Equal(newbie.rpc(21, 11, cat(u32(gid), str(""))), []byte{1}) {
		t.Fatal("third player could not join")
	}
}

// WCC accepts spaces between characters in player names, not at either end.
func TestNamesWithInnerSpacesOverUDP(t *testing.T) {
	a := start(t)
	signup(t, a, "Card Shark", "PASSWORD")
	pid, ticket := login(t, a, "card shark", "PASSWORD")
	if _, url := secure(t, a, pid, ticket, 0x7000, 4100); !strings.Contains(url, "PID="+strconv.FormatUint(uint64(pid), 10)) {
		t.Fatal("named login with a space")
	}
	guestPID, guestTicket := login(t, a, "guest", "h7fyctiuucf")
	guest, _ := secure(t, a, guestPID, guestTicket, 0x7001, 4101)
	initial := quazalcrypto.InitialKey([]byte("PASSWORD"))
	for _, name := range []string{" Leading", "Trailing "} {
		args := cat(str(name), str(hex.EncodeToString(initial[:])), u32(0x20), str("qwerty@keyboard.com"))
		if guest.exchange(25, 1, args).Success {
			t.Fatalf("signup accepted %q", name)
		}
	}
}
