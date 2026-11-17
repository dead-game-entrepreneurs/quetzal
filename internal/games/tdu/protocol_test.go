package tdu

import (
	"bytes"
	"crypto/rc4"
	"encoding/binary"
	"encoding/hex"
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

// This client uses the documented retail byte shapes on real loopback UDP.
// It is deliberately separate from the server's payload/compression helpers.
type protocolClient struct {
	t            *testing.T
	conn         *net.UDPConn
	cookie       uint32
	session      byte
	sequence     uint16
	call         uint32
	connectionID uint32
}

func wrapRPC(plain []byte) []byte {
	if len(plain) == 0 {
		return nil
	}
	input := append([]byte{0}, plain...)
	out := make([]byte, len(input))
	cipher, _ := rc4.NewCipher([]byte("CD&ML"))
	cipher.XORKeyStream(out, input)
	return out
}
func unwrapRPC(t *testing.T, encoded []byte) []byte {
	t.Helper()
	if len(encoded) == 0 {
		return nil
	}
	out := make([]byte, len(encoded))
	cipher, _ := rc4.NewCipher([]byte("CD&ML"))
	cipher.XORKeyStream(out, encoded)
	if out[0] != 0 {
		t.Fatalf("unexpected server compression selector %d", out[0])
	}
	return out[1:]
}
func (c *protocolClient) send(packet prudp.Packet) {
	c.t.Helper()
	packet.Source = 0x3f
	packet.Destination = 0x31
	if packet.Type != prudp.SYN {
		packet.Signature = c.cookie
		packet.Session = c.session
	}
	wire, err := packet.Marshal(0xbe)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.conn.Write(wire); err != nil {
		c.t.Fatal(err)
	}
}
func (c *protocolClient) read() prudp.Packet {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		c.t.Fatal(err)
	}
	wire := make([]byte, 65535)
	n, err := c.conn.Read(wire)
	if err != nil {
		c.t.Fatal(err)
	}
	p, used, err := prudp.ParseFrame(wire[:n], 0xbe)
	if err != nil || used != n {
		c.t.Fatalf("wire packet: %x %v", wire[:n], err)
	}
	return p
}
func connectProtocol(t *testing.T, addr netip.AddrPort, security []byte) (*protocolClient, []byte) {
	t.Helper()
	conn, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(addr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := &protocolClient{t: t, conn: conn, session: 0x27, sequence: 2}
	c.send(prudp.Packet{Type: prudp.SYN, Flags: prudp.FlagNeedsAck})
	syn := c.read()
	if syn.Type != prudp.SYN || syn.Flags&prudp.FlagAck == 0 {
		t.Fatalf("SYN reply %+v", syn)
	}
	c.cookie = syn.ConnectionSignature
	c.send(prudp.Packet{Type: prudp.CONNECT, Flags: prudp.FlagReliable | prudp.FlagNeedsAck, Sequence: 1, ConnectionSignature: 0x12345678, Payload: wrapRPC(security)})
	ack := c.read()
	if ack.Type != prudp.CONNECT || ack.Flags&prudp.FlagAck == 0 || ack.Signature != 0x12345678 || ack.Session == 0 {
		t.Fatalf("CONNECT reply %+v", ack)
	}
	return c, unwrapRPC(t, ack.Payload)
}
func (c *protocolClient) exchange(protocol byte, method uint32, args []byte) quazal.Response {
	c.t.Helper()
	c.call++
	message, err := quazal.EncodeRequest(quazal.Request{Protocol: protocol, Method: method, CallID: c.call, Args: args})
	if err != nil {
		c.t.Fatal(err)
	}
	c.send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagReliable | prudp.FlagNeedsAck | prudp.FlagSize, Sequence: c.sequence, Payload: wrapRPC(message)})
	c.sequence++
	for n := 0; n < 8; n++ {
		p := c.read()
		if p.Flags&prudp.FlagAck != 0 {
			continue
		}
		if p.Type != prudp.DATA || p.Fragment != 0 {
			c.t.Fatalf("unexpected RPC packet %+v", p)
		}
		c.send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagAck, Sequence: p.Sequence, Fragment: p.Fragment})
		reply, err := quazal.ParseResponse(unwrapRPC(c.t, p.Payload))
		if err != nil {
			c.t.Fatal(err)
		}
		if reply.Protocol != protocol || reply.CallID != c.call {
			c.t.Fatalf("RPC failed %+v", reply)
		}
		return reply
	}
	c.t.Fatal("RPC response not received")
	return quazal.Response{}
}
func (c *protocolClient) rpc(protocol byte, method uint32, args []byte) quazal.Response {
	c.t.Helper()
	reply := c.exchange(protocol, method, args)
	if !reply.Success {
		c.t.Fatalf("RPC failed %+v", reply)
	}
	return reply
}
func sdkString(value string) []byte {
	out := make([]byte, 2+len(value)+1)
	binary.LittleEndian.PutUint16(out, uint16(len(value)+1))
	copy(out[2:], value)
	return out
}
func u32(value uint32) []byte { var b [4]byte; binary.LittleEndian.PutUint32(b[:], value); return b[:] }
func takeBuffer(t *testing.T, data []byte) ([]byte, []byte) {
	t.Helper()
	if len(data) < 4 {
		t.Fatal("missing Buffer length")
	}
	n := uint64(binary.LittleEndian.Uint32(data))
	if n > uint64(len(data)-4) {
		t.Fatal("truncated Buffer")
	}
	end := 4 + int(n)
	return data[4:end], data[end:]
}
func loginTicket(t *testing.T, addr netip.AddrPort, name, password string, securePort uint16) (uint32, quazalcrypto.ServiceTicketEnvelope) {
	t.Helper()
	client, _ := connectProtocol(t, addr, nil)
	response := client.rpc(10, 1, sdkString(name))
	if len(response.Payload) < 8 || binary.LittleEndian.Uint32(response.Payload) != quazal.Success {
		t.Fatal("invalid login result")
	}
	pid := binary.LittleEndian.Uint32(response.Payload[4:])
	key := quazalcrypto.PasswordKey([]byte(password), pid)
	proof, urlData := takeBuffer(t, response.Payload[8:])
	if len(urlData) < 2 {
		t.Fatal("missing advertised secure URL")
	}
	urlLength := int(binary.LittleEndian.Uint16(urlData))
	if urlLength < 1 || urlLength > len(urlData)-2 {
		t.Fatal("invalid advertised secure URL")
	}
	url := string(urlData[2 : 2+urlLength])
	if !strings.HasPrefix(url, "prudps:/") || !strings.Contains(url, ";port="+strconv.Itoa(int(securePort))+";") {
		t.Fatalf("incorrect bound secure port in %q", url)
	}
	if _, err := quazalcrypto.Open(key, proof); err != nil {
		t.Fatalf("login proof: %v", err)
	}
	response = client.rpc(10, 3, append(u32(pid), u32(2)...))
	if len(response.Payload) < 4 || binary.LittleEndian.Uint32(response.Payload) != quazal.Success {
		t.Fatal("invalid ticket result")
	}
	sealed, rest := takeBuffer(t, response.Payload[4:])
	if len(rest) != 0 {
		t.Fatal("ticket trailing bytes")
	}
	envelope, err := quazalcrypto.OpenServiceTicket(key, sealed)
	if err != nil {
		t.Fatal(err)
	}
	return pid, envelope
}
func secureProtocol(t *testing.T, addr netip.AddrPort, pid uint32, ticket quazalcrypto.ServiceTicketEnvelope, nonce uint32) *protocolClient {
	t.Helper()
	payload, err := quazalcrypto.BuildConnect(ticket.Ticket, ticket.SessionKey, quazalcrypto.Challenge{PrincipalID: pid, Nonce: nonce})
	if err != nil {
		t.Fatal(err)
	}
	client, reply := connectProtocol(t, addr, payload)
	if err := quazalcrypto.VerifyConnectReply(reply, nonce); err != nil {
		t.Fatal(err)
	}
	response := client.rpc(11, 1, append(u32(1), sdkString("prudps:/address=192.0.2.40;port=3658;stream=3;sid=15;type=2")...))
	if len(response.Payload) < 8 || binary.LittleEndian.Uint32(response.Payload) != quazal.Success {
		t.Fatal("registration failed")
	}
	if len(response.Payload) < 10 {
		t.Fatal("registration URL missing")
	}
	client.connectionID = binary.LittleEndian.Uint32(response.Payload[4:])
	n := int(binary.LittleEndian.Uint16(response.Payload[8:]))
	if n < 1 || n > len(response.Payload)-10 || !strings.HasPrefix(string(response.Payload[10:10+n]), "prudps:/") {
		t.Fatal("TDU registration StationURL scheme changed")
	}
	return client
}

func TestRuntimeGuestSignupLoginTicketsAndBootstrapOverUDP(t *testing.T) {
	cfg := localConfig(t)
	addresses := startAdapter(t, cfg)
	guestPID, guestTicket := loginTicket(t, addresses.Auth, "guest", "h7fyctiuucf", addresses.Secure.Port())
	guest := secureProtocol(t, addresses.Secure, guestPID, guestTicket, 0x11111111)
	if got := guest.rpc(104, 1, nil); !bytes.Equal(got.Payload, u32(binary.BigEndian.Uint32(cfg.PublicIP.AsSlice()))) {
		t.Fatal("registered guest bootstrap policy changed")
	}
	if result := guest.exchange(104, 1, []byte{1}); result.Success {
		t.Fatal("bootstrap accepted arguments")
	}
	if result := guest.exchange(104, 2, nil); result.Success {
		t.Fatal("guest reached unsupported operation")
	}
	initial := quazalcrypto.InitialKey([]byte("synthetic-password"))
	args := append(sdkString("driver"), sdkString(hex.EncodeToString(initial[:]))...)
	args = append(args, u32(0x20)...)
	args = append(args, sdkString("unused@example.invalid")...)
	signup := guest.rpc(25, 1, args)
	if !bytes.Equal(signup.Payload, u32(quazal.Success)) {
		t.Fatal("signup not accepted")
	}
	pid, ticket := loginTicket(t, addresses.Auth, "driver", "synthetic-password", addresses.Secure.Port())
	if pid == guestPID {
		t.Fatal("account used guest identity")
	}
	account := secureProtocol(t, addresses.Secure, pid, ticket, 0x22222222)
	if result := account.exchange(104, 2, nil); result.Success {
		t.Fatal("named account reached unsupported TDU operation")
	}
	bootstrap := account.rpc(104, 1, nil)
	ip := cfg.PublicIP.As4()
	if !bytes.Equal(bootstrap.Payload, u32(binary.BigEndian.Uint32(ip[:]))) {
		t.Fatalf("wrong bootstrap %x", bootstrap.Payload)
	}
}
