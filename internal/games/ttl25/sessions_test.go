package ttl25

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func sessionArgs(name string, private byte) []byte {
	var body []byte
	for _, v := range []uint32{0, 0, 0, 1 | 16<<16, 7, 0, 16, 0} {
		body = rankingU32(body, v)
	}
	body = rankingString(body, name)
	body = rankingString(body, "Test Level")
	for _, v := range []uint32{13, 0, 240, 50, 0, 0, 16, 0, 1} {
		body = rankingU32(body, v)
	}
	body = append(body, 0, 0, private, 0, 0, 0, 0, 1, 0)
	return rankingAny(nil, quazal.AnyData{Type: "TTLSession", Payload: append(rankingU32(nil, uint32(len(body))), body...)})
}
func sessionSearch(query string, offset, count uint32) []byte {
	b := rankingString(nil, query)
	b = rankingU32(b, offset)
	return rankingU32(b, count)
}

const publicSessionQuery = "WHERE Gatherings.type = 'TTLSession' AND TTLSessions.isPrivate = 0"

func TestSessionsHaveRealOwnersAndFilteredListings(t *testing.T) {
	s := newSessions()
	a := prudp.Peer{ID: 1, LocalPort: 30121}
	other := prudp.Peer{ID: 2, LocalPort: 30121}
	args := sessionArgs("Room One", 0)
	reply, err := s.register(a, 1000, args)
	if err != nil || len(reply) != 4 {
		t.Fatal("registration", err)
	}
	id := binary.LittleEndian.Uint32(reply)
	if id < 10000 {
		t.Fatal("session overlaps shared ranking")
	}
	second, err := s.register(a, 1000, args)
	if err != nil || !bytes.Equal(reply, second) || len(s.rooms) != 1 {
		t.Fatal("duplicate registration")
	}
	if _, err := s.register(other, 1001, sessionArgs("Private Room", 1)); err != nil {
		t.Fatal(err)
	}
	got, err := s.search(sessionSearch(publicSessionQuery, 0, 25))
	if err != nil {
		t.Fatal(err)
	}
	r := rankingReader{b: got}
	if r.u32() != 1 || r.str() != "TTLSession" {
		t.Fatal("public listing count/type")
	}
	n := r.u32()
	data := r.take(int(n))
	if !r.done() || binary.LittleEndian.Uint32(data[4:]) != id || binary.LittleEndian.Uint32(data[8:]) != 1000 || binary.LittleEndian.Uint32(data[12:]) != 1000 {
		t.Fatal("assigned identity/owner/host")
	}
	for _, q := range []string{
		publicSessionQuery + " AND TTLSessions.levelName = 'Other Level'",
		publicSessionQuery + " AND TTLSessions.gameType = 2",
	} {
		got, err = s.search(sessionSearch(q, 0, 25))
		if err != nil || !bytes.Equal(got, []byte{0, 0, 0, 0}) {
			t.Fatal("nonmatching filter", err)
		}
	}
	// Same PID on a different connection cannot remove the host's room.
	got, err = s.unregister(other, 1000, rankingU32(nil, id))
	if err != nil || !bytes.Equal(got, []byte{0}) {
		t.Fatal("foreign deletion", err)
	}
	s.disconnect(other, 1000)
	if len(s.rooms) != 2 {
		t.Fatal("foreign disconnect deleted room")
	}
	s.disconnect(a, 1000)
	if len(s.rooms) != 1 {
		t.Fatal("host disconnect did not remove exactly its room")
	}
	got, err = s.search(sessionSearch(publicSessionQuery, 0, 25))
	if err != nil || !bytes.Equal(got, []byte{0, 0, 0, 0}) {
		t.Fatal("stale public listing", err)
	}
}

func TestSessionsRejectMalformedRecordsAndQueries(t *testing.T) {
	s := newSessions()
	p := prudp.Peer{ID: 1, LocalPort: 30121}
	args := sessionArgs("Room", 0)
	for n := 0; n < len(args); n++ {
		if _, err := s.register(p, 1000, args[:n]); err == nil {
			t.Fatalf("accepted truncated session %d", n)
		}
	}
	bad := bytes.Clone(args)
	bad[len(bad)-1] = 2
	if _, err := s.register(p, 1000, bad); err == nil {
		t.Fatal("invalid bool")
	}
	bad = bytes.Clone(args)
	binary.LittleEndian.PutUint32(bad[21:], 2000) // forged gathering ID
	if _, err := s.register(p, 1000, bad); err == nil {
		t.Fatal("forged identity")
	}
	if len(s.rooms) != 0 {
		t.Fatal("invalid requests mutated registry")
	}
	for _, query := range []string{
		"DROP TABLE Gatherings", publicSessionQuery + "; DELETE FROM Gatherings",
		publicSessionQuery + " AND TTLSessions.unknown = 1",
		publicSessionQuery + " AND TTLSessions.isPrivate = 1",
		"WHERE Gatherings.type = 'TTLSession' AND TTLSessions.isPrivate = 2",
	} {
		if _, err := s.search(sessionSearch(query, 0, 25)); err == nil {
			t.Fatalf("accepted query %q", query)
		}
	}
	if _, err := s.search(sessionSearch(publicSessionQuery, 0, 101)); err == nil {
		t.Fatal("unbounded browse")
	}
}
