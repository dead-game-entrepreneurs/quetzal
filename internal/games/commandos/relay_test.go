package commandos

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal/peerrelay"
)

func relayFixture(t *testing.T, mode string) (*sessions, *peerrelay.Relay, uint32) {
	t.Helper()
	return relayFixtureAt(t, mode, "127.0.0.1")
}
func relayFixtureAt(t *testing.T, mode, publicIP string) (*sessions, *peerrelay.Relay, uint32) {
	t.Helper()
	r, err := peerrelay.New(peerrelay.Options{Config: peerrelay.Config{Mode: mode, Bind: netip.MustParseAddr("127.0.0.1"), PublicIP: netip.MustParseAddr(publicIP), FirstPort: 30600, LastPort: 30607, ProbeCandidates: true, IntroduceCandidates: true}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Listen: func(uint16) (*net.UDPConn, error) {
		return net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	s := newSessions()
	s.relay = r
	s.relayMode = mode
	id := hostRoom(t, s, testPeer(1), 1000)
	s.registered(testPeer(2), 1001, testURL(1001))
	return s, r, id
}
func TestSameIPHostUsesSingleRelayCandidate(t *testing.T) {
	s, r, id := relayFixtureAt(t, "adaptive", "192.0.2.10")
	urls := returnedURLs(t, s, id)
	if len(urls) != 1 || urls[0] == quazal.WithRVCID(testURL(1000)) {
		t.Fatal("same-IP host retained indistinguishable direct candidate", urls)
	}
	hostURL, err := quazal.ParseStationURL(urls[0])
	if err != nil || hostURL.Address.Addr().String() != "192.0.2.10" || hostURL.PID != 1000 || hostURL.Fields["RVCID"] != "1" {
		t.Fatal("virtual host lost registered identity", urls, err)
	}
	if _, err := s.probe(testPeer(2), 1001, urls); err != nil {
		t.Fatal(err)
	}
	instructions := r.DrainInstructions()
	if len(instructions) != 1 || instructions[0].Peer != testPeer(1) || instructions[0].URL == quazal.WithRVCID(testURL(1001)) {
		t.Fatal("same-IP counterpart was not introduced via relay", instructions)
	}
	if state := r.Snapshot(); state.Candidates != 1 || state.Active != 0 {
		t.Fatal("browser probe activated gameplay before native SYN", state)
	}
	// Once admitted, the caller still gets its own registration unchanged.
	call(t, s, testPeer(2), 1001, 21, 11, messageArgs(id, ""))
	urls = returnedURLs(t, s, id)
	if len(urls) != 2 || urls[0] == quazal.WithRVCID(testURL(1000)) || urls[1] != quazal.WithRVCID(testURL(1001)) {
		t.Fatal("participant URL grouping or own endpoint changed", urls)
	}
	// The reverse room lookup must also select a single virtual endpoint.
	s.mu.Lock()
	reverse := s.participantURLs(identity(testPeer(1), 1000), id, s.rooms[id])
	s.mu.Unlock()
	if len(reverse) != 2 || reverse[0] != quazal.WithRVCID(testURL(1000)) || reverse[1] != instructions[0].URL {
		t.Fatal("reverse lookup reintroduced duplicate-IP candidates", reverse)
	}
}
func TestSameIPHostPreservesExplicitDirectMode(t *testing.T) {
	s, r, id := relayFixtureAt(t, "direct", "192.0.2.10")
	urls := returnedURLs(t, s, id)
	if len(urls) != 1 || urls[0] != quazal.WithRVCID(testURL(1000)) || r.Snapshot().Candidates != 0 {
		t.Fatal("direct mode allocated or selected a relay", urls)
	}
}
func returnedURLs(t *testing.T, s *sessions, id uint32) []string {
	t.Helper()
	reader := quazal.NewReader(call(t, s, testPeer(2), 1001, 21, 16, roomArgs(id)))
	count := reader.U32()
	var urls []string
	for range count {
		urls = append(urls, reader.String(512))
	}
	if !reader.Done() {
		t.Fatal("URL list shape")
	}
	return urls
}
func TestAdaptiveRoomCandidatesRetainDirectPathAndConnectionAuthority(t *testing.T) {
	s, r, id := relayFixture(t, "adaptive")
	urls := returnedURLs(t, s, id)
	if len(urls) != 2 || urls[0] != quazal.WithRVCID(testURL(1000)) || urls[1] == urls[0] {
		t.Fatal("direct-first candidates", urls)
	}
	for _, url := range urls {
		p, err := quazal.ParseStationURL(url)
		if err != nil || p.PID != 1000 || p.Fields["RVCID"] != "1" {
			t.Fatal("host identity changed", url, err)
		}
	}
	if r.Snapshot().Candidates != 1 || r.Snapshot().Active != 0 {
		t.Fatal("lookup activated gameplay")
	}
	if _, err := s.probe(testPeer(2), 1001, []string{urls[1]}); err != nil {
		t.Fatal(err)
	}
	if instructions := r.DrainInstructions(); len(instructions) != 1 || instructions[0].Peer != testPeer(1) || instructions[0].URL == testURL(1001) {
		t.Fatal("candidate introduction not virtual")
	}
	s.registered(testPeer(3), 1002, testURL(1002))
	if _, err := s.probe(testPeer(3), 1002, []string{urls[1]}); err == nil {
		t.Fatal("another connection used an unissued virtual endpoint")
	}
	call(t, s, testPeer(2), 1001, 21, 11, messageArgs(id, ""))
	call(t, s, testPeer(2), 1001, 21, 12, messageArgs(id, ""))
	if r.Snapshot().Candidates != 0 {
		t.Fatal("leave retained an unused pair")
	}
	if _, err := s.probe(testPeer(2), 1001, []string{urls[1]}); err == nil {
		t.Fatal("expired route still admitted")
	}
}
func TestForcedRoomCandidateRejectsDirectBypassAndHostExitCleansPairs(t *testing.T) {
	s, r, id := relayFixture(t, "relay")
	urls := returnedURLs(t, s, id)
	if len(urls) != 1 || urls[0] == quazal.WithRVCID(testURL(1000)) {
		t.Fatal("forced mode leaked direct endpoint")
	}
	if _, err := s.probe(testPeer(2), 1001, []string{testURL(1000)}); err == nil {
		t.Fatal("forced direct bypass")
	}
	if _, err := s.probe(testPeer(2), 1001, urls); err != nil {
		t.Fatal(err)
	}
	if r.Snapshot().Active != 1 {
		t.Fatal("forced probe did not activate route")
	}
	if !bytes.Equal(call(t, s, testPeer(1), 1000, 21, 12, messageArgs(id, "")), []byte{1}) {
		t.Fatal("host leave")
	}
	if r.Snapshot().Candidates != 0 || s.count() != 0 {
		t.Fatal("host exit retained relay/room")
	}
}
