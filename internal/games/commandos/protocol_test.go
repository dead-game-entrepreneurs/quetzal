package commandos

import (
	"bytes"
	"context"
	"crypto/rc4"
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

// Synthetic client over real UDP. It implements the selected payload mode
// independently; it is not evidence that a stock client selects that mode.
type client struct {
	t        *testing.T
	conn     *net.UDPConn
	cookie   uint32
	sequence uint16
	call     uint32
	mode     string
}

func payload(b []byte, mode string, decode bool) []byte {
	if len(b) == 0 || mode == "raw" {
		return bytes.Clone(b)
	}
	if !decode {
		b = append([]byte{0}, b...)
	} else {
		b = bytes.Clone(b)
	}
	if mode == "rc4" {
		cipher, _ := rc4.NewCipher([]byte("CD&ML"))
		cipher.XORKeyStream(b, b)
	}
	if decode {
		if b[0] != 0 {
			panic("unexpected compression selector")
		}
		b = b[1:]
	}
	return b
}
func (c *client) send(p prudp.Packet) {
	c.t.Helper()
	p.Source = 0x3f
	p.Destination = 0x31
	if p.Type != prudp.SYN {
		p.Signature = c.cookie
		p.Session = 0x27
	}
	b, err := p.Marshal(0xa6)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err = c.conn.Write(b); err != nil {
		c.t.Fatal(err)
	}
}
func (c *client) read() prudp.Packet {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	b := make([]byte, 65535)
	n, err := c.conn.Read(b)
	if err != nil {
		c.t.Fatal(err)
	}
	p, used, err := prudp.ParseFrame(b[:n], 0xa6)
	if err != nil || used != n {
		c.t.Fatalf("bad packet %x %v", b[:n], err)
	}
	return p
}
func connect(t *testing.T, addr netip.AddrPort, security []byte, mode string) *client {
	t.Helper()
	conn, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(addr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := &client{t: t, conn: conn, sequence: 2, mode: mode}
	c.send(prudp.Packet{Type: prudp.SYN, Flags: prudp.FlagNeedsAck})
	syn := c.read()
	if syn.Type != prudp.SYN || syn.Flags&prudp.FlagAck == 0 {
		t.Fatal("SYN handshake failed")
	}
	c.cookie = syn.ConnectionSignature
	c.send(prudp.Packet{Type: prudp.CONNECT, Flags: prudp.FlagReliable | prudp.FlagNeedsAck, Sequence: 1, ConnectionSignature: 0x12345678, Payload: payload(security, mode, false)})
	ack := c.read()
	if ack.Type != prudp.CONNECT || ack.Flags&prudp.FlagAck == 0 || ack.Signature != 0x12345678 {
		t.Fatal("CONNECT handshake failed")
	}
	if len(security) > 0 {
		if err := quazalcrypto.VerifyConnectReply(payload(ack.Payload, mode, true), 123); err != nil {
			t.Fatal(err)
		}
	}
	return c
}
func (c *client) rpc(protocol byte, method uint32, b []byte) quazal.Response {
	c.t.Helper()
	c.call++
	wire, err := quazal.EncodeRequest(quazal.Request{Protocol: protocol, Method: method, CallID: c.call, Args: b})
	if err != nil {
		c.t.Fatal(err)
	}
	c.send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagReliable | prudp.FlagNeedsAck | prudp.FlagSize, Sequence: c.sequence, Payload: payload(wire, c.mode, false)})
	c.sequence++
	for n := 0; n < 8; n++ {
		p := c.read()
		if p.Flags&prudp.FlagAck != 0 {
			continue
		}
		if p.Type != prudp.DATA || p.Fragment != 0 {
			c.t.Fatalf("unexpected reply packet %+v", p)
		}
		c.send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagAck, Sequence: p.Sequence})
		reply, err := quazal.ParseResponse(payload(p.Payload, c.mode, true))
		if err != nil {
			c.t.Fatal(err)
		}
		if reply.CallID != c.call || reply.Protocol != protocol {
			c.t.Fatal("misrouted reply")
		}
		return reply
	}
	c.t.Fatal("RPC reply missing")
	return quazal.Response{}
}
func (c *client) ok(protocol byte, method uint32, b []byte) []byte {
	c.t.Helper()
	reply := c.rpc(protocol, method, b)
	if !reply.Success {
		c.t.Fatalf("RPC %d/%d failed: %+v", protocol, method, reply)
	}
	return reply.Payload
}
func startAdapter(t *testing.T, mode string) [2]netip.AddrPort {
	t.Helper()
	c := DefaultConfig()
	c.Bind = netip.MustParseAddr("127.0.0.1")
	c.PublicIP = c.Bind
	c.StateDir = t.TempDir()
	c.AuthPort = 0
	c.SecurePort = 0
	c.PayloadMode = mode
	var key quazalcrypto.Key
	key[0] = 42
	def, err := Definition(slog.New(slog.NewTextHandler(io.Discard, nil)), c, key)
	if err != nil {
		t.Fatal(err)
	}
	var sockets []*net.UDPConn
	var addresses [2]netip.AddrPort
	for i := range addresses {
		conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(c.Bind, 0)))
		if err != nil {
			t.Fatal(err)
		}
		sockets = append(sockets, conn)
		addresses[i] = conn.LocalAddr().(*net.UDPAddr).AddrPort()
		t.Cleanup(func() { _ = conn.Close() })
	}
	services, err := def.NewServices(sockets, nil)
	if err != nil {
		t.Fatal(err)
	}
	services = append(services, def.Auxiliary...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, len(services))
	for _, service := range services {
		go func() { done <- service.Serve(ctx) }()
	}
	t.Cleanup(func() {
		cancel()
		for _, service := range services {
			_ = service.Close()
		}
		for range services {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("service failed to stop")
			}
		}
	})
	return addresses
}
func login(t *testing.T, addresses [2]netip.AddrPort, name, password, mode string) (uint32, quazalcrypto.ServiceTicketEnvelope) {
	t.Helper()
	auth := connect(t, addresses[0], nil, mode)
	r := quazal.NewReader(auth.ok(10, 1, args(func(w *quazal.Writer) { w.String(name) })))
	if r.U32() != quazal.Success {
		t.Fatal("login result")
	}
	pid := r.U32()
	proof := r.Buffer(128)
	url := r.String(512)
	if r.U32() != 0 || r.String(512) != "" || r.String(255) != "" || !r.Done() {
		t.Fatal("login data shape")
	}
	if !strings.Contains(url, ";port="+strconv.Itoa(int(addresses[1].Port()))+";") {
		t.Fatalf("incorrect secure endpoint %q", url)
	}
	key := quazalcrypto.PasswordKey([]byte(password), pid)
	if _, err := quazalcrypto.Open(key, proof); err != nil {
		t.Fatal(err)
	}
	r = quazal.NewReader(auth.ok(10, 3, args(func(w *quazal.Writer) { w.U32(pid); w.U32(2) })))
	if r.U32() != quazal.Success {
		t.Fatal("ticket result")
	}
	sealed := r.Buffer(1024)
	if !r.Done() {
		t.Fatal("ticket shape")
	}
	ticket, err := quazalcrypto.OpenServiceTicket(key, sealed)
	if err != nil {
		t.Fatal(err)
	}
	return pid, ticket
}
func secureClient(t *testing.T, addresses [2]netip.AddrPort, pid uint32, ticket quazalcrypto.ServiceTicketEnvelope, mode string) *client {
	t.Helper()
	b, err := quazalcrypto.BuildConnect(ticket.Ticket, ticket.SessionKey, quazalcrypto.Challenge{PrincipalID: pid, Nonce: 123})
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, addresses[1], b, mode)
	r := quazal.NewReader(c.ok(11, 1, roomArgs(0)))
	if r.U32() != quazal.Success || r.U32() == 0 || !strings.HasPrefix(r.String(512), "prudp:/") || !r.Done() {
		t.Fatal("registration result")
	}
	return c
}
func TestAccountsAndTwoPlayerLobbyOverUDP(t *testing.T) {
	addresses := startAdapter(t, "raw")
	pid, ticket := login(t, addresses, signupPrincipal, signupPrincipal, "raw")
	bootstrap := secureClient(t, addresses, pid, ticket, "raw")
	if bootstrap.rpc(21, 19, nil).Success {
		t.Fatal("signup principal reached title services")
	}
	for _, name := range []string{"commando_one", "commando_two"} {
		initial := quazalcrypto.InitialKey([]byte(name + " password"))
		b := args(func(w *quazal.Writer) {
			w.String(name)
			w.String(hex.EncodeToString(initial[:]))
			w.U32(0)
			w.String("")
		})
		if !bytes.Equal(bootstrap.ok(25, 1, b), roomArgs(quazal.Success)) {
			t.Fatal("signup shape")
		}
	}
	hostPID, hostTicket := login(t, addresses, "commando_one", "commando_one password", "raw")
	host := secureClient(t, addresses, hostPID, hostTicket, "raw")
	guestPID, guestTicket := login(t, addresses, "commando_two", "commando_two password", "raw")
	guest := secureClient(t, addresses, guestPID, guestTicket, "raw")
	g := sampleRoom()
	b, _ := g.encode()
	r := quazal.NewReader(host.ok(21, 1, b))
	id := r.U32()
	if id == 0 || !r.Done() {
		t.Fatal("gathering ID")
	}
	for _, c := range []*client{host, guest} {
		if !bytes.Equal(c.ok(21, 11, messageArgs(id, "")), []byte{1}) {
			t.Fatal("participation")
		}
	}
	if !bytes.Equal(host.ok(21, 26, messageArgs(id, "")), []byte{1}) {
		t.Fatal("empty host URL publication")
	}
	host.ok(21, 30, args(func(w *quazal.Writer) { w.U32(id); w.U32(2) }))
	r = quazal.NewReader(guest.ok(21, 19, args(func(w *quazal.Writer) { w.String("^W...."); w.U32(0xffffffff); w.U32(10) })))
	if r.U32() != 1 {
		t.Fatal("host missing from browse")
	}
	any := r.Any(2048)
	if !r.Done() {
		t.Fatal("browse result shape")
	}
	b = args(func(w *quazal.Writer) { w.Any(any) })
	got, err := decodeGathering(b)
	if err != nil || got.ID != id || got.OwnerPID != hostPID || got.State != 2 {
		t.Fatalf("browse metadata %+v %v", got, err)
	}
	r = quazal.NewReader(guest.ok(50, 2, append(roomArgs(id), 1)))
	if r.U32() != 2 || r.U32() != hostPID || r.U32() != guestPID || !r.Done() {
		t.Fatal("participant result")
	}
	r = quazal.NewReader(guest.ok(21, 16, roomArgs(id)))
	// Host and relay both use loopback: return one virtual host candidate,
	// followed by the caller's own direct registration.
	if r.U32() != 2 {
		t.Fatal("peer endpoints")
	}
	hostEndpoint, hostErr := quazal.ParseStationURL(r.String(512))
	guestEndpoint, guestErr := quazal.ParseStationURL(r.String(512))
	if hostErr != nil || guestErr != nil || !r.Done() || hostEndpoint.PID != hostPID || guestEndpoint.PID != guestPID || hostEndpoint.Address.Port() < 30600 || hostEndpoint.Address.Port() > 30727 || guestEndpoint.Address != guest.conn.LocalAddr().(*net.UDPAddr).AddrPort() {
		t.Fatal("same-IP peer endpoint identity or routing", hostEndpoint, guestEndpoint, hostErr, guestErr)
	}
	if !bytes.Equal(guest.ok(21, 30, args(func(w *quazal.Writer) { w.U32(id); w.U32(3) })), []byte{0}) {
		t.Fatal("non-host changed state")
	}
	host.ok(21, 12, messageArgs(id, ""))
	r = quazal.NewReader(guest.ok(21, 19, args(func(w *quazal.Writer) { w.String("^W...."); w.U32(0); w.U32(10) })))
	if r.U32() != 0 || !r.Done() {
		t.Fatal("host departure left stale room")
	}
}
func TestConfiguredPayloadModesCarryTicketProofOverUDP(t *testing.T) {
	for _, mode := range []string{"plain", "rc4"} {
		t.Run(mode, func(t *testing.T) {
			addresses := startAdapter(t, mode)
			pid, ticket := login(t, addresses, signupPrincipal, signupPrincipal, mode)
			secureClient(t, addresses, pid, ticket, mode)
		})
	}
}
