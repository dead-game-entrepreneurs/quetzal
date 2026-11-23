package ttl25

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func relaySocket(t *testing.T, ip string) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(ip)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func relayRead(t *testing.T, c *net.UDPConn) ([]byte, netip.AddrPort) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4096)
	n, from, err := c.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf[:n], from
}
func relayQuiet(t *testing.T, c *net.UDPConn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	buf := make([]byte, 4096)
	if _, _, err := c.ReadFromUDPAddrPort(buf); err == nil {
		t.Fatal("unexpected relay traffic")
	} else if e, ok := err.(net.Error); !ok || !e.Timeout() {
		t.Fatal(err)
	}
}
func relayTestFixture(t *testing.T, mode ...string) (*nativeRelay, *net.UDPConn, *net.UDPConn, relayIdentity, relayIdentity) {
	t.Helper()
	a, b := relaySocket(t, "127.0.0.1"), relaySocket(t, "127.0.0.1")
	cfg := RelayConfig{Mode: "adaptive", Bind: netip.MustParseAddr("127.0.0.1"), PublicIP: netip.MustParseAddr("127.0.0.1"), FirstPort: 30200, LastPort: 30263}
	if len(mode) > 0 {
		cfg.Mode = mode[0]
	}
	r, err := newNativeRelayWithListener(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), func(uint16) (*net.UDPConn, error) {
		return net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	identity := func(c *net.UDPConn, id uint64, pid, cid uint32) relayIdentity {
		peer := prudp.Peer{ID: id, Addr: c.LocalAddr().(*net.UDPAddr).AddrPort(), LocalPort: 30121}
		url := relayStationURL("prudp:/stream=3;sid=15;PID="+strconvPID(pid), peer.Addr, cid)
		return relayIdentity{ownerOf(peer, pid), peer, cid, url}
	}
	return r, a, b, identity(a, 1, 1000, 11), identity(b, 2, 1001, 12)
}
func strconvPID(pid uint32) string { return fmt.Sprint(pid) }

func relayEndpoint(t *testing.T, url string) netip.AddrPort {
	t.Helper()
	parsed, err := quazal.ParseStationURL(url)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Address
}

func nativeProbe(kind byte, cid uint32, nonce []byte) []byte {
	payload := make([]byte, 13)
	payload[0] = kind
	binary.LittleEndian.PutUint32(payload[1:], cid)
	copy(payload[5:], nonce)
	wire, _ := (prudp.Packet{Source: 0x51, Destination: 0x51, Type: prudp.USER, Payload: payload}).Marshal(0)
	return wire
}

func proveRelay(t *testing.T, conn *net.UDPConn, endpoint netip.AddrPort, cid uint32) {
	t.Helper()
	nonce := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	conn.WriteToUDPAddrPort(nativeProbe(0, cid, nonce), endpoint)
	wire, _ := relayRead(t, conn)
	p, _, err := prudp.ParseFrame(wire, 0)
	if err != nil || len(p.Payload) != 13 || p.Payload[0] != 1 || !bytes.Equal(p.Payload[5:], nonce) {
		t.Fatal("native relay reachability reply", err)
	}
	wire, from := relayRead(t, conn)
	p, _, err = prudp.ParseFrame(wire, 0)
	if err != nil || len(p.Payload) != 13 || p.Payload[0] != 0 {
		t.Fatal("native relay proof challenge", err)
	}
	conn.WriteToUDPAddrPort(nativeProbe(1, cid, p.Payload[5:]), from)
}

func TestAdaptiveBrowseProbeDoesNotActivateGameplay(t *testing.T) {
	r, a, b, ai, bi := relayTestFixture(t)
	forward := r.offer(123, ai, bi, bi.url)
	reverse := r.offer(123, bi, ai, ai.url)
	endpoint := relayEndpoint(t, forward[1])
	stranger := relaySocket(t, "127.0.0.2")
	stranger.WriteToUDPAddrPort(nativeProbe(0, ai.cid, make([]byte, 8)), endpoint)
	relayQuiet(t, stranger)
	a.WriteToUDPAddrPort(nativeProbe(0, ai.cid+1, make([]byte, 8)), endpoint)
	relayQuiet(t, a)
	proveRelay(t, a, endpoint, ai.cid)
	proveRelay(t, b, relayEndpoint(t, reverse[1]), bi.cid)
	deadline := time.Now().Add(time.Second)
	for r.core.Snapshot().ProvedMappings != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	r.requestProbe(ai.owner, forward[1])
	data, _ := (prudp.Packet{Source: 0x11, Destination: 0x11, Type: prudp.DATA, Payload: []byte("not yet connected")}).Marshal(0)
	a.WriteToUDPAddrPort(data, endpoint)
	relayQuiet(t, b)
	state := r.core.Snapshot()
	if state.Active != 0 || state.ProvedMappings != 2 || state.ForwardedPackets != 0 || len(r.drainInstructions()) != 0 {
		t.Fatal("browse reachability activated forwarding or queued an introduction", state)
	}
	syn, _ := (prudp.Packet{Source: 0x11, Destination: 0x11, Type: prudp.SYN, Flags: prudp.FlagNeedsAck, ConnectionSignature: 123}).Marshal(0)
	a.WriteToUDPAddrPort(syn, endpoint)
	wire, _ := relayRead(t, b)
	intro := r.drainInstructions()
	if !bytes.Equal(wire, syn) || r.core.Snapshot().Active != 1 || len(intro) != 1 || intro[0].Peer != bi.peer {
		t.Fatal("selecting alternate did not activate existing candidate")
	}
}
