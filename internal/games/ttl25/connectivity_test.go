package ttl25

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func TestForcedRelayAdvertisesEarlyURLsAndKeepsNATIntroductionVirtual(t *testing.T) {
	f := newCommunityFixture(t)
	r, _, _, host, guest := relayTestFixture(t)
	r.cfg.Mode = "relay"
	s := f.c.sessions
	s.relay = r
	f.pa, f.pb = host.peer, guest.peer
	s.registered(f.pa, f.a.PID, host.url)
	s.registered(f.pb, f.b.PID, guest.url)
	b, err := s.register(f.pa, f.a.PID, sessionArgs("Forced native room", 0))
	if err != nil {
		t.Fatal(err)
	}
	gid := binary.LittleEndian.Uint32(b)
	b, err = s.lobby(f.pa, f.a.PID, quazal.Request{Method: 26, Args: rankingString(rankingU32(nil, gid), host.url)})
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, true)
	b, err = s.lobby(f.pb, f.b.PID, quazal.Request{Method: 16, Args: rankingU32(nil, gid)})
	reader := rankingReader{b: b}
	if err != nil || reader.u32() != 1 {
		t.Fatal("early host URL list", err)
	}
	virtual := reader.str()
	if !reader.done() || virtual == host.url || stationField(virtual, "RVCID") != stationField(host.url, "RVCID") {
		t.Fatal("early lookup leaked direct host address")
	}
	urls, err := s.connectionURLs(f.pb, f.b.PID, 0, f.a.PID)
	if err != nil || len(urls) != 1 || urls[0] != virtual || len(r.pairs) != 1 {
		t.Fatal("later lookup changed virtual address", err)
	}
	b, err = s.lobby(f.pb, f.b.PID, quazal.Request{Method: 28, Args: rankingU32(nil, gid)})
	reader = rankingReader{b: b}
	if err != nil || !reader.boolean() || reader.str() != virtual || !reader.done() {
		t.Fatal("single host URL bypassed forced route", err)
	}
	b, err = s.gatheringURLs(f.pb, f.b.PID, rankingU32(rankingU32(nil, 1), gid))
	reader = rankingReader{b: b}
	if err != nil || reader.u32() != 1 || reader.u32() != gid || reader.u32() != 1 || reader.str() != virtual || !reader.done() {
		t.Fatal("grouped host URL bypassed forced route", err)
	}
	targets, err := s.probeTargets(f.pb, f.b.PID, []string{virtual})
	if err != nil || len(targets) != 0 {
		t.Fatal("virtual probe requested a direct introduction", err)
	}
	instructions := r.drainInstructions()
	if len(instructions) != 1 || instructions[0].Peer != f.pa || instructions[0].URL == guest.url {
		t.Fatal("host not instructed toward guest virtual endpoint")
	}
	if _, err = s.probeTargets(f.pb, f.b.PID, []string{host.url}); err == nil {
		t.Fatal("cached direct URL bypassed forced mode")
	}
	if _, err = s.probeTargets(f.pd, f.d.PID, []string{virtual}); err == nil {
		t.Fatal("unregistered caller used another connection's issued URL")
	}
	if _, err = s.probeTargets(f.pb, f.b.PID, []string{strings.Replace(virtual, "CID=11", "CID=999", 1)}); err == nil {
		t.Fatal("modified virtual URL accepted")
	}
	s.mu.Lock()
	room := s.rooms[gid]
	room.fields["isPrivate"] = "1"
	s.rooms[gid] = room
	s.mu.Unlock()
	if _, err = s.probeTargets(f.pb, f.b.PID, []string{virtual}); err == nil {
		t.Fatal("stale public URL bypassed new private policy")
	}
}

func TestProbeTargetsRequirePublishedAccessibleLiveHost(t *testing.T) {
	f := newCommunityFixture(t)
	s := f.c.sessions
	s.registered(f.pa, f.a.PID, "observed-host")
	s.registered(f.pb, f.b.PID, "observed-guest")
	guestURL := "prudps:/address=127.0.0.1;port=40001;stream=3;sid=15;PID=1001;CID=2;type=2"
	s.registered(f.pb, f.b.PID, guestURL)
	b, err := s.register(f.pa, f.a.PID, sessionArgs("Probe room", 0))
	if err != nil {
		t.Fatal(err)
	}
	gid := binary.LittleEndian.Uint32(b)
	url := "prudps:/address=127.0.0.1;port=40000;stream=3;sid=15;PID=1000;CID=1;type=2;RVCID=1"
	b, err = s.lobby(f.pa, f.a.PID, quazal.Request{Method: 26, Args: rankingString(rankingU32(nil, gid), url)})
	if err != nil || len(b) != 1 || b[0] != 1 {
		t.Fatal("publish host", err)
	}
	targets, err := s.probeTargets(f.pb, f.b.PID, []string{url, url})
	if err != nil || len(targets) != 1 || targets[0] != f.pa {
		t.Fatal("published host not resolved", err)
	}
	targets, err = s.probeTargets(f.pb, f.b.PID, []string{url, guestURL, nativeStationURL(guestURL)})
	if err != nil || len(targets) != 1 || targets[0] != f.pa {
		t.Fatal("retry self URL rejected or introduced", err)
	}
	if got := s.urls[ownerOf(f.pb, f.b.PID)]; got != guestURL+";RVCID=2" {
		t.Fatal("native participant URL missing RVCID")
	}
	// An accessible gathering may also contain live non-host participants.
	s.mu.Lock()
	memberRoom := s.rooms[gid]
	memberRoom.members[ownerOf(f.pb, f.b.PID)] = ""
	s.rooms[gid] = memberRoom
	s.mu.Unlock()
	s.registered(f.pd, f.d.PID, "observed-third")
	targets, err = s.probeTargets(f.pd, f.d.PID, []string{url, nativeStationURL(guestURL)})
	if err != nil || len(targets) != 2 {
		t.Fatal("live gathering participants not admitted", err)
	}
	// Remove the synthetic membership before checking private invitation policy.
	s.mu.Lock()
	memberRoom = s.rooms[gid]
	delete(memberRoom.members, ownerOf(f.pb, f.b.PID))
	s.rooms[gid] = memberRoom
	s.mu.Unlock()
	for _, selectors := range [][2]uint32{{0, f.a.PID}, {1, f.a.PID}, {1, 0}} {
		urls, err := s.connectionURLs(f.pb, f.b.PID, selectors[0], selectors[1])
		if err != nil || len(urls) != 1 || urls[0] != url {
			t.Fatal("host connection URL not resolved", err)
		}
	}
	for _, selectors := range [][2]uint32{{0, 0}, {2, f.a.PID}, {1, f.b.PID}, {0, 99999}} {
		urls, err := s.connectionURLs(f.pb, f.b.PID, selectors[0], selectors[1])
		if err != nil || len(urls) != 0 {
			t.Fatal("invalid connection selector exposed URL", err)
		}
	}
	if _, err := s.probeTargets(f.pb, f.b.PID, []string{"prudps:/address=127.0.0.1;port=9999"}); err == nil {
		t.Fatal("arbitrary probe endpoint accepted")
	}
	s.mu.Lock()
	room := s.rooms[gid]
	room.fields["isPrivate"] = "1"
	s.rooms[gid] = room
	s.mu.Unlock()
	if _, err := s.probeTargets(f.pb, f.b.PID, []string{url}); err == nil {
		t.Fatal("uninvited private room probed")
	}
	if urls, err := s.connectionURLs(f.pb, f.b.PID, 0, f.a.PID); err != nil || len(urls) != 0 {
		t.Fatal("private connection URL exposed", err)
	}
	s.mu.Lock()
	s.invites[gid] = map[uint32]string{f.b.PID: ""}
	s.mu.Unlock()
	if _, err := s.probeTargets(f.pb, f.b.PID, []string{url}); err != nil {
		t.Fatal("invited private host", err)
	}
	if urls, err := s.connectionURLs(f.pb, f.b.PID, 0, f.a.PID); err != nil || len(urls) != 1 {
		t.Fatal("invited connection URL missing", err)
	}
	s.disconnect(f.pa, f.a.PID)
	if urls, err := s.connectionURLs(f.pb, f.b.PID, 0, f.a.PID); err != nil || len(urls) != 0 {
		t.Fatal("disconnected connection URL exposed", err)
	}
	if _, err := s.probeTargets(f.pb, f.b.PID, []string{url}); err == nil {
		t.Fatal("disconnected host probed")
	}
}

func TestRelayCandidatesRequireAccessibleLiveRoomAndPreserveHostIdentity(t *testing.T) {
	f := newCommunityFixture(t)
	r, _, _, host, guest := relayTestFixture(t)
	s := f.c.sessions
	s.relay = r
	f.pa, f.pb = host.peer, guest.peer
	s.registered(f.pa, f.a.PID, host.url)
	s.registered(f.pb, f.b.PID, guest.url)
	b, err := s.register(f.pa, f.a.PID, sessionArgs("Private relay room", 1))
	if err != nil {
		t.Fatal(err)
	}
	gid := binary.LittleEndian.Uint32(b)
	b, err = s.lobby(f.pa, f.a.PID, quazal.Request{Method: 26, Args: rankingString(rankingU32(nil, gid), host.url)})
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, true)
	urls, err := s.connectionURLs(f.pb, f.b.PID, 0, f.a.PID)
	if err != nil || len(urls) != 0 || len(r.pairs) != 0 {
		t.Fatal("private uninvited relay access", err)
	}
	invite := rankingString(rankingU32(rankingU32(rankingU32(nil, gid), 1), f.b.PID), "")
	b, err = s.operation(f.pa, f.a.PID, quazal.Request{Method: 5, Args: invite})
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, true)
	urls, err = s.connectionURLs(f.pb, f.b.PID, 0, f.a.PID)
	if err != nil || len(urls) != 2 || urls[0] != host.url || stationField(urls[1], "RVCID") != stationField(host.url, "CID") || stationField(urls[1], "PID") != stationField(host.url, "PID") {
		t.Fatal("host fallback candidates", err)
	}
	if urls, err = s.connectionURLs(f.pb, f.b.PID, 999, f.a.PID); err != nil || len(urls) != 0 {
		t.Fatal("wrong selector returned relay")
	}
	s.disconnect(f.pa, f.a.PID)
	if len(r.pairs) != 0 {
		t.Fatal("disconnected host retained virtual endpoints")
	}
	if urls, err = s.connectionURLs(f.pb, f.b.PID, 0, f.a.PID); err != nil || len(urls) != 0 {
		t.Fatal("dead host returned candidate")
	}
}
