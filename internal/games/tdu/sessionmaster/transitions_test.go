package sessionmaster

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
)

func TestActualNativeSerializersAndRequestBuilders(t *testing.T) {
	b, e := os.ReadFile("testdata/native-senders.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Free        string `json:"free_hex"`
		Client      string `json:"client_hex"`
		Restriction string `json:"restriction_frame_hex"`
		Update      string `json:"update_frame_hex"`
		Create      string `json:"create_frame_hex"`
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	decode := func(s string) []byte {
		b, e := hex.DecodeString(s)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	free, e := ParseFreeRide(decode(f.Free))
	if e != nil {
		t.Fatal(e)
	}
	if free.Identity != 0x0102030405060708 || free.Capacity != 8 || free.NAT != 3 || free.Bounds != (Bounds{-1500.5, 100.25, 1500.5, 3100.25}) {
		t.Fatalf("native FreeRide %+v", free)
	}
	encoded, e := free.MarshalBinary()
	if e != nil || !bytes.Equal(encoded, decode(f.Free)) {
		t.Fatal("native FreeRide mismatch", e)
	}
	client, e := ParseClient(decode(f.Client))
	if e != nil {
		t.Fatal(e)
	}
	if client.ConnectionID != 0x12345678 || client.Port != 8122 || client.Reference != 0xc0000203 || len(client.Extension) != 44 {
		t.Fatalf("native client %+v", client)
	}
	encoded, e = client.MarshalBinary()
	if e != nil || !bytes.Equal(encoded, decode(f.Client)) {
		t.Fatal("native SessionClient mismatch", e)
	}
	for _, x := range []struct{ name, wire string }{{"restriction", f.Restriction}, {"update", f.Update}, {"create", f.Create}} {
		wire := decode(x.wire)
		frame, e := ednet.ParseFrame(wire)
		if e != nil || frame.Kind != ednet.CallUnreliable {
			t.Fatal(x.name, e)
		}
		switch x.name {
		case "restriction":
			q, e := ParseRestrictionUpdate(frame.Action)
			if e != nil {
				t.Fatal(e)
			}
			if q.Capacity != 8 || q.Restrictions != (Restrictions{Opaque70: 0x1234, Signed: -2, ID: 0x5678, Category: 9}) {
				t.Fatalf("native restrictions %+v", q)
			}
			encoded, e = q.MarshalBinary()
			if e != nil {
				t.Fatal(e)
			}
		case "update":
			encoded, e = MarshalUpdate(free)
		case "create":
			q, err := ParseCreateRequest(frame.Action)
			if err != nil {
				t.Fatal(err)
			}
			if q.Mode != 0x12345678 || q.Session != free || q.Client.ConnectionID != client.ConnectionID {
				t.Fatalf("native create %+v", q)
			}
			encoded, e = q.MarshalBinary()
		}
		if e != nil || !bytes.Equal(encoded, frame.Action) {
			t.Fatalf("%s native action mismatch %v", x.name, e)
		}
	}
}
func TestSpatialSplitRejoinAndHostRegroup(t *testing.T) {
	s := testStore(t, StoreOptions{MaxSessions: 2})
	_, host, _ := s.Create(owner(1), request(1, 0))
	host = publish(t, s, owner(1), host)
	_, _, e := s.Create(owner(2), request(2, 0))
	if e != nil {
		t.Fatal(e)
	}
	// A changed disjoint request is a real selection, without requiring a blacklist.
	result, split, e := s.Create(owner(2), request(2, 20000))
	if e != nil || result != 1 || split.ID == host.ID {
		t.Fatal("joined owner stuck in old group", result, e)
	}
	if _, ok := s.Membership(2, 1); ok {
		t.Fatal("split retained old membership")
	}
	// The former joiner now owns its group and may merge back into a published host.
	result, joined, e := s.Create(owner(2), request(2, 0))
	if e != nil || result != 2 || joined.ID != host.ID {
		t.Fatal("host could not regroup", result, e)
	}
	if _, ok := s.Get(split.ID); ok {
		t.Fatal("merged host retained abandoned record")
	}
	if _, ok := s.Membership(2, 1); !ok {
		t.Fatal("rejoin did not replace membership")
	}
	// Blacklisting an owned record requests a new selection, never foreign mutation.
	q := request(1, 0)
	q.Blacklist = []Exclusion{{host.ID, 10}}
	result, newHost, e := s.Create(owner(1), q)
	if e != nil || result != 1 || newHost.ID == host.ID {
		t.Fatal("owner blacklist regroup failed", result, e)
	}
	if s.OwnerConnectionID(owner(2)) != 0 {
		t.Fatal("abandoned host retained other transport membership")
	}
	q = request(3, 0)
	q.Session.ID = newHost.ID
	if _, _, e = s.Create(owner(3), q); !errors.Is(e, ErrOwnership) {
		t.Fatal("adopted foreign record ID", e)
	}
}
func TestExpiredReservationDoesNotAuthorizeIntroduction(t *testing.T) {
	now := time.Unix(100, 0)
	s := testStore(t, StoreOptions{Now: func() time.Time { return now }})
	_, host, _ := s.Create(owner(1), request(1, 0))
	publish(t, s, owner(1), host)
	s.Create(owner(2), request(2, 0))
	if _, ok := s.Membership(2, 1); !ok {
		t.Fatal("fresh reservation missing")
	}
	now = now.Add(5 * time.Second)
	if _, ok := s.Membership(2, 1); ok {
		t.Fatal("expired reservation authorized introduction")
	}
	if s.OwnerConnectionID(owner(2)) != 2 {
		t.Fatal("expired reservation released identity ownership")
	}
	s.Create(owner(2), request(2, 0))
	if _, ok := s.Membership(2, 1); !ok {
		t.Fatal("fresh explicit selection did not renew reservation")
	}
}
func TestRestrictionsFilteredSearchAndWireBounds(t *testing.T) {
	s := testStore(t, StoreOptions{})
	_, host, _ := s.Create(owner(1), request(1, 0))
	host = publish(t, s, owner(1), host)
	q := RestrictionUpdate{SessionID: host.ID, Capacity: 4, Restrictions: Restrictions{Opaque70: 7, Signed: -2, ID: 123, Category: 9}, Extra: []byte{1, 2, 3}}
	if e := s.SetRestrictions(owner(2), q); !errors.Is(e, ErrOwnership) {
		t.Fatal("foreign restrictions", e)
	}
	if e := s.SetRestrictions(owner(1), q); e != nil {
		t.Fatal(e)
	}
	query := FilterQuery{Name: host.Name, Limit: 99, Signed: -1}
	if len(s.Filter(owner(2), query)) != 1 || len(s.Filter(owner(1), query)) != 0 {
		t.Fatal("filtered owner exclusion/wildcard")
	}
	query.Signed = -2
	query.ID = 123
	query.Category = 9
	got := s.Filter(owner(2), query)
	if len(got) != 1 || got[0].Descriptor.Capacity != 4 || got[0].Restrictions != q.Restrictions {
		t.Fatal("restriction state missing")
	}
	query.Category = 8
	if len(s.Filter(owner(2), query)) != 0 {
		t.Fatal("category ignored")
	}
	wire, _ := q.MarshalBinary()
	for n := 0; n < len(wire); n++ {
		if n == len(wire)-len(q.Extra)-2 {
			continue
		}
		if _, e := ParseRestrictionUpdate(wire[:n]); e == nil {
			t.Fatalf("accepted truncated restriction %d", n)
		}
	}
	query.Category = 9
	encoded, _ := query.MarshalBinary()
	decoded, e := ParseFilteredSearch(encoded)
	if e != nil || decoded != query {
		t.Fatal("filter roundtrip", e)
	}
	for n := 0; n < len(encoded); n++ {
		if n == len(encoded)-4 {
			continue
		}
		if _, e := ParseFilteredSearch(encoded[:n]); e == nil {
			t.Fatalf("accepted truncated filter %d", n)
		}
	}
	svc, _ := New(Options{Port: 9002, Store: s})
	frames := call(t, svc, ednet.Call, 55, info(2), encoded)
	if len(frames) != 2 {
		t.Fatal("missing filtered result/completion")
	}
	business, _ := ednet.ParseFrame(frames[0])
	completion, _ := ednet.ParseFrame(frames[1])
	if business.Kind != ednet.CallUnreliable || completion.Kind != ednet.Reply || completion.RequestID != 55 {
		t.Fatal("native synchronous business-before-completion ordering")
	}
	if _, e = svc.HandleFrames(context.Background(), info(2), owner(2).Remote, frames[0]); !errors.Is(e, ErrUnsupported) {
		t.Fatal("accepted reply action as request", e)
	}
}
