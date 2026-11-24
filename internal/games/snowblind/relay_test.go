package snowblind

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal/peerrelay"
)

func snowblindRelayFixture(t *testing.T, mode, publicIP string) (*sessions, *peerrelay.Relay, []prudp.Peer, []string, uint32) {
	t.Helper()
	s, peers, urls := sessionFixture(t)
	c := DefaultConfig()
	c.Bind = netip.MustParseAddr("127.0.0.1")
	c.PublicIP = netip.MustParseAddr(publicIP)
	c.RelayMode = mode
	c.RelayLastPort = c.RelayFirstPort + 7
	r, err := peerrelay.New(peerrelay.Options{Config: c.relayConfig(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Listen: func(uint16) (*net.UDPConn, error) {
		return net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	s.relay = r
	s.relayMode = mode
	id := createMatch(t, s, peers[0])
	callMatch(t, s, peers[0], 1000, 21, 11, messageArgs(id, ""))
	callMatch(t, s, peers[0], 1000, 21, 26, messageArgs(id, urls[0]))
	return s, r, peers, urls, id
}
func snowblindReturnedURLs(t *testing.T, s *sessions, p prudp.Peer, pid, gid uint32) []string {
	t.Helper()
	r := quazal.NewReader(callMatch(t, s, p, pid, 21, 16, gidArgs(gid)))
	n := r.U32()
	if n > 64 {
		t.Fatal("unbounded URLs")
	}
	urls := []string{}
	for range n {
		urls = append(urls, r.String(512))
	}
	if !r.Done() {
		t.Fatal("URL list fields")
	}
	return urls
}
func TestSnowblindAdaptiveCandidatesAuthorizationAndCleanup(t *testing.T) {
	s, r, peers, urls, gid := snowblindRelayFixture(t, "adaptive", "127.0.0.1")
	candidates := snowblindReturnedURLs(t, s, peers[1], 1001, gid)
	if len(candidates) != 2 || candidates[0] != quazal.WithRVCID(urls[0]) {
		t.Fatal("adaptive candidates not direct-first", candidates)
	}
	for _, url := range candidates {
		p, err := quazal.ParseStationURL(url)
		if err != nil || p.PID != 1000 || p.Fields["CID"] != "7" || p.Fields["RVCID"] != "7" {
			t.Fatal("native identity lost", err)
		}
	}
	if got := snowblindReturnedURLs(t, s, peers[1], 1001, gid); !slices.Equal(got, candidates) || r.Snapshot().Candidates != 1 {
		t.Fatal("repeat lookup allocated another pair")
	}
	lookup, err := s.connectionURLs(peers[1], 1001, 7, 1000)
	if err != nil || !slices.Equal(lookup, candidates) {
		t.Fatal("CID callback bypassed adaptive candidates", lookup, err)
	}
	if got, err := s.connectionURLs(peers[1], 1001, 99, 1000); err != nil || len(got) != 0 {
		t.Fatal("foreign CID lookup admitted")
	}
	query := encode(func(w *quazal.Writer) { w.String(queryPrefix + "1" + queryPopulation) })
	found := quazal.NewReader(callMatch(t, s, peers[1], 1001, 101, 3, query))
	if found.U32() != 1 {
		t.Fatal("relay search result")
	}
	found.Any(2048)
	if found.U32() != 2 || found.String(512) != candidates[0] || found.String(512) != candidates[1] || !found.Done() {
		t.Fatal("browser candidates differ from participant URLs")
	}
	targets, err := s.probe(peers[1], 1001, candidates)
	if err != nil || len(targets) != 1 || targets[0] != peers[0] {
		t.Fatal("probe authority", targets, err)
	}
	instructions := r.DrainInstructions()
	if len(instructions) != 1 || instructions[0].Peer != peers[0] || instructions[0].URL == quazal.WithRVCID(urls[1]) || r.Snapshot().Active != 0 {
		t.Fatal("virtual counterpart introduction or premature activation")
	}
	if _, err := s.probe(peers[2], 1002, []string{candidates[1]}); err == nil {
		t.Fatal("another connection used unissued URL")
	}
	old := peers[1]
	fresh := old
	fresh.ID += 100
	s.registered(fresh, 1001, urls[1])
	if _, err := s.probe(fresh, 1001, []string{candidates[1]}); err == nil {
		t.Fatal("new peer generation stole old route")
	}
	callMatch(t, s, peers[1], 1001, 21, 11, messageArgs(gid, ""))
	callMatch(t, s, peers[1], 1001, 101, 5, messageArgs(gid, ""))
	if r.Snapshot().Candidates != 0 {
		t.Fatal("leave retained unused pair")
	}
	if _, err := s.probe(peers[1], 1001, []string{candidates[1]}); err == nil {
		t.Fatal("removed virtual route still accepted")
	}
}
func TestSnowblindSameIPAndForcedModes(t *testing.T) {
	for _, mode := range []string{"adaptive", "relay", "direct"} {
		t.Run(mode, func(t *testing.T) {
			s, r, peers, urls, gid := snowblindRelayFixture(t, mode, "192.0.2.10")
			candidates := snowblindReturnedURLs(t, s, peers[1], 1001, gid)
			if len(candidates) != 1 {
				t.Fatal("same-IP candidates merged by native cache", candidates)
			}
			host := quazal.NewReader(callMatch(t, s, peers[1], 1001, 21, 28, gidArgs(gid)))
			if !host.Bool() || host.String(512) != candidates[0] || !host.Done() {
				t.Fatal("single host URL bypassed configured mode")
			}
			batch := quazal.NewReader(callMatch(t, s, peers[1], 1001, 50, 4, encode(func(w *quazal.Writer) { w.U32(1); w.U32(gid) })))
			if batch.U32() != 1 || batch.U32() != gid || batch.U32() != 1 || batch.String(512) != candidates[0] || !batch.Done() {
				t.Fatal("URL batch bypassed configured mode")
			}
			if mode == "direct" {
				if candidates[0] != quazal.WithRVCID(urls[0]) || r.Snapshot().Candidates != 0 {
					t.Fatal("direct mode used relay")
				}
				return
			}
			if candidates[0] == quazal.WithRVCID(urls[0]) {
				t.Fatal("same-IP virtual candidate missing")
			}
			if mode == "relay" {
				if _, err := s.probe(peers[1], 1001, []string{urls[0]}); err == nil {
					t.Fatal("forced direct bypass")
				}
			}
			if _, err := s.probe(peers[1], 1001, candidates); err != nil {
				t.Fatal(err)
			}
			if mode == "adaptive" && r.Snapshot().Active != 0 {
				t.Fatal("probe activated adaptive forwarding")
			}
			if mode == "relay" && r.Snapshot().Active != 1 {
				t.Fatal("forced route not activated")
			}
			callMatch(t, s, peers[1], 1001, 21, 11, messageArgs(gid, ""))
			returned := snowblindReturnedURLs(t, s, peers[1], 1001, gid)
			if len(returned) != 2 || returned[1] != quazal.WithRVCID(urls[1]) {
				t.Fatal("own URL rewritten or candidate grouping lost")
			}
			s.disconnect(peers[0], 1000)
			if r.Snapshot().Candidates != 0 || s.count() != 0 {
				t.Fatal("host disconnect leaked route")
			}
		})
	}
}
func TestSnowblindParticipantRelayRequiresSharedRoom(t *testing.T) {
	s, _, peers, urls, gid := snowblindRelayFixture(t, "adaptive", "127.0.0.1")
	callMatch(t, s, peers[1], 1001, 21, 11, messageArgs(gid, ""))
	if got, err := s.connectionURLs(peers[2], 1002, 8, 1001); err != nil || len(got) != 0 {
		t.Fatal("outsider discovered participant")
	}
	if _, err := s.probe(peers[2], 1002, []string{urls[1]}); err == nil {
		t.Fatal("outsider probed participant")
	}
	callMatch(t, s, peers[2], 1002, 21, 11, messageArgs(gid, "")) // Fixture capacity is two; this join fails.
	if got, err := s.connectionURLs(peers[2], 1002, 8, 1001); err != nil || len(got) != 0 {
		t.Fatal("failed join acquired relay access")
	}
}

func TestSnowblindMixedProbeWithSameIPRequesterIntroducesOnlyVirtual(t *testing.T) {
	// Host uses .10; requester and advertised relay both use .11. The host
	// must not see direct .11:4659 plus virtual .11:relay-port for one RVCID.
	s, r, peers, urls, gid := snowblindRelayFixture(t, "adaptive", "192.0.2.11")
	candidates := snowblindReturnedURLs(t, s, peers[1], 1001, gid)
	if len(candidates) != 2 {
		t.Fatal("host should retain direct-first candidates")
	}
	targets, err := s.probe(peers[1], 1001, candidates)
	if err != nil || len(targets) != 0 {
		t.Fatal("mixed batch queued conflicting direct requester introduction", targets, err)
	}
	instructions := r.DrainInstructions()
	if len(instructions) != 1 || instructions[0].Peer != peers[0] || instructions[0].URL == quazal.WithRVCID(urls[1]) || peerrelay.Field(instructions[0].URL, "address") != "192.0.2.11" {
		t.Fatal("virtual counterpart instruction missing", instructions)
	}
	if r.Snapshot().Active != 0 {
		t.Fatal("browser introduction activated gameplay")
	}
	// An explicit direct-only request continues to use the regular NAT path.
	targets, err = s.probe(peers[1], 1001, candidates[:1])
	if err != nil || len(targets) != 1 || targets[0] != peers[0] {
		t.Fatal("direct-only probe changed", targets, err)
	}
}

func TestSnowblindLobbyRemovalReleasesRoundRoutes(t *testing.T) {
	s, r, peers, _, gid := snowblindRelayFixture(t, "adaptive", "127.0.0.1")
	snowblindReturnedURLs(t, s, peers[1], 1001, gid)
	round := gathering{Kind: "Gathering", Gathering: quazal.Gathering{Description: "BO3Round", Policy: 1}}
	body, _ := round.encode()
	rid := quazal.NewReader(callMatch(t, s, peers[0], 1000, 21, 1, body)).U32()
	callMatch(t, s, peers[0], 1000, 21, 11, messageArgs(rid, ""))
	callMatch(t, s, peers[0], 1000, 21, 26, messageArgs(rid, s.peers[identity(peers[0], 1000)].url))
	snowblindReturnedURLs(t, s, peers[1], 1001, rid)
	if r.Snapshot().Candidates != 2 {
		t.Fatal("round/lobby routes were not isolated")
	}
	callMatch(t, s, peers[0], 1000, 21, 2, gidArgs(gid))
	if r.Snapshot().Candidates != 0 || s.count() != 0 {
		t.Fatal("lobby removal retained a child relay")
	}
}

func natWire(t *testing.T, kind byte, cid uint32, nonce []byte) []byte {
	t.Helper()
	body := make([]byte, 13)
	body[0] = kind
	binary.LittleEndian.PutUint32(body[1:], cid)
	copy(body[5:], nonce)
	b, err := (prudp.Packet{Source: 0x51, Destination: 0x51, Type: prudp.USER, Payload: body}).Marshal(0)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSnowblindNATPayloadMatchesNativeWriter(t *testing.T) {
	b, err := os.ReadFile("testdata/nat-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Payload string `json:"payload_hex"`
	}
	if err := json.Unmarshal(b, &native); err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString(native.Payload)
	if err != nil {
		t.Fatal(err)
	}
	frame := natWire(t, 0, 7, []byte{0x44, 0x33, 0x22, 0x11, 0, 0, 0, 0})
	parsed, consumed, err := prudp.ParseFrame(frame, 0)
	if err != nil || consumed != len(frame) || !bytes.Equal(parsed.Payload, want) {
		t.Fatal("relay probe payload differs from native writer", err)
	}
}
func readRelayPacket(t *testing.T, c *net.UDPConn) ([]byte, netip.AddrPort) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 2048)
	n, from, err := c.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf[:n], from
}
func TestSnowblindRelayForwardsAfterNativeProofAndSYN(t *testing.T) {
	s, r, peers, _, gid := snowblindRelayFixture(t, "adaptive", "127.0.0.1")
	sockets := make([]*net.UDPConn, 2)
	for i, ip := range []string{"127.0.0.2", "127.0.0.3"} {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(ip)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		sockets[i] = c
		delete(s.peers, identity(peers[i], 1000+uint32(i)))
		peers[i].Addr = c.LocalAddr().(*net.UDPAddr).AddrPort()
		url := "prudp:/address=" + ip + ";port=" + strconv.Itoa(int(peers[i].Addr.Port())) + ";stream=3;sid=15;PID=" + strconv.Itoa(1000+i) + ";CID=" + strconv.Itoa(7+i) + ";type=2"
		s.registered(peers[i], 1000+uint32(i), url)
	}
	s.rooms[gid].url = s.peers[identity(peers[0], 1000)].url
	candidates := snowblindReturnedURLs(t, s, peers[1], 1001, gid)
	hostVirtual, err := quazal.ParseStationURL(candidates[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.probe(peers[1], 1001, []string{candidates[1]}); err != nil {
		t.Fatal(err)
	}
	instructions := r.DrainInstructions()
	if len(instructions) != 1 {
		t.Fatal("missing host introduction")
	}
	guestVirtual, err := quazal.ParseStationURL(instructions[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	destinations := []netip.AddrPort{guestVirtual.Address, hostVirtual.Address}
	for i, c := range sockets {
		c.WriteToUDPAddrPort(natWire(t, 0, uint32(7+i), []byte{1, 2, 3, 4, 5, 6, 7, 8}), destinations[i])
		reply, _ := readRelayPacket(t, c)
		p, _, err := prudp.ParseFrame(reply, 0)
		if err != nil || p.Payload[0] != 1 {
			t.Fatal("native probe reply", err)
		}
		challenge, from := readRelayPacket(t, c)
		p, _, err = prudp.ParseFrame(challenge, 0)
		if err != nil || p.Payload[0] != 0 {
			t.Fatal("native nonce challenge", err)
		}
		c.WriteToUDPAddrPort(natWire(t, 1, uint32(7+i), p.Payload[5:]), from)
	}
	deadline := time.Now().Add(time.Second)
	for r.Snapshot().ProvedMappings != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if state := r.Snapshot(); state.ProvedMappings != 2 || state.Active != 0 {
		t.Fatal("probe-only admission", state)
	}
	syn, err := (prudp.Packet{Source: 0x11, Destination: 0x11, Type: prudp.SYN, Flags: prudp.FlagNeedsAck, ConnectionSignature: 123}).Marshal(0)
	if err != nil {
		t.Fatal(err)
	}
	sockets[1].WriteToUDPAddrPort(syn, hostVirtual.Address)
	forwarded, from := readRelayPacket(t, sockets[0])
	if !bytes.Equal(forwarded, syn) || from != guestVirtual.Address || r.Snapshot().Active != 1 {
		t.Fatal("native alternate SYN not forwarded")
	}
	data, _ := (prudp.Packet{Source: 0x11, Destination: 0x11, Type: prudp.DATA, Flags: prudp.FlagReliable, Sequence: 2, Payload: []byte("opaque peer data")}).Marshal(0)
	sockets[0].WriteToUDPAddrPort(data, guestVirtual.Address)
	forwarded, from = readRelayPacket(t, sockets[1])
	if !bytes.Equal(forwarded, data) || from != hostVirtual.Address || r.Snapshot().ForwardedPackets < 2 {
		t.Fatal("opaque peer data or virtual source changed")
	}
}
