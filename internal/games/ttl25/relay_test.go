package ttl25

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"slices"
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
func relayTestFixture(t *testing.T) (*nativeRelay, *net.UDPConn, *net.UDPConn, relayIdentity, relayIdentity) {
	t.Helper()
	a, b := relaySocket(t, "127.0.0.1"), relaySocket(t, "127.0.0.1")
	cfg := RelayConfig{Mode: "adaptive", Bind: netip.MustParseAddr("127.0.0.1"), PublicIP: netip.MustParseAddr("127.0.0.1"), FirstPort: 30200, LastPort: 30203}
	r, err := newNativeRelay(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r.listen = func(uint16) (*net.UDPConn, error) {
		return net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
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

func TestAdaptiveBrowseProbeDoesNotActivateGameplay(t *testing.T) {
	r, a, b, ai, bi := relayTestFixture(t)
	r.offer(123, ai, bi, bi.url)
	p := r.pairs[relayPairKey{123, ai.owner, bi.owner}]
	endpoint := p.sockets[1].LocalAddr().(*net.UDPAddr).AddrPort()
	stranger := relaySocket(t, "127.0.0.2")
	nonce := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	stranger.WriteToUDPAddrPort(probePacket(0, ai.cid, nonce), endpoint)
	relayQuiet(t, stranger)
	a.WriteToUDPAddrPort(probePacket(0, ai.cid+1, nonce), endpoint)
	relayQuiet(t, a)
	a.WriteToUDPAddrPort(probePacket(0, ai.cid, nonce), endpoint)
	reply, _ := relayRead(t, a)
	pong, _, err := prudp.ParseFrame(reply, 0)
	if err != nil || pong.Type != prudp.USER || pong.Payload[0] != 1 || binary.LittleEndian.Uint32(pong.Payload[1:]) != bi.cid || !bytes.Equal(pong.Payload[5:], nonce) {
		t.Fatal("browse did not receive native alternate reachability reply", err)
	}
	challenge, from := relayRead(t, a)
	cp, _, err := prudp.ParseFrame(challenge, 0)
	if err != nil || cp.Payload[0] != 0 {
		t.Fatal("missing mapping proof", err)
	}
	a.WriteToUDPAddrPort(probePacket(1, ai.cid, cp.Payload[5:]), from)
	otherEndpoint := p.sockets[0].LocalAddr().(*net.UDPAddr).AddrPort()
	b.WriteToUDPAddrPort(probePacket(0, bi.cid, nonce), otherEndpoint)
	relayRead(t, b) // reachability reply
	bChallenge, bFrom := relayRead(t, b)
	bp, _, err := prudp.ParseFrame(bChallenge, 0)
	if err != nil || bp.Payload[0] != 0 {
		t.Fatal("missing other mapping proof", err)
	}
	b.WriteToUDPAddrPort(probePacket(1, bi.cid, bp.Payload[5:]), bFrom)
	data, _ := (prudp.Packet{Source: 0x11, Destination: 0x11, Type: prudp.DATA, Payload: []byte("not yet connected")}).Marshal(0)
	a.WriteToUDPAddrPort(data, endpoint)
	relayQuiet(t, b)
	r.mu.Lock()
	active, proved, packets := p.active, p.mapping[0].proved && p.mapping[1].proved, p.packets
	r.mu.Unlock()
	if active || !proved || packets != [2]uint64{} || len(r.drainInstructions()) != 0 {
		t.Fatal("reachability check activated native forwarding")
	}
	syn, _ := (prudp.Packet{Source: 0x11, Destination: 0x11, Type: prudp.SYN, Flags: prudp.FlagNeedsAck | prudp.FlagSize, ConnectionSignature: 123}).Marshal(0)
	a.WriteToUDPAddrPort(syn, endpoint)
	forwarded, _ := relayRead(t, b)
	if !bytes.Equal(forwarded, syn) {
		t.Fatal("selected fallback did not forward native SYN")
	}
	r.mu.Lock()
	active = p.active
	r.mu.Unlock()
	intro := r.drainInstructions()
	if !active || len(intro) != 1 || intro[0].Peer != bi.peer {
		t.Fatal("selecting alternate did not activate the existing candidate")
	}
}

func TestNativeProbeMatchesRetailFrame(t *testing.T) {
	// Successful two-PC capture: USER stream 5, request, RVCID 1, timing echo.
	want, err := hex.DecodeString("515105000000000000000001000000dcd54100000000009b")
	if err != nil {
		t.Fatal(err)
	}
	got := probePacket(0, 1, []byte{0xdc, 0xd5, 0x41, 0, 0, 0, 0, 0})
	if !bytes.Equal(got, want) {
		t.Fatalf("native framing/checksum mismatch: %x", got)
	}
	if _, _, err := prudp.ParseFrame(want, 0xff); err == nil {
		t.Fatal("backend seed accepted native frame")
	}
}

func TestAdaptiveRelayDirectSilenceAndProvenOpaqueFallback(t *testing.T) {
	r, a, b, ai, bi := relayTestFixture(t)
	urls := r.offer(10000, ai, bi, bi.url)
	if len(urls) != 2 || urls[0] != bi.url || stationField(urls[1], "RVCID") != "12" || stationField(urls[1], "CID") != "12" || stationField(urls[1], "type") != "2" {
		t.Fatal("direct-first identity candidates", urls)
	}
	if got := r.offer(10000, ai, bi, bi.url); !slices.Equal(got, urls) {
		t.Fatal("lookup reallocated route")
	}
	relayQuiet(t, a)
	relayQuiet(t, b)
	if len(r.drainInstructions()) != 0 {
		t.Fatal("direct candidate sent a relay introduction")
	}
	r.mu.Lock()
	p := r.pairs[relayPairKey{10000, ai.owner, bi.owner}]
	r.mu.Unlock()
	endpointB := p.sockets[1].LocalAddr().(*net.UDPAddr).AddrPort()
	syn, _ := (prudp.Packet{Source: 0x11, Destination: 0x11, Type: prudp.SYN, Flags: prudp.FlagNeedsAck | prudp.FlagSize, ConnectionSignature: 123}).Marshal(0)
	// A forged proof/probe cannot activate forwarding, nor can a source from
	// an address other than the authenticated registration's observed IP.
	stranger := relaySocket(t, "127.0.0.2")
	stranger.WriteToUDPAddrPort(syn, endpointB)
	relayQuiet(t, stranger)
	a.WriteToUDPAddrPort(probePacket(1, ai.cid, make([]byte, 8)), endpointB)
	relayQuiet(t, a)
	a.WriteToUDPAddrPort(syn, endpointB)
	challenge, from := relayRead(t, a)
	packet, _, err := prudp.ParseFrame(challenge, 0)
	if err != nil || packet.Type != prudp.USER || len(packet.Payload) != 13 || packet.Payload[0] != 0 || binary.LittleEndian.Uint32(packet.Payload[1:]) != bi.cid {
		t.Fatal("native proof challenge", err)
	}
	// A replay with a wrong nonce cannot release the buffered session SYN.
	wrong := bytes.Clone(packet.Payload[5:])
	wrong[0] ^= 1
	a.WriteToUDPAddrPort(probePacket(1, ai.cid, wrong), from)
	relayQuiet(t, b)
	a.WriteToUDPAddrPort(probePacket(1, ai.cid, packet.Payload[5:]), from)
	var intro []quazal.ProbeInstruction
	deadline := time.Now().Add(time.Second)
	for len(intro) == 0 && time.Now().Before(deadline) {
		intro = r.drainInstructions()
		if len(intro) == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	if len(intro) != 1 || intro[0].Peer != bi.peer || intro[0].URL != p.urls[0] {
		t.Fatal("host mapping instruction not scoped to pair")
	}
	endpointA := p.sockets[0].LocalAddr().(*net.UDPAddr).AddrPort()
	// Different local socket models an endpoint-dependent NAT mapping. The
	// backend-observed control port is never assumed to be the relay mapping.
	bMapped := relaySocket(t, "127.0.0.1")
	bMapped.WriteToUDPAddrPort(probePacket(0, bi.cid, []byte{1, 2, 3, 4, 5, 6, 7, 8}), endpointA)
	natReply, _ := relayRead(t, bMapped)
	rp, _, err := prudp.ParseFrame(natReply, 0)
	if err != nil || rp.Payload[0] != 1 || binary.LittleEndian.Uint32(rp.Payload[1:]) != ai.cid {
		t.Fatal("native relay probe reply", err)
	}
	bChallenge, sourceA := relayRead(t, bMapped)
	cp, _, err := prudp.ParseFrame(bChallenge, 0)
	if err != nil {
		t.Fatal(err)
	}
	bMapped.WriteToUDPAddrPort(probePacket(1, bi.cid, cp.Payload[5:]), sourceA)
	forwarded, senderA := relayRead(t, bMapped)
	if !bytes.Equal(forwarded, syn) || senderA != endpointA {
		t.Fatal("buffered native SYN changed or wrong virtual source")
	}
	opaque := []byte("opaque native handshake and gameplay")
	bMapped.WriteToUDPAddrPort(opaque, endpointA)
	got, senderB := relayRead(t, a)
	if !bytes.Equal(got, opaque) || senderB != endpointB {
		t.Fatal("reverse opaque datagram changed")
	}
	a.WriteToUDPAddrPort(opaque, endpointB)
	got, _ = relayRead(t, bMapped)
	if !bytes.Equal(got, opaque) {
		t.Fatal("forward gameplay changed")
	}
	relayQuiet(t, b)
	r.removeParticipant(ai.owner, 10000)
	r.mu.Lock()
	closing := p.closing
	now := closing.Add(-time.Second)
	r.now = func() time.Time { return now }
	remaining, ports, queued := len(r.pairs), len(r.ports), len(r.instructions)
	r.mu.Unlock()
	if closing.IsZero() || remaining != 1 || ports != 2 || queued != 0 {
		t.Fatal("proved route must briefly drain after control-plane exit")
	}
	if _, _, ok := r.probeTarget(ai.owner, urls[1]); ok {
		t.Fatal("draining route admitted a new introduction")
	}
	if got := r.offer(10000, ai, bi, bi.url); !slices.Equal(got, []string{bi.url}) {
		t.Fatal("draining route was advertised again")
	}
	// Retail sends reliable leave DATA after the control-plane quit reply.
	// Forward its ACK too, without allowing new handshakes or mapping proofs.
	leave, err := (prudp.Packet{Source: 0x11, Destination: 0x11, Type: prudp.DATA, Flags: prudp.FlagNeedsAck | prudp.FlagSize, Payload: []byte("native leave")}).Marshal(0)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := (prudp.Packet{Source: 0x11, Destination: 0x11, Type: prudp.DATA, Flags: prudp.FlagAck | prudp.FlagSize}).Marshal(0)
	if err != nil {
		t.Fatal(err)
	}
	a.WriteToUDPAddrPort(leave, endpointB)
	got, senderA = relayRead(t, bMapped)
	if !bytes.Equal(got, leave) || senderA != endpointA {
		t.Fatal("native leave lost during exit drain")
	}
	bMapped.WriteToUDPAddrPort(ack, endpointA)
	got, senderB = relayRead(t, a)
	if !bytes.Equal(got, ack) || senderB != endpointB {
		t.Fatal("native leave ACK lost during exit drain")
	}
	a.WriteToUDPAddrPort(syn, endpointB)
	relayQuiet(t, bMapped)
	a.WriteToUDPAddrPort(probePacket(0, ai.cid, make([]byte, 8)), endpointB)
	relayQuiet(t, a)
	r.removeParticipant(bi.owner, 10000)
	r.mu.Lock()
	unchangedDeadline := p.closing == closing
	now = closing
	r.mu.Unlock()
	if !unchangedDeadline {
		t.Fatal("repeat exit extended drain deadline")
	}
	r.tick()
	r.mu.Lock()
	remaining, ports, queued = len(r.pairs), len(r.ports), len(r.instructions)
	r.mu.Unlock()
	if remaining != 0 || ports != 0 || queued != 0 {
		t.Fatal("drained route leaked resources")
	}
}

func TestRelayCandidateExpiryCapacityAndForcedMode(t *testing.T) {
	r, _, _, a, b := relayTestFixture(t)
	now := time.Unix(1, 0)
	r.now = func() time.Time { return now }
	if len(r.offer(1, a, b, b.url)) != 2 || len(r.offer(2, a, b, b.url)) != 2 {
		t.Fatal("pool capacity")
	}
	if got := r.offer(3, a, b, b.url); len(got) != 1 || got[0] != b.url {
		t.Fatal("capacity must preserve direct path")
	}
	now = now.Add(relayCandidateTTL)
	r.tick()
	if len(r.pairs) != 0 || len(r.ports) != 0 {
		t.Fatal("unused direct fallback retained ports")
	}
	r.cfg.Mode = "relay"
	got := r.offer(3, a, b, b.url)
	if len(got) != 1 || got[0] == b.url {
		t.Fatal("forced relay candidate")
	}
	r.removeParticipant(b.owner, 0)
	if len(r.pairs) != 0 {
		t.Fatal("disconnect retained route")
	}
}

func TestForcedRelayReusesReversePairAndNeverFallsBackToDirectOnCapacity(t *testing.T) {
	r, _, _, a, b := relayTestFixture(t)
	r.cfg.Mode = "relay"
	forward := r.offer(10, a, b, b.url)
	reverse := r.offer(10, b, a, a.url)
	if len(forward) != 1 || len(reverse) != 1 || len(r.pairs) != 1 || forward[0] == reverse[0] {
		t.Fatal("reverse lookup allocated a second pair")
	}
	if target, gid, ok := r.probeTarget(b.owner, reverse[0]); !ok || gid != 10 || target != a.owner {
		t.Fatal("reverse virtual probe identity")
	}
	r.offer(11, a, b, b.url)
	if got := r.offer(12, a, b, b.url); len(got) != 0 {
		t.Fatal("forced capacity returned direct endpoint")
	}
	r.requestProbe(a.owner, forward[0])
	queued := r.drainInstructions()
	if len(queued) != 1 || queued[0].Peer != b.peer || queued[0].URL != reverse[0] {
		t.Fatal("forced introduction not paired with matching reverse endpoint")
	}
}
