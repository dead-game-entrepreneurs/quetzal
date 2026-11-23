package ttl25

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func migrationFixture(t *testing.T, private byte) (*communityFixture, uint32) {
	t.Helper()
	f := newCommunityFixture(t)
	s := f.c.sessions
	for i, who := range []struct {
		peer prudp.Peer
		pid  uint32
	}{{f.pa, f.a.PID}, {f.pb, f.b.PID}, {f.pd, f.d.PID}} {
		s.registered(who.peer, who.pid, fmt.Sprintf("prudp:/address=127.0.0.1;port=%d;stream=3;sid=1;PID=%d;CID=%d;type=2", 40000+i, who.pid, i+1))
	}
	b, err := s.register(f.pa, f.a.PID, sessionArgs("Migration room", private))
	if err != nil {
		t.Fatal(err)
	}
	gid := binary.LittleEndian.Uint32(b)
	if private != 0 {
		s.invites[gid] = map[uint32]string{f.b.PID: ""}
	}
	for _, who := range []struct {
		peer prudp.Peer
		pid  uint32
	}{{f.pa, f.a.PID}, {f.pb, f.b.PID}} {
		migrationCall(t, s, who.peer, who.pid, 21, 11, rankingString(rankingU32(nil, gid), ""), true)
	}
	migrationCall(t, s, f.pa, f.a.PID, 21, 26, rankingString(rankingU32(nil, gid), s.urls[ownerOf(f.pa, f.a.PID)]), true)
	return f, gid
}

func migrationCall(t *testing.T, s *sessionRegistry, peer prudp.Peer, pid uint32, protocol byte, method uint32, args []byte, want bool) {
	t.Helper()
	request := quazal.Request{Protocol: protocol, Method: method, Args: args}
	var b []byte
	var err error
	if protocol == 50 {
		b, err = s.extension(peer, pid, request)
	} else {
		b, err = s.operation(peer, pid, request)
	}
	if err != nil {
		t.Fatalf("%d/%d: %v", protocol, method, err)
	}
	wantBool(t, b, want)
}

func TestRetailHostMigrationPreservesGatheringAndCanonicalizesCachedIdentity(t *testing.T) {
	for _, private := range []byte{0, 1} {
		t.Run(fmt.Sprintf("private=%d", private), func(t *testing.T) {
			f, gid := migrationFixture(t, private)
			s := f.c.sessions
			cached := s.rooms[gid].data
			cached.Payload = bytes.Clone(cached.Payload)
			// Preserve a stale native count in the cached update too.
			binary.LittleEndian.PutUint32(cached.Payload[len(cached.Payload)-13:], 2)
			oldURL := s.rooms[gid].url
			leave := rankingString(rankingU32(nil, gid), "")
			clearURL := rankingString(rankingU32(nil, gid), "")
			// An ordinary member cannot steal an actively owned room.
			migrationCall(t, s, f.pb, f.b.PID, 21, 27, clearURL, false)
			migrationCall(t, s, f.pa, f.a.PID, 50, 1, leave, true)
			if room, exists := s.rooms[gid]; !exists || room.migrationUntil.IsZero() || room.url != "" || len(room.members) != 1 {
				t.Fatal("departure did not retain an endpoint-free migration vacancy")
			}
			lookup, err := s.operation(f.pb, f.b.PID, quazal.Request{Method: 21, Args: rankingU32(nil, gid)})
			if err != nil || len(lookup) < 22 || lookup[0] != 1 {
				t.Fatal("remaining client lost its gathering record", err)
			}
			r := rankingReader{b: lookup[1:]}
			if r.str() != "TTLSession" {
				t.Fatal("lost type")
			}
			n := r.u32()
			data := r.take(int(n))
			if !r.done() || binary.LittleEndian.Uint32(data[4:]) != gid {
				t.Fatal("lost gathering ID")
			}
			// Invited outsiders and a different connection using the survivor's
			// account cannot acquire the vacancy or obtain its retained record.
			s.invites[gid] = map[uint32]string{f.d.PID: ""}
			otherPeer := f.pb
			otherPeer.ID += 100
			for _, who := range []struct {
				peer prudp.Peer
				pid  uint32
			}{{f.pd, f.d.PID}, {otherPeer, f.b.PID}, {f.pa, f.a.PID}} {
				migrationCall(t, s, who.peer, who.pid, 21, 27, clearURL, false)
				migrationCall(t, s, who.peer, who.pid, 21, 11, leave, false)
				lookup, err := s.operation(who.peer, who.pid, quazal.Request{Method: 21, Args: rankingU32(nil, gid)})
				if err != nil || len(lookup) == 0 || lookup[0] != 0 {
					t.Fatal("foreign connection retrieved a migrating record", err)
				}
			}
			if urls, err := s.connectionURLs(f.pb, f.b.PID, 0, f.a.PID); err != nil || len(urls) != 0 {
				t.Fatal("departed endpoint still published", err)
			}
			if _, err := s.probeTargets(f.pb, f.b.PID, []string{oldURL}); err == nil {
				t.Fatal("departed host still probeable")
			}
			b, err := s.search(sessionSearch("WHERE Gatherings.type = 'TTLSession'", 0, 25))
			if err != nil || !bytes.Equal(b, rankingU32(nil, 0)) {
				t.Fatal("vacancy remained discoverable", err)
			}
			b, err = s.operation(f.pd, f.d.PID, quazal.Request{Method: 20, Args: rankingU32(rankingU32(nil, 1), gid)})
			if err != nil || !bytes.Equal(b, rankingU32(nil, 0)) {
				t.Fatal("generic gathering list exposed a migration vacancy", err)
			}
			migrationCall(t, s, f.pb, f.b.PID, 21, 26, clearURL, false)
			migrationCall(t, s, f.pb, f.b.PID, 21, 27, clearURL, true)
			room := s.rooms[gid]
			if room.owner != ownerOf(f.pb, f.b.PID) || !room.migrationUntil.IsZero() || room.url != s.urls[room.owner] || room.fields["isPrivate"] != fmt.Sprint(private) {
				t.Fatal("native elected host or room policy not retained")
			}
			// The elected host may still have the original host's PID in its
			// cached record. The server authorizes the connection and rewrites
			// both wire identities rather than accepting them as authority.
			migrationCall(t, s, f.pb, f.b.PID, 21, 4, rankingAny(nil, cached), true)
			room = s.rooms[gid]
			if room.fields["totalConnectedPlayers"] != "1" {
				t.Fatal("cached update resurrected a departed player count")
			}
			if binary.LittleEndian.Uint32(room.data.Payload[8:]) != f.b.PID || binary.LittleEndian.Uint32(room.data.Payload[12:]) != f.b.PID {
				t.Fatal("cached identity overwrote successor authority")
			}
			migrationCall(t, s, f.pa, f.a.PID, 21, 4, rankingAny(nil, cached), false)
			if b, err := s.unregister(f.pa, f.a.PID, rankingU32(nil, gid)); err != nil || !bytes.Equal(b, boolReply(false)) {
				t.Fatal("departed owner deleted migrated gathering", err)
			}
			urls, err := s.connectionURLs(f.pd, f.d.PID, 0, f.b.PID)
			if err != nil || len(urls) != 1 || urls[0] != room.url {
				t.Fatal("new host is not discoverable", err)
			}
			if _, err := s.update(f.pb, f.b.PID, rankingAny(nil, quazal.AnyData{Payload: make([]byte, 50)})); err == nil {
				t.Fatal("empty-type zero-ID update accepted")
			}
		})
	}
}

func TestMigrationExpiresWithoutTrafficAndCannotBeRevived(t *testing.T) {
	f, gid := migrationFixture(t, 0)
	s := f.c.sessions
	now := time.Unix(100, 0)
	s.now = func() time.Time { return now }
	leave := rankingString(rankingU32(nil, gid), "")
	migrationCall(t, s, f.pa, f.a.PID, 50, 1, leave, true)
	deadline := s.rooms[gid].migrationUntil
	now = now.Add(10 * time.Second)
	s.disconnect(f.pa, f.a.PID)
	if s.rooms[gid].migrationUntil != deadline {
		t.Fatal("late disconnect extended migration lease")
	}
	now = deadline
	s.migrationInstructions() // transport idle tick, no client request required
	if len(s.rooms) != 0 || len(s.invites) != 0 {
		t.Fatal("abandoned migration did not expire")
	}
	migrationCall(t, s, f.pb, f.b.PID, 21, 27, leave, false)
}

func TestHostDisconnectAndOwnerCancelBothPermitMigration(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprint(disconnect), func(t *testing.T) {
			f, gid := migrationFixture(t, 0)
			s := f.c.sessions
			if disconnect {
				s.disconnect(f.pa, f.a.PID)
			} else {
				migrationCall(t, s, f.pa, f.a.PID, 21, 12, rankingString(rankingU32(nil, gid), ""), true)
			}
			migrationCall(t, s, f.pb, f.b.PID, 21, 27, rankingString(rankingU32(nil, gid), ""), true)
			if s.rooms[gid].owner != ownerOf(f.pb, f.b.PID) {
				t.Fatal("host did not migrate")
			}
		})
	}
}

func TestLastParticipantDisconnectRemovesPendingMigration(t *testing.T) {
	f, gid := migrationFixture(t, 0)
	s := f.c.sessions
	s.disconnect(f.pa, f.a.PID)
	s.disconnect(f.pb, f.b.PID)
	if _, exists := s.rooms[gid]; exists {
		t.Fatal("empty migration leaked")
	}
}

func TestMigrationPreservesSurvivorRelayAndRevokesDepartedRoutes(t *testing.T) {
	f, gid := migrationFixture(t, 0)
	s := f.c.sessions
	r, _, bSocket, a, b := relayTestFixture(t, "relay")
	s.relay = r
	f.pa, f.pb = a.peer, b.peer
	s.registered(f.pa, f.a.PID, a.url)
	s.registered(f.pb, f.b.PID, b.url)
	dSocket := relaySocket(t, "127.0.0.1")
	f.pd.Addr = dSocket.LocalAddr().(*net.UDPAddr).AddrPort()
	dURL := relayStationURL("prudp:/stream=3;sid=1;PID="+fmt.Sprint(f.d.PID), f.pd.Addr, 13)
	s.registered(f.pd, f.d.PID, dURL)
	now := time.Unix(100, 0)
	s.now = func() time.Time { return now }
	// Existing survivor-to-survivor routes must remain usable when a third
	// player, the original host, departs.
	third := relayIdentity{ownerOf(f.pd, f.d.PID), f.pd, 13, dURL}
	r.offer(gid, a, b, b.url)
	forward := r.offer(gid, b, third, third.url)
	reverse := r.offer(gid, third, b, b.url)
	room := s.rooms[gid]
	room.members[third.owner] = ""
	s.rooms[gid] = room
	virtualD := relayEndpoint(t, forward[0])
	r.requestProbe(b.owner, forward[0])
	proveRelay(t, bSocket, virtualD, b.cid)
	proveRelay(t, dSocket, relayEndpoint(t, reverse[0]), third.cid)
	deadline := time.Now().Add(time.Second)
	for r.core.Snapshot().ProvedMappings != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if state := r.core.Snapshot(); state.Active != 1 || state.ProvedMappings != 2 {
		t.Fatal("survivor route was not proved", state)
	}
	s.disconnect(f.pa, f.a.PID)
	if r.core.Snapshot().Candidates != 1 {
		t.Fatal("host exit removed a survivor route or retained the old host route")
	}
	migrationCall(t, s, f.pb, f.b.PID, 21, 27, rankingString(rankingU32(nil, gid), ""), true)
	if r.core.Snapshot().Candidates != 1 {
		t.Fatal("host claim replaced an existing survivor route")
	}
	payload := []byte("synthetic native gameplay after host departure")
	if _, err := bSocket.WriteToUDPAddrPort(payload, virtualD); err != nil {
		t.Fatal(err)
	}
	if wire, _ := relayRead(t, dSocket); !bytes.Equal(wire, payload) {
		t.Fatal("survivor relay did not forward after migration")
	}
	// Explicit removal must revoke every remaining pair, including pairs
	// between guests that were never owned by the original host.
	bResult, err := s.unregister(f.pb, f.b.PID, rankingU32(nil, gid))
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, bResult, true)
	if r.core.Snapshot().Draining != 1 {
		t.Fatal("removed room retained an active route instead of draining")
	}
	if _, _, issued := r.probeTarget(b.owner, forward[0]); issued {
		t.Fatal("removed room still admitted new guest-to-guest relay probes")
	}
	// The shared engine separately tests drain expiry and socket release.
}
