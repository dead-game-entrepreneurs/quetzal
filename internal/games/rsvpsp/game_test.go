package rsvpsp

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
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

// client speaks the retail byte shapes over real loopback UDP: checksum seed
// 0x1f and the plain selector-0 payload wrapper (no transport RC4).
type client struct {
	t        *testing.T
	conn     *net.UDPConn
	cookie   uint32
	session  byte
	sequence uint16
	call     uint32
	// pending holds server-initiated requests that arrived while waiting for
	// a reply; they were answered at once, as the retail dispatcher does.
	pending []quazal.Request
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
		c.t.Fatalf("server frame did not use seed 0x1f: %x %v", wire[:n], err)
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
		if len(p.Payload) > 5 && p.Payload[5]&0x80 != 0 {
			c.pending = append(c.pending, c.answer(p.Payload[1:]))
			continue
		}
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

// answer replies to a server-initiated request with an empty success.
func (c *client) answer(wire []byte) quazal.Request {
	c.t.Helper()
	request, err := quazal.ParseRequest(wire)
	if err != nil {
		c.t.Fatalf("server request %x: %v", wire, err)
	}
	reply, err := quazal.SuccessReply(request, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	c.send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagReliable | prudp.FlagNeedsAck | prudp.FlagSize, Sequence: c.sequence, Payload: wrap(reply)}, ChecksumSeed)
	c.sequence++
	return request
}

// serverCall returns the next server-initiated request, already answered.
func (c *client) serverCall() quazal.Request {
	c.t.Helper()
	if len(c.pending) > 0 {
		request := c.pending[0]
		c.pending = c.pending[1:]
		return request
	}
	for range 8 {
		p, ok := c.read(3 * time.Second)
		if !ok {
			break
		}
		if p.Flags&prudp.FlagAck != 0 || p.Type != prudp.DATA {
			continue
		}
		c.send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagAck, Sequence: p.Sequence}, ChecksumSeed)
		return c.answer(p.Payload[1:])
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

func str(v string) []byte {
	if v == "" {
		return []byte{0, 0}
	}
	b := binary.LittleEndian.AppendUint16(nil, uint16(len(v)+1))
	return append(append(b, v...), 0)
}
func u32(v uint32) []byte        { return binary.LittleEndian.AppendUint32(nil, v) }
func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// record frames a Data body with its own u32 length, as the any<> writer does.
func record(fields ...[]byte) []byte {
	body := cat(fields...)
	return cat(u32(uint32(len(body))), body)
}

// anyData encodes an any<Data> holder: class name, then a u32-sized buffer.
func anyData(name string, payload []byte) []byte {
	return cat(str(name), u32(uint32(len(payload))), payload)
}

func publicRecord() []byte {
	birth := binary.LittleEndian.AppendUint64(nil, 0x1f4b4c0000000000)
	return record(str("Logan Keller"), []byte{1}, birth, str("US"), str("en"), str(""), str("Las Vegas"))
}

func privateRecord() []byte { return record(str("Favourite city?"), str("Vegas")) }

func signupArgs(name, password string, public, private []byte) []byte {
	initial := quazalcrypto.InitialKey([]byte(password))
	return cat(str(name), str(hex.EncodeToString(initial[:])), u32(0x20), str("player@example.test"),
		anyData(publicType, public), anyData(privateType, private))
}

func buffer(t *testing.T, b []byte) ([]byte, []byte) {
	t.Helper()
	if len(b) < 4 || int(binary.LittleEndian.Uint32(b)) > len(b)-4 {
		t.Fatal("truncated Buffer")
	}
	n := int(binary.LittleEndian.Uint32(b))
	return b[4 : 4+n], b[4+n:]
}

func readStr(t *testing.T, b []byte) string {
	t.Helper()
	if len(b) < 2 {
		t.Fatal("truncated String")
	}
	n := int(binary.LittleEndian.Uint16(b))
	if n == 0 {
		return ""
	}
	if len(b) < 2+n {
		t.Fatal("truncated String")
	}
	return string(b[2 : 2+n-1])
}

type adapter struct {
	auth, secure netip.AddrPort
	state        string
}

func start(t *testing.T) adapter {
	t.Helper()
	return startWithLog(t, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func startWithLog(t *testing.T, log *slog.Logger) adapter {
	t.Helper()
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
	state := t.TempDir()
	def, err := definition(log, netip.MustParseAddr("127.0.0.1"), authPort, securePort, state, key)
	if err != nil {
		t.Fatal(err)
	}
	if def.Key != "rsvpsp" || len(def.UDPPorts) != 2 || def.UDPPorts[0] != int(authPort) || def.UDPPorts[1] != int(securePort) {
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
	loopback := netip.MustParseAddr("127.0.0.1")
	return adapter{auth: netip.AddrPortFrom(loopback, authPort), secure: netip.AddrPortFrom(loopback, securePort), state: state}
}

// login performs 10/1 and 10/3 and returns the PID and service ticket.
func login(t *testing.T, a adapter, name string, key quazalcrypto.Key) (uint32, quazalcrypto.ServiceTicketEnvelope, bool) {
	t.Helper()
	c, _ := dial(t, a.auth, nil)
	reply := c.exchange(10, 1, str(name))
	if !reply.Success {
		return 0, quazalcrypto.ServiceTicketEnvelope{}, false
	}
	b := reply.Payload
	if binary.LittleEndian.Uint32(b) != quazal.Success {
		t.Fatal("login result")
	}
	pid := binary.LittleEndian.Uint32(b[4:])
	proof, rest := buffer(t, b[8:])
	url := readStr(t, rest)
	if !strings.HasPrefix(url, "prudps:/") || !strings.Contains(url, ";port="+strconv.Itoa(int(a.secure.Port()))+";") {
		t.Fatalf("secure URL %q", url)
	}
	if key == (quazalcrypto.Key{}) {
		return pid, quazalcrypto.ServiceTicketEnvelope{}, true
	}
	if _, err := quazalcrypto.Open(key, proof); err != nil {
		t.Fatalf("login proof: %v", err)
	}
	sealed, rest := buffer(t, c.rpc(10, 3, cat(u32(pid), u32(2)))[4:])
	if len(rest) != 0 {
		t.Fatal("ticket trailing bytes")
	}
	envelope, err := quazalcrypto.OpenServiceTicket(key, sealed)
	if err != nil {
		t.Fatal(err)
	}
	return pid, envelope, true
}

func secure(t *testing.T, a adapter, pid uint32, ticket quazalcrypto.ServiceTicketEnvelope, nonce uint32, register bool) *client {
	t.Helper()
	payload, err := quazalcrypto.BuildConnect(ticket.Ticket, ticket.SessionKey, quazalcrypto.Challenge{PrincipalID: pid, Nonce: nonce})
	if err != nil {
		t.Fatal(err)
	}
	c, reply := dial(t, a.secure, payload)
	if err := quazalcrypto.VerifyConnectReply(reply, nonce); err != nil {
		t.Fatal(err)
	}
	if register {
		local := "prudp:/address=192.168.1.30;port=3658;stream=3;sid=15;type=2"
		got := c.rpc(11, 1, cat(u32(1), str(local)))
		if binary.LittleEndian.Uint32(got) != quazal.Success {
			t.Fatal("registration failed")
		}
		if url := readStr(t, got[8:]); !strings.Contains(url, "PID="+strconv.FormatUint(uint64(pid), 10)) {
			t.Fatalf("registered URL %q", url)
		}
	}
	return c
}

func guest(t *testing.T, a adapter, nonce uint32, register bool) *client {
	t.Helper()
	pid, ticket, ok := login(t, a, "guest", quazalcrypto.PasswordKey([]byte("h7fyctiuucf"), quazal.GuestPID))
	if !ok || pid != quazal.GuestPID {
		t.Fatal("guest login")
	}
	return secure(t, a, pid, ticket, nonce, register)
}

// TestSignupWithProfileThenLoginOverUDP follows CreatingAccountState: guest
// login and registration, 25/21 with the Spark profile records (groups 0x20
// from 0x08bac48c), then login under the new name.
func TestSignupWithProfileThenLoginOverUDP(t *testing.T) {
	a := start(t)

	// A frame checksummed with another title's seed is dropped.
	stray, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(a.auth))
	if err != nil {
		t.Fatal(err)
	}
	defer stray.Close()
	probe := &client{t: t, conn: stray}
	probe.send(prudp.Packet{Type: prudp.SYN, Flags: prudp.FlagNeedsAck}, 0x2b)
	if _, ok := probe.read(300 * time.Millisecond); ok {
		t.Fatal("server answered a frame with WCC's checksum seed")
	}

	if _, _, ok := login(t, a, "Keller", quazalcrypto.Key{}); ok {
		t.Fatal("unknown account logged in")
	}
	unregistered := guest(t, a, 1, false)
	if unregistered.exchange(25, 21, signupArgs("Keller", "pass1234", publicRecord(), privateRecord())).Success {
		t.Fatal("signup before registration")
	}
	g := guest(t, a, 2, true)
	for i, bad := range [][]byte{
		cat(str("Keller"), str(strings.Repeat("0", 32)), u32(0x20), str("player@example.test"), anyData("EidosAccountPublic", publicRecord()), anyData(privateType, privateRecord())),
		signupArgs("Keller", "pass1234", publicRecord()[4:], privateRecord()),
		signupArgs("Keller", "pass1234", record(str("Logan"), []byte{2}, make([]byte, 8), str(""), str(""), str(""), str("")), privateRecord()),
		signupArgs("Keller", "pass1234", publicRecord(), record(str("question only"))),
		append(signupArgs("Keller", "pass1234", publicRecord(), privateRecord()), 0),
		signupArgs("guest", "pass1234", publicRecord(), privateRecord()),
	} {
		if g.exchange(25, 21, bad).Success {
			t.Fatalf("malformed signup %d accepted", i)
		}
	}
	if got := g.rpc(25, 21, signupArgs("Keller", "pass1234", publicRecord(), privateRecord())); len(got) != 0 {
		t.Fatalf("CreateAccountWithCustomData returns void, got %x", got)
	}
	if g.exchange(25, 21, signupArgs("keller", "other", publicRecord(), privateRecord())).Success {
		t.Fatal("duplicate name accepted")
	}

	store, err := quazal.OpenStore(filepath.Join(a.state, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	account, ok := store.Lookup("Keller")
	if !ok || account.Contact != "player@example.test" || account.PublicData == nil || account.PublicData.Type != publicType ||
		!bytes.Equal(account.PublicData.Payload, publicRecord()) || !bytes.Equal(account.PrivateData.Payload, privateRecord()) {
		t.Fatalf("stored account %+v", account)
	}

	key := quazalcrypto.ExtendInitial(quazalcrypto.InitialKey([]byte("pass1234")), account.PID)
	pid, ticket, ok := login(t, a, "Keller", key)
	if !ok || pid != account.PID {
		t.Fatal("named login")
	}
	player := secure(t, a, pid, ticket, 3, true)
	// The retail menus' first Spark calls: own statistics (7: u32 1 and
	// ResultRange 0/20) show only the player, unranked and with zeros, before
	// a first match; the friends list (10, no arguments) is empty.
	zero := cat(u32(pid), str("Keller"), u32(2), u32(8), make([]byte, 32), u32(1), u32(0))
	if got := player.rpc(60, 7, cat(u32(1), u32(0), u32(20))); !bytes.Equal(got, cat(u32(1), zero)) {
		t.Fatalf("60/7 %x", got)
	}
	if got := player.rpc(60, 10, nil); !bytes.Equal(got, u32(0)) {
		t.Fatalf("60/10 %x", got)
	}
	if got := player.rpc(60, 21, quickMatchArgs()); !bytes.Equal(got, make([]byte, 8)) {
		t.Fatalf("60/21 with no games %x", got)
	}
	if player.exchange(60, 13, nil).Success || player.exchange(20, 9, nil).Success {
		t.Fatal("unimplemented title call succeeded")
	}
	if guest(t, a, 4, true).exchange(60, 10, nil).Success {
		t.Fatal("guest reached Spark")
	}
	if got := player.exchange(25, 17, str("Online")); !got.Success {
		t.Fatal("status update after login")
	}
}

// TestPublicURLProbeOverUDP replays the probe captured from PPSSPP when
// creating a match (game socket port 1000 → secure port, seed 0).
func TestPublicURLProbeOverUDP(t *testing.T) {
	a := start(t)
	probe, _ := hex.DecodeString("7171050000000000000034120000022f")
	for _, target := range []netip.AddrPort{a.secure, a.auth} {
		conn, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(target))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		bad := bytes.Clone(probe)
		bad[len(bad)-1] ^= 1
		for _, wire := range [][]byte{bad, probe} {
			if _, err := conn.Write(wire); err != nil {
				t.Fatal(err)
			}
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		reply := make([]byte, 512)
		n, err := conn.Read(reply)
		if target == a.auth {
			if err == nil {
				t.Fatal("auth port answered the probe")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		local := conn.LocalAddr().(*net.UDPAddr)
		want := "4\x12\x00\x00\x02udp:/address=127.0.0.1;port=" + strconv.Itoa(local.Port) + "\x00"
		if string(reply[:n]) != want {
			t.Fatalf("probe reply %q", reply[:n])
		}
		// Only the one valid probe is answered.
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		if _, err := conn.Read(reply); err == nil {
			t.Fatal("corrupted probe was answered")
		}
	}
}

func TestPublicURLRejectsOtherUSERPackets(t *testing.T) {
	ctx := context.Background()
	source := netip.MustParseAddrPort("192.168.1.30:1000")
	good := prudp.Packet{Source: 0x71, Destination: 0x71, Type: prudp.USER, Payload: probeRequest}
	if _, err := publicURL(ctx, source, good); err != nil {
		t.Fatal(err)
	}
	tdu := good
	tdu.Payload = []byte{0xcd, 0x80, 0, 0, 2}
	other := good
	other.Source = 0x31
	for _, p := range []prudp.Packet{tdu, other} {
		if _, err := publicURL(ctx, source, p); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
	if _, err := publicURL(ctx, netip.MustParseAddrPort("0.0.0.0:1000"), good); err == nil {
		t.Fatal("reflected an unspecified source")
	}
}

// createArgs is the 60/4 request captured from PPSSPP, with the LAN address
// and match name replaced by same-length placeholders.
func createArgs(t *testing.T) []byte {
	t.Helper()
	const captured = "0a00537061726b47616d65005e0000005a00000000000000000000000000000000000000630000000000000010000000000000000600534e45454400010000000000000000030000000000000000000000000000000200000002000000010000000200000002000000000000000100020000002e007564703a2f616464726573733d3139322e3136382e38382e3137353b706f72743d313030303b52564349443d31002e007564703a2f706f72743d313030303b616464726573733d3139322e3136382e38382e3137353b52564349443d3100"
	b, err := hex.DecodeString(captured)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.ReplaceAll(b, []byte("192.168.88.175"), []byte("192.168.10.175"))
	return bytes.ReplaceAll(b, []byte("SNEED"), []byte("VEGAS"))
}

func quickMatchArgs() []byte { return cat([]byte{0}, u32(0), u32(0), u32(0), u32(10)) }

func browseArgs() []byte {
	var b []byte
	for range 7 {
		b = append(b, str("")...)
	}
	return cat(b, u32(0), u32(20))
}

func account(t *testing.T, a adapter, name string, nonce uint32) (*client, uint32) {
	t.Helper()
	g := guest(t, a, nonce, true)
	g.rpc(25, 21, signupArgs(name, "pass1234", publicRecord(), privateRecord()))
	store, err := quazal.OpenStore(filepath.Join(a.state, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	acct, _ := store.Lookup(name)
	key := quazalcrypto.ExtendInitial(quazalcrypto.InitialKey([]byte("pass1234")), acct.PID)
	pid, ticket, ok := login(t, a, name, key)
	if !ok {
		t.Fatal("login")
	}
	return secure(t, a, pid, ticket, nonce+1, true), pid
}

// TestHostedGameFlowOverUDP covers create (60/4), browse (60/20), quick
// match (60/21), join (60/5) and end (60/19) with the captured create call.
func TestHostedGameFlowOverUDP(t *testing.T) {
	a := start(t)
	host, hostPID := account(t, a, "Hostess", 10)
	joiner, joinerPID := account(t, a, "Joiner", 20)

	for i, bad := range [][]byte{
		bytes.Replace(createArgs(t), []byte("SparkGame"), []byte("SparkGamf"), 1),
		createArgs(t)[:len(createArgs(t))-1],
		append(createArgs(t), 0),
	} {
		if host.exchange(60, 4, bad).Success {
			t.Fatalf("malformed create %d accepted", i)
		}
	}
	reply := host.rpc(60, 4, createArgs(t))
	if len(reply) != 4 || binary.LittleEndian.Uint32(reply) == 0 {
		t.Fatalf("create reply %x", reply)
	}
	id := binary.LittleEndian.Uint32(reply)

	// The host does not see its own game; the other player does.
	if got := host.rpc(60, 21, quickMatchArgs()); !bytes.Equal(got, make([]byte, 8)) {
		t.Fatalf("host saw its own game %x", got)
	}
	for _, request := range []struct {
		method uint32
		args   []byte
	}{{20, browseArgs()}, {21, quickMatchArgs()}} {
		got := joiner.rpc(60, request.method, request.args)
		r := reader{b: got}
		if r.u32() != 1 || r.str(64) != sparkGameType {
			t.Fatalf("60/%d games %x", request.method, got)
		}
		record := r.take(int(r.u32()))
		if r.err != nil || binary.LittleEndian.Uint32(record[4:]) != id || binary.LittleEndian.Uint32(record[8:]) != hostPID ||
			binary.LittleEndian.Uint32(record[12:]) != hostPID || !bytes.Contains(record, []byte("VEGAS")) {
			t.Fatalf("listed record %x", record)
		}
		if r.u32() != 1 || r.u32() != id || r.u32() != 2 ||
			r.str(256) != "udp:/address=192.168.10.175;port=1000;RVCID=1" || r.str(256) != "udp:/port=1000;address=192.168.10.175;RVCID=1" || !r.done() {
			t.Fatalf("60/%d URLs %x", request.method, got)
		}
	}
	// A page beyond the list is empty.
	if got := joiner.rpc(60, 21, cat([]byte{0}, u32(0), u32(0), u32(1), u32(10))); !bytes.Equal(got, make([]byte, 8)) {
		t.Fatalf("offset page %x", got)
	}

	got := joiner.rpc(60, 5, cat(u32(id), []byte{0, 0}))
	r := reader{b: got}
	if r.u32() != 2 || r.str(256) != "udp:/address=192.168.10.175;port=1000;RVCID=1" || r.str(256) == "" {
		t.Fatalf("join reply URLs %x", got)
	}
	// Player records: the host, then the joiner (PID, name, status 2, two
	// empty stat objects).
	wantPlayers := func(r *reader, pids ...uint32) {
		t.Helper()
		if n := r.u32(); n != uint32(len(pids)) {
			t.Fatalf("%d player records", n)
		}
		for _, pid := range pids {
			if r.u32() != pid || r.str(64) == "" || r.u32() != 2 || r.u32() != 0 || r.u32() != 0 {
				t.Fatalf("record for %d", pid)
			}
		}
	}
	wantPlayers(&r, hostPID, joinerPID)
	if r.str(64) != sparkGameType {
		t.Fatalf("join reply game %x", got)
	}
	r.take(int(r.u32()))
	if !r.done() {
		t.Fatalf("join reply trailing %x", got)
	}

	// The host learns of the joiner: participation 900002 (player, game,
	// name), then 901000 asking it to refresh records for the game.
	for _, want := range []struct{ kind, param1, param2 uint32 }{{900002, joinerPID, id}, {901000, id, 0}} {
		event := host.serverCall()
		r := reader{b: event.Args}
		if event.Protocol != 14 || event.Method != 1 || r.u32() != joinerPID || r.u32() != want.kind || r.u32() != want.param1 || r.u32() != want.param2 {
			t.Fatalf("notification %+v %x", event, event.Args)
		}
		name := r.str(64)
		if (want.kind == 900002) != (name == "Joiner") || !r.done() {
			t.Fatalf("notification text %q", name)
		}
	}
	// 60/9 returns records for the requested PIDs that are players.
	records := host.rpc(60, 9, cat(u32(id), u32(3), u32(hostPID), u32(joinerPID), u32(999), u32(0), u32(20)))
	r = reader{b: records}
	wantPlayers(&r, hostPID, joinerPID)
	if !r.done() {
		t.Fatalf("60/9 trailing %x", records)
	}
	if joiner.exchange(60, 5, cat(u32(id+1), []byte{0, 0})).Success {
		t.Fatal("joined a missing game")
	}

	// Captured from the LAN match: 3/3 and 21/39 after joining, then 60/15 in
	// the shape of the captured 60/16 (game ID, play time; 60/16 itself is a
	// leave, see TestLeavingAndRejoining) and both players' 60/12
	// end-of-match reports. All are void.
	for _, call := range []struct {
		protocol byte
		method   uint32
		args     string
	}{
		{3, 3, "010000002e007564703a2f616464726573733d3139322e3136382e31302e3132363b706f72743d313030303b52564349443d31002e007564703a2f616464726573733d3139322e3136382e31302e3137353b706f72743d313030303b52564349443d3200"},
		{21, 39, "01000000020000002e007564703a2f616464726573733d3139322e3136382e31302e3137353b706f72743d313030303b52564349443d32002e007564703a2f706f72743d313030303b616464726573733d3139322e3136382e31302e3137353b52564349443d3200"},
		{60, 15, "0100000000000000"},
		{60, 12, "010000003001000001000000ea03000005005452495300020000000000000000000000"},
		{60, 12, "010000002e01000000000000"},
	} {
		args, _ := hex.DecodeString(call.args)
		if got := joiner.rpc(call.protocol, call.method, args); len(got) != 0 {
			t.Fatalf("%d/%d reply %x", call.protocol, call.method, got)
		}
		// Stat records hold polymorphic nested objects, so 60/12 is only
		// bounded, not parsed to the end.
		if call.method != 12 && joiner.exchange(call.protocol, call.method, append(args, 0)).Success {
			t.Fatalf("%d/%d accepted trailing bytes", call.protocol, call.method)
		}
	}

	// Each report asks every player to refresh the game's records (901000);
	// the host's own record now carries what was reported.
	for range 2 {
		for _, c := range []*client{host, joiner} {
			if event := c.serverCall(); !bytes.Equal(event.Args[4:16], cat(u32(901000), u32(id), u32(0))) {
				t.Fatalf("report notification %x", event.Args)
			}
		}
	}
	// A report with real stats replaces the player's record for 60/9:
	// PID, name, status, 2 floats (3.0, 10.0), then an empty second object.
	reported := cat(u32(hostPID), str("Hostess"), u32(2), u32(2), u32(0x40400000), u32(0x41200000), u32(0))
	joiner.rpc(60, 12, cat(u32(id), u32(180), u32(1), reported))
	for _, c := range []*client{host, joiner} {
		c.serverCall()
	}
	if got := host.rpc(60, 9, cat(u32(id), u32(1), u32(hostPID), u32(0), u32(20))); !bytes.Equal(got, cat(u32(1), reported)) {
		t.Fatalf("60/9 after a report %x", got)
	}
	// A record for someone outside the game is not kept.
	joiner.rpc(60, 12, cat(u32(id), u32(180), u32(1), cat(u32(4242), str("Nobody"), u32(2), u32(0), u32(0))))
	for _, c := range []*client{host, joiner} {
		c.serverCall()
	}
	if got := host.rpc(60, 9, cat(u32(id), u32(1), u32(4242), u32(0), u32(20))); !bytes.Equal(got, u32(0)) {
		t.Fatalf("60/9 for a non-player %x", got)
	}

	// The host's 60/19 (entering play) keeps the game listed, since games in
	// play can be joined; its players and records stay as they were.
	if got := joiner.rpc(60, 19, u32(id)); len(got) != 0 {
		t.Fatalf("start reply %x", got)
	}
	host.rpc(60, 19, u32(id))
	if got := joiner.rpc(60, 21, quickMatchArgs()); bytes.Equal(got, make([]byte, 8)) {
		t.Fatal("game in play not listed")
	}
	if got := joiner.rpc(60, 9, cat(u32(id), u32(1), u32(hostPID), u32(0), u32(20))); !bytes.Equal(got, cat(u32(1), reported)) {
		t.Fatalf("60/9 after start %x", got)
	}

	// After the reports, 60/18 names the next round's host: the current one.
	for _, c := range []*client{host, joiner} {
		if got := c.rpc(60, 18, u32(id)); !bytes.Equal(got, u32(hostPID)) {
			t.Fatalf("next host %x", got)
		}
	}
	if host.exchange(60, 18, u32(id+1)).Success {
		t.Fatal("next host for a missing game")
	}

	// The host then re-creates the match with only "udp:/fullhouse"; the
	// joiner is carried into the new game and the host is told about it.
	// Keep the SparkGame any and the bool, then one URL.
	next := createArgs(t)[:len(str(sparkGameType))+4+0x5e+1]
	next = cat(next, u32(1), str("udp:/fullhouse"))
	reply = host.rpc(60, 4, next)
	second := binary.LittleEndian.Uint32(reply)
	if second != id {
		t.Fatalf("next round got game %d, want the same ID %d", second, id)
	}
	for _, want := range []struct{ kind, param1, param2 uint32 }{{900002, joinerPID, second}, {901000, second, 0}} {
		event := host.serverCall()
		r := reader{b: event.Args}
		if r.u32() == 0 || r.u32() != want.kind || r.u32() != want.param1 || r.u32() != want.param2 {
			t.Fatalf("next-round notification %x", event.Args)
		}
	}
	r = reader{b: host.rpc(60, 9, cat(u32(second), u32(2), u32(hostPID), u32(joinerPID), u32(0), u32(20)))}
	wantPlayers(&r, hostPID, joinerPID)
	if got := joiner.rpc(60, 21, quickMatchArgs()); !bytes.Contains(got, []byte("udp:/fullhouse")) {
		t.Fatalf("next-round game not listed %x", got)
	}
	// Every player is asked to refresh the new game's records.
	if event := joiner.serverCall(); !bytes.Equal(event.Args[4:16], cat(u32(901000), u32(second), u32(0))) {
		t.Fatalf("joiner next-round notification %x", event.Args)
	}

	// The joiner then finds the host's next-round game by its description
	// (captured request: "(1)TRIS", ResultRange 0/20). Here the description
	// is the captured create's ("VEGAS"); an unknown one finds nothing.
	got = joiner.rpc(21, 18, cat(str("VEGAS"), u32(0), u32(20)))
	r = reader{b: got}
	if r.u32() != 1 || r.str(64) != sparkGameType {
		t.Fatalf("21/18 %x", got)
	}
	if record := r.take(int(r.u32())); r.err != nil || binary.LittleEndian.Uint32(record[4:]) != id || !r.done() {
		t.Fatalf("21/18 record %x", got)
	}
	// Joining the next-round game hands out the host's real addresses, not
	// the "udp:/fullhouse" placeholder.
	got = joiner.rpc(60, 5, cat(u32(id), []byte{0, 0}))
	r = reader{b: got}
	if r.u32() != 2 || r.str(256) != "udp:/address=192.168.10.175;port=1000;RVCID=1" {
		t.Fatalf("next-round join URLs %x", got)
	}
	captured, _ := hex.DecodeString("080028312954524953000000000014000000")
	if got := joiner.rpc(21, 18, captured); !bytes.Equal(got, u32(0)) {
		t.Fatalf("21/18 unknown description %x", got)
	}
	if joiner.exchange(21, 18, append(captured, 0)).Success {
		t.Fatal("21/18 accepted trailing bytes")
	}
}

func TestDisconnectsUpdateGames(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	names := map[uint32]string{1000: "Hostess", 1001: "Joiner"}
	g := newGames(log, func(pid uint32) string { return names[pid] })
	host := prudp.Peer{ID: 1, LocalPort: SecurePort}
	joiner := prudp.Peer{ID: 2, LocalPort: SecurePort}
	g.registered(host, 1000, "")
	g.registered(joiner, 1001, "")
	request := quazal.Request{Protocol: SparkProtocol, Method: 4, Args: createArgs(t)}
	if _, ok, err := g.spark(host, 1000, request); !ok || err != nil {
		t.Fatal(err)
	}
	if _, ok, err := g.spark(joiner, 1001, quazal.Request{Protocol: SparkProtocol, Method: 5, Args: cat(u32(1), []byte{0, 0})}); !ok || err != nil {
		t.Fatal(err)
	}
	if calls := g.serverCalls(); len(calls) != 2 || calls[0].Peer != host || calls[1].Peer != host {
		t.Fatalf("join notifications %+v", calls)
	}
	// A stale registration of the joiner leaves it in the game.
	g.disconnect(prudp.Peer{ID: 9, LocalPort: SecurePort}, 1001)
	if !g.byID[1].has(1001) {
		t.Fatal("stale registration removed the player")
	}
	// The joiner leaving tells the host with status 0.
	g.disconnect(joiner, 1001)
	calls := g.serverCalls()
	if len(calls) != 1 || calls[0].Peer != host || g.byID[1].has(1001) {
		t.Fatalf("leave notifications %+v", calls)
	}
	r := reader{b: calls[0].Args}
	if r.u32() != 1001 || r.u32() != 900000 || r.u32() != 1001 || r.u32() != 1 {
		t.Fatalf("leave event %x", calls[0].Args)
	}
	g.disconnect(host, 1000)
	if len(g.byID) != 0 {
		t.Fatal("host disconnect left the game")
	}
	// A second create by the same host replaces the first.
	g.registered(host, 1000, "")
	for range 2 {
		if _, ok, err := g.spark(host, 1000, request); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	if len(g.byID) != 1 {
		t.Fatalf("host has %d games", len(g.byID))
	}
}

func TestNewGamesTakeTheLowestFreeID(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	g := newGames(log, func(uint32) string { return "" })
	create := func(peer prudp.Peer, pid uint32) uint32 {
		t.Helper()
		reply, ok, err := g.spark(peer, pid, quazal.Request{Protocol: SparkProtocol, Method: 4, Args: createArgs(t)})
		if !ok || err != nil {
			t.Fatal(err)
		}
		return binary.LittleEndian.Uint32(reply)
	}
	a, b, c := prudp.Peer{ID: 1}, prudp.Peer{ID: 2}, prudp.Peer{ID: 3}
	if create(a, 1000) != 1 || create(b, 1001) != 2 {
		t.Fatal("first IDs")
	}
	g.disconnect(a, 1000)
	if id := create(c, 1002); id != 1 {
		t.Fatalf("freed ID not reused: %d", id)
	}
	// A host's new ordinary game replaces its old one and takes the lowest
	// free ID, not necessarily the old one.
	if id := create(b, 1001); id != 2 {
		t.Fatalf("re-created game %d", id)
	}
}

func TestValidateAccountData(t *testing.T) {
	public := quazal.AnyData{Type: publicType, Payload: publicRecord()}
	private := quazal.AnyData{Type: privateType, Payload: privateRecord()}
	if err := ValidateAccountData(public, private); err != nil {
		t.Fatal(err)
	}
	long := quazal.AnyData{Type: publicType, Payload: record(str(strings.Repeat("x", maxField+1)), []byte{0}, make([]byte, 8), str(""), str(""), str(""), str(""))}
	if ValidateAccountData(long, private) == nil {
		t.Fatal("overlong field accepted")
	}
	inner := quazal.AnyData{Type: privateType, Payload: record([]byte{4, 0, 'a', 0, 'b', 0}, str(""))}
	if ValidateAccountData(public, inner) == nil {
		t.Fatal("String with an inner NUL accepted")
	}
	err := ValidateAccountData(public, quazal.AnyData{Type: privateType, Payload: record(str("secret"))})
	if err == nil || strings.Contains(err.Error(), hex.EncodeToString([]byte("secret"))) {
		t.Fatalf("private record error leaked or missing: %v", err)
	}
}

// TestLeavingAndRejoining covers 60/16: a player leaving after a match (as
// at 20:06 in the field) is taken out of the game and can join it again, also
// once it is in play; the host leaving closes the game.
func TestLeavingAndRejoining(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	names := map[uint32]string{1000: "Sneed", 1002: "Tris"}
	g := newGames(log, func(pid uint32) string { return names[pid] })
	host, joiner := prudp.Peer{ID: 1, LocalPort: SecurePort}, prudp.Peer{ID: 2, LocalPort: SecurePort}
	g.registered(host, 1002, "")
	g.registered(joiner, 1000, "")
	call := func(peer prudp.Peer, pid, method uint32, args []byte) []byte {
		t.Helper()
		reply, ok, err := g.spark(peer, pid, quazal.Request{Protocol: SparkProtocol, Method: method, Args: args})
		if !ok || err != nil {
			t.Fatalf("60/%d: %v", method, err)
		}
		return reply
	}
	id := binary.LittleEndian.Uint32(call(host, 1002, 4, createArgs(t)))
	call(joiner, 1000, 5, cat(u32(id), []byte{0, 0}))
	call(host, 1002, 19, u32(id))
	g.serverCalls()

	// Captured: the joiner leaving after the match (game 1, play time 0).
	call(joiner, 1000, 16, cat(u32(id), u32(0)))
	calls := g.serverCalls()
	if g.byID[id].has(1000) || len(calls) != 1 || calls[0].Peer != host {
		t.Fatalf("leave: players %v, calls %+v", g.byID[id].players, calls)
	}
	r := reader{b: calls[0].Args}
	if r.u32() != 1000 || r.u32() != eventParticipation || r.u32() != 1000 || r.u32() != id {
		t.Fatalf("leave event %x", calls[0].Args)
	}
	// The game in play is still listed and joinable from the friends list.
	if got := call(joiner, 1000, 21, quickMatchArgs()); bytes.Equal(got, make([]byte, 8)) {
		t.Fatal("game in play not listed")
	}
	if _, gid, joinable := g.gameOf(1002); gid != id || !joinable {
		t.Fatalf("gameOf %d %v", gid, joinable)
	}
	// Joining again announces the player to the host.
	call(joiner, 1000, 5, cat(u32(id), []byte{0, 0}))
	if calls := g.serverCalls(); len(calls) != 2 || calls[0].Peer != host || !g.byID[id].has(1000) {
		t.Fatalf("rejoin calls %+v", calls)
	}
	// A leave for another game or a malformed one changes nothing.
	call(joiner, 1000, 16, cat(u32(id+1), u32(0)))
	if _, _, err := g.spark(joiner, 1000, quazal.Request{Protocol: SparkProtocol, Method: 16, Args: u32(id)}); err == nil {
		t.Fatal("short 60/16 accepted")
	}
	if !g.byID[id].has(1000) {
		t.Fatal("unrelated leave removed the player")
	}
	// The host leaving closes the game.
	call(host, 1002, 16, cat(u32(id), u32(0)))
	if len(g.byID) != 0 {
		t.Fatal("host leave kept the game")
	}
}
