package snowblind

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func sessionFixture(t *testing.T) (*sessions, []prudp.Peer, []string) {
	t.Helper()
	s := newSessions(slog.New(slog.NewTextHandler(io.Discard, nil)), func(pid uint32) string { return fmt.Sprintf("player_%d", pid) })
	peers := []prudp.Peer{}
	urls := []string{}
	for i := 0; i < 3; i++ {
		p := prudp.Peer{ID: uint64(9 + i), LocalPort: 13016, Addr: netip.MustParseAddrPort(fmt.Sprintf("192.0.2.%d:4659", 10+i))}
		url := fmt.Sprintf("prudp:/address=%s;port=4659;stream=3;sid=15;PID=%d;CID=%d;type=2", p.Addr.Addr(), 1000+i, 7+i)
		peers = append(peers, p)
		urls = append(urls, url)
		s.registered(p, uint32(1000+i), url)
	}
	return s, peers, urls
}
func matchFixture() gathering {
	return gathering{Kind: "BO3Match", Gathering: quazal.Gathering{MinParticipants: 1, MaxParticipants: 2, Policy: 7, Flags: 16, Description: "Synthetic room"}, Platform: 1, Mode: 2, Mods: 2, Players: 1, Map: 300}
}
func gidArgs(id uint32) []byte { return encode(func(w *quazal.Writer) { w.U32(id) }) }
func messageArgs(id uint32, text string) []byte {
	return encode(func(w *quazal.Writer) { w.U32(id); w.String(text) })
}
func callMatch(t *testing.T, s *sessions, p prudp.Peer, pid uint32, protocol byte, method uint32, args []byte) []byte {
	t.Helper()
	b, err := s.handle(p, pid, quazal.Request{Protocol: protocol, Method: method, Args: args})
	if err != nil {
		t.Fatalf("%d/%d: %v", protocol, method, err)
	}
	return b
}
func createMatch(t *testing.T, s *sessions, p prudp.Peer) uint32 {
	t.Helper()
	b, err := matchFixture().encode()
	if err != nil {
		t.Fatal(err)
	}
	r := quazal.NewReader(callMatch(t, s, p, 1000, 21, 1, b))
	id := r.U32()
	if id < 10000 || !r.Done() {
		t.Fatal("assigned ID")
	}
	return id
}
func TestLobbySearchOwnershipCapacityAndCleanup(t *testing.T) {
	s, peers, urls := sessionFixture(t)
	host, joiner := peers[0], peers[1]
	id := createMatch(t, s, host)
	query := encode(func(w *quazal.Writer) { w.String(queryPrefix + "1" + queryPopulation) })
	search := func() []byte { return callMatch(t, s, joiner, 1001, 101, 3, query) }
	if !bytes.Equal(search(), []byte{0, 0, 0, 0}) {
		t.Fatal("unpublished lobby leaked")
	}
	result := quazal.NewReader(callMatch(t, s, host, 1000, 21, 21, gidArgs(id)))
	if !result.Bool() {
		t.Fatal("FindByID missing")
	}
	data := result.Any(2048)
	if data.Type != "BO3Match" || !result.Done() {
		t.Fatal("FindByID shape")
	}
	if !bytes.Equal(callMatch(t, s, host, 1000, 21, 11, messageArgs(id, "host")), []byte{1}) {
		t.Fatal("host participation")
	}
	if !bytes.Equal(callMatch(t, s, joiner, 1001, 21, 26, messageArgs(id, urls[1])), []byte{0}) {
		t.Fatal("foreign host mutation")
	}
	if !bytes.Equal(callMatch(t, s, host, 1000, 21, 26, messageArgs(id, urls[0])), []byte{1}) {
		t.Fatal("publish host")
	}
	r := quazal.NewReader(search())
	if r.U32() != 1 {
		t.Fatal("search result missing")
	}
	data = r.Any(2048)
	g, err := decodeGathering(encode(func(w *quazal.Writer) { w.Any(data) }))
	if err != nil || g.ID != id || g.OwnerPID != 1000 || g.HostPID != 1000 || g.Players != 1 {
		t.Fatal("search gathering", g, err)
	}
	if r.U32() != 1 || r.String(512) != quazal.WithRVCID(urls[0]) || !r.Done() {
		t.Fatal("search must include native URL list")
	}
	if !bytes.Equal(callMatch(t, s, joiner, 1001, 21, 11, messageArgs(id, "guest")), []byte{1}) {
		t.Fatal("join")
	}
	if !bytes.Equal(search(), []byte{0, 0, 0, 0}) {
		t.Fatal("full lobby offered")
	}
	if !bytes.Equal(callMatch(t, s, peers[2], 1002, 21, 11, messageArgs(id, "")), []byte{0}) {
		t.Fatal("capacity bypass")
	}
	details := quazal.NewReader(callMatch(t, s, host, 1000, 50, 3, encode(func(w *quazal.Writer) { w.U32(id); w.Bool(true) })))
	if details.U32() != 2 || details.U32() != 1000 || details.String(255) != "player_1000" || details.String(512) != "host" || details.U32() != 1001 || details.String(255) != "player_1001" || details.String(512) != "guest" || !details.Done() {
		t.Fatal("detailed participants")
	}
	updated := s.rooms[id].data
	updated.Mode = 3
	updated.Map = 301
	updated.Mods = 128
	body, _ := updated.encode()
	if b, err := s.handle(joiner, 1001, quazal.Request{Protocol: 21, Method: 4, Args: body}); err == nil && !bytes.Equal(b, []byte{0}) {
		t.Fatal("foreign update")
	}
	if !bytes.Equal(callMatch(t, s, host, 1000, 21, 4, body), []byte{1}) {
		t.Fatal("host update")
	}
	if !bytes.Equal(callMatch(t, s, joiner, 1001, 101, 5, messageArgs(id, "")), []byte{1}) || s.rooms[id].data.Players != 1 {
		t.Fatal("leave/count")
	}
	if s.rooms[id].data.Mode != 3 || s.rooms[id].data.Map != 301 {
		t.Fatal("metadata update lost")
	}
	targets, err := s.probe(joiner, 1001, []string{quazal.WithRVCID(urls[0])})
	if err != nil || len(targets) != 1 || targets[0].ID != host.ID {
		t.Fatal("published host probe", err)
	}
	if _, err = s.probe(host, 1000, []string{urls[2]}); err == nil {
		t.Fatal("unpublished peer probe")
	}
	found, err := s.connectionURLs(joiner, 1001, 7, 1000)
	if err != nil || len(found) != 1 {
		t.Fatal("CID lookup must use registration, not transport peer ID", err)
	}
	if found, err = s.connectionURLs(joiner, 1001, 9, 1000); err != nil || len(found) != 0 {
		t.Fatal("foreign CID lookup")
	}
	s.disconnect(host, 1000)
	if s.count() != 0 || !bytes.Equal(search(), []byte{0, 0, 0, 0}) {
		t.Fatal("stale lobby after disconnect")
	}
	if _, err = s.probe(joiner, 1001, []string{urls[0]}); err == nil {
		t.Fatal("stale host probe")
	}
}
func TestRegistrationBoundsAndRoundAssociation(t *testing.T) {
	s, peers, _ := sessionFixture(t)
	host := peers[0]
	body, _ := matchFixture().encode()
	for n := range len(body) {
		if _, err := s.handle(host, 1000, quazal.Request{Protocol: 21, Method: 1, Args: body[:n]}); err == nil {
			t.Fatalf("truncation %d", n)
		}
	}
	id := createMatch(t, s, host)
	if !bytes.Equal(callMatch(t, s, host, 1000, 21, 1, body), gidArgs(id)) {
		t.Fatal("duplicate registration")
	}
	round := gathering{Kind: "Gathering", Gathering: quazal.Gathering{Description: "BO3Round", Policy: 1}}
	b, _ := round.encode()
	r := quazal.NewReader(callMatch(t, s, host, 1000, 21, 1, b))
	rid := r.U32()
	if !r.Done() || rid == id {
		t.Fatal("round ID")
	}
	assoc := encode(func(w *quazal.Writer) { w.U32(1); w.U32(rid) })
	if !bytes.Equal(callMatch(t, s, peers[1], 1001, 26, 2, assoc), []byte{0}) {
		t.Fatal("foreign round association")
	}
	if !bytes.Equal(callMatch(t, s, host, 1000, 26, 2, assoc), []byte{1}) || s.rooms[rid].capacity != 2 {
		t.Fatal("round association/capacity")
	}
	if !bytes.Equal(callMatch(t, s, host, 1000, 21, 2, gidArgs(id)), []byte{1}) || s.count() != 0 {
		t.Fatal("round survived staging lobby removal")
	}
}
func TestNativeSearchFiltersAndRejectedGrammar(t *testing.T) {
	base := queryPrefix + "1" + queryPopulation
	g := matchFixture()
	for _, suffix := range []string{"", " AND BO3Matches.type=2", " AND BO3Matches.mapID=300", " AND (BO3Matches.type = 0 OR !(BO3Matches.mods & 1))", " AND (BO3Matches.mods & ~(1 | 128))=2", " AND !(BO3Matches.mods & 128)"} {
		p, err := parseQuery(base + suffix)
		if err != nil || !p(g) {
			t.Fatalf("native clause %q: %v", suffix, err)
		}
	}
	for _, suffix := range []string{" AND BO3Matches.type=3", " AND BO3Matches.mapID=0", " AND (BO3Matches.mods & 128)", " AND (BO3Matches.type = 0 OR (BO3Matches.mods & 1))"} {
		p, err := parseQuery(base + suffix)
		if err != nil || p(g) {
			t.Fatalf("filter mismatch %s %v", suffix, err)
		}
	}
	for _, query := range []string{"", base + " OR 1=1", base + ";", base + " AND BO3Matches.type=2 AND BO3Matches.type=2", base + " AND BO3Matches.mapID=4294967296", base + " AND (BO3Matches.mods & 65536)", base + " AND BO3Matches.playerName='x'"} {
		if _, err := parseQuery(query); err == nil {
			t.Fatalf("unsupported grammar %q", query)
		}
	}
	for _, bad := range []string{"prudp:/address=203.0.113.99;port=4659;PID=1000;CID=7", "prudp:/address=192.0.2.10;port=4659;PID=1001;CID=7", "prudp:/address=192.0.2.10;port=4659;PID=1000;CID=8"} {
		s, peers, _ := sessionFixture(t)
		if _, err := s.validateURL(identity(peers[0], 1000), bad); err == nil {
			t.Fatal("spoofed host URL", bad)
		}
	}
}

func TestGatheringURLBatchDeduplicatesAndBoundsRequests(t *testing.T) {
	s, peers, urls := sessionFixture(t)
	id := createMatch(t, s, peers[0])
	callMatch(t, s, peers[0], 1000, 21, 11, messageArgs(id, ""))
	callMatch(t, s, peers[0], 1000, 21, 26, messageArgs(id, urls[0]))
	args := encode(func(w *quazal.Writer) { w.U32(3); w.U32(id); w.U32(id); w.U32(9999) })
	r := quazal.NewReader(callMatch(t, s, peers[1], 1001, 50, 4, args))
	if r.U32() != 1 || r.U32() != id || r.U32() != 1 || r.String(512) != quazal.WithRVCID(urls[0]) || !r.Done() {
		t.Fatal("batch URL reply")
	}
	if _, err := s.handle(peers[1], 1001, quazal.Request{Protocol: 50, Method: 4, Args: gidArgs(33)}); err == nil {
		t.Fatal("unbounded URL batch")
	}
}
