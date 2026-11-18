package tdu

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/proxycrypto"
)

// This is a test-side native client. Standard-library MAC verification checks
// both handshake transcripts; no retail traffic or game files are fixtures.
type nativeClient struct {
	conn     *net.UDPConn
	id       uint16
	send     *proxycrypto.Sender
	receive  *proxycrypto.Receiver
	publicIP netip.Addr
}

func nativeControl(id, command uint16, payload []byte) []byte {
	b := make([]byte, 10+len(payload))
	binary.BigEndian.PutUint16(b, 4)
	binary.BigEndian.PutUint16(b[2:], id)
	binary.BigEndian.PutUint16(b[4:], command)
	copy(b[10:], payload)
	return b
}
func nativeSign(id, command uint16, payload, transcript []byte) []byte {
	key, _ := hex.DecodeString("f2296c20e809a0cf98464de7b2ee75c2af1c24f50a83b3ce0d19d5c11cc1f70a")
	b := nativeControl(id, command, payload)
	mac := hmac.New(sha256.New, key)
	mac.Write(transcript)
	mac.Write(b)
	return append(b, mac.Sum(nil)...)
}
func (c *nativeClient) write(t *testing.T, b []byte) {
	t.Helper()
	if _, e := c.conn.Write(b); e != nil {
		t.Fatal(e)
	}
}
func (c *nativeClient) read(t *testing.T) []byte {
	t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	b := make([]byte, 1600)
	n, e := c.conn.Read(b)
	if e != nil {
		t.Fatal(e)
	}
	return b[:n]
}
func (c *nativeClient) verify(t *testing.T, b []byte, command uint16, transcript []byte) []byte {
	t.Helper()
	if len(b) < 42 || binary.BigEndian.Uint16(b) != 4 || binary.BigEndian.Uint16(b[2:]) != c.id || binary.BigEndian.Uint16(b[4:]) != command {
		t.Fatalf("handshake %x", b)
	}
	payload := b[10 : len(b)-32]
	if !bytes.Equal(b, nativeSign(c.id, command, payload, transcript)) {
		t.Fatal("handshake transcript MAC mismatch")
	}
	return payload
}
func nativeHandshake(t *testing.T, addresses Addresses, ip netip.Addr) *nativeClient {
	t.Helper()
	conn, e := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(addresses.Proxy))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close() })
	c := &nativeClient{conn: conn, publicIP: ip}
	var nonce [32]byte
	for i := range nonce {
		nonce[i] = byte(i + 40)
	}
	c.write(t, proxycrypto.EncodeClientHello(proxycrypto.RegionUS, nonce))
	hello := c.read(t)
	if len(hello) < 4 {
		t.Fatal("short hello")
	}
	c.id = binary.BigEndian.Uint16(hello[2:])
	serverPublic := c.verify(t, hello, 2, nonce[:])
	if len(serverPublic) != 140 {
		t.Fatal("server DH length")
	}
	var exponent [128]byte
	exponent[127] = 7
	private, e := proxycrypto.NewDHPrivate(exponent[:])
	if e != nil {
		t.Fatal(e)
	}
	clientPublic := private.Public()
	transcript := append(bytes.Clone(nonce[:]), serverPublic...)
	c.write(t, nativeSign(c.id, 3, clientPublic[:], transcript))
	transcript = append(transcript, clientPublic[:]...)
	c.verify(t, c.read(t), 8, transcript)
	shared, e := private.Shared(serverPublic)
	if e != nil {
		t.Fatal(e)
	}
	keys, e := proxycrypto.DeriveKeys(shared)
	if e != nil {
		t.Fatal(e)
	}
	c.send, _ = proxycrypto.NewSender(keys, proxycrypto.ClientToProxy)
	c.receive, _ = proxycrypto.NewReceiver(keys, proxycrypto.ProxyToClient)
	wire, e := c.send.Seal(nativeControl(c.id, 29, nil))
	if e != nil {
		t.Fatal(e)
	}
	c.write(t, wire)
	plain, _, e := c.receive.Open(c.read(t))
	if e != nil || len(plain) != 10 || binary.BigEndian.Uint16(plain[4:]) != 30 {
		t.Fatalf("activation %x %v", plain, e)
	}
	return c
}
func (c *nativeClient) call(t *testing.T, port uint16, id uint32, action []byte) []byte {
	t.Helper()
	frame, e := (ednet.Frame{Kind: ednet.Call, RequestID: id, Timeout: 1000, Action: action}).MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	plain := make([]byte, 10+len(frame))
	binary.BigEndian.PutUint16(plain, 2)
	binary.BigEndian.PutUint16(plain[2:], c.id)
	binary.BigEndian.PutUint16(plain[4:], port)
	ip := c.publicIP.As4()
	copy(plain[6:10], ip[:])
	copy(plain[10:], frame)
	wire, e := c.send.Seal(plain)
	if e != nil {
		t.Fatal(e)
	}
	c.write(t, wire)
	reply, _, e := c.receive.Open(c.read(t))
	if e != nil || len(reply) < 10 {
		t.Fatalf("reply %x %v", reply, e)
	}
	if binary.BigEndian.Uint16(reply[4:]) != port || !bytes.Equal(reply[6:10], ip[:]) {
		t.Fatalf("wrong virtual source %x", reply[:10])
	}
	f, e := ednet.ParseFrame(reply[10:])
	if e != nil || f.Kind != ednet.Reply || f.RequestID != id {
		t.Fatalf("RPC reply %+v %v", f, e)
	}
	return f.Action
}
func nativeOpen(name string) []byte {
	b := make([]byte, 14+len(name))
	binary.BigEndian.PutUint16(b, ednet.OpenRequest)
	b[2] = 2
	binary.BigEndian.PutUint16(b[7:], uint16(len(name)))
	copy(b[9:], name)
	return b
}
func nativeGet(id uint32) []byte {
	b := make([]byte, 10)
	binary.BigEndian.PutUint16(b, ednet.GetRequest)
	binary.BigEndian.PutUint32(b[2:], id)
	return b
}
func TestNativeRuntimeDirectoryAndPersistentResources(t *testing.T) {
	cfg := localConfig(t)
	cfg.DirectoryPort = 19000
	cfg.ResourcePort = 19001
	// Synthetic transport fixtures only: these are not asserted to be a complete
	// game-accepted configuration or a reconstruction of retail XML assets.
	dir := t.TempDir()
	netXML := []byte("<NETCONFIG><TEST_ONLY value=\"1\"/></NETCONFIG>")
	prizeXML := []byte("<TEST_ONLY_PRIZE/>")
	cfg.NetConfigPath = filepath.Join(dir, "netconfig.xml")
	cfg.PrizePath = filepath.Join(dir, "prize.xml")
	if e := os.WriteFile(cfg.NetConfigPath, netXML, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(cfg.PrizePath, prizeXML, 0600); e != nil {
		t.Fatal(e)
	}
	addresses := startAdapter(t, cfg)
	c := nativeHandshake(t, addresses, cfg.PublicIP)
	request := make([]byte, 12)
	binary.BigEndian.PutUint16(request, ednet.DirectoryRequest)
	for i, id := range []uint16{ednet.OpenRequest, 0xdead, ednet.GetRequest, ednet.DirectoryRequest, 0xbeef} {
		binary.BigEndian.PutUint16(request[2+i*2:], id)
	}
	reply := c.call(t, cfg.DirectoryPort, 1, request)
	if len(reply) != 42 || binary.BigEndian.Uint16(reply) != ednet.DirectoryReply {
		t.Fatalf("directory %x", reply)
	}
	for i, port := range []uint16{cfg.ResourcePort, 0, cfg.ResourcePort, cfg.DirectoryPort, 0} {
		row := reply[2+i*8:]
		if binary.BigEndian.Uint16(row[6:]) != port {
			t.Fatalf("directory slot%d %x", i, row[:8])
		}
		if port == 0 && !bytes.Equal(row[2:6], make([]byte, 4)) {
			t.Fatal("advertised unavailable service")
		}
	}
	key, e := os.ReadFile(filepath.Join(cfg.StateDir, "p2p.key"))
	if e != nil || len(key) != 16 {
		t.Fatalf("persisted key %v length%d", e, len(key))
	}
	for i, file := range []struct {
		name string
		data []byte
	}{{"p2pkey", key}, {"netconfig.xml", netXML}, {"prize.xml", prizeXML}} {
		tx := uint32(10 + i*2)
		opened := c.call(t, cfg.ResourcePort, tx, nativeOpen(file.name))
		if len(opened) != 19 || opened[2] != 0 || binary.BigEndian.Uint32(opened[7:]) != uint32(len(file.data)) {
			t.Fatalf("open %s %x", file.name, opened)
		}
		fileID := binary.BigEndian.Uint32(opened[3:])
		got := c.call(t, cfg.ResourcePort, tx+1, nativeGet(fileID))
		if len(got) < 15 || got[2] != 0 || !bytes.Equal(got[15:], file.data) {
			t.Fatalf("get %s %x", file.name, got)
		}
		duplicate := c.call(t, cfg.ResourcePort, tx+1, nativeGet(fileID))
		if !bytes.Equal(got, duplicate) {
			t.Fatal("retry changed resource")
		}
	}
	missing := c.call(t, cfg.ResourcePort, 90, nativeOpen("missing.xml"))
	if len(missing) != 19 || missing[2] == 0 {
		t.Fatalf("missing file got success %x", missing)
	}
}
