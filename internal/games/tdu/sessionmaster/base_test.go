package sessionmaster

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
)

func genericRequest(cid uint32, name string) GenericRequest {
	return GenericRequest{Client: Client{Name: "Driver", ConnectionID: cid}, Session: BaseSession{Identity: uint64(cid), Connection: [4]uint32{8889, 0xc0000202, 8889, cid}, Game: "TestDriveV2.30", Name: name, Occupancy: 1, Capacity: 8, Fields: [5]uint32{0, 7, 0, 123, 9}, Extra: []byte{0x12, 0x34, 1, 2}}}
}
func TestNativeBaseAndSourceEndpointFixture(t *testing.T) {
	raw, e := os.ReadFile("testdata/native-senders.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Base   string `json:"base_hex"`
		Source string `json:"source_endpoint_hex"`
	}
	if e = json.Unmarshal(raw, &f); e != nil {
		t.Fatal(e)
	}
	expected, e := hex.DecodeString(f.Base)
	if e != nil {
		t.Fatal(e)
	}
	d, e := ParseBaseSession(expected)
	if e != nil {
		t.Fatal(e)
	}
	if d.Identity != 0x0102030405060708 || d.Opaque != 0x1020304050607080 || d.Fields != ([5]uint32{0x11223344, 0x22334455, 0xfffffffe, 0x33445566, 0x44556677}) {
		t.Fatalf("native base %+v", d)
	}
	encoded, e := d.MarshalBinary()
	if e != nil || !bytes.Equal(encoded, expected) {
		t.Fatal("native base codec mismatch", e)
	}
	for n := 0; n < len(expected); n++ {
		if _, e := ParseBaseSession(expected[:n]); e == nil {
			t.Fatalf("base truncation %d", n)
		}
	}
	source, e := hex.DecodeString(f.Source)
	if e != nil {
		t.Fatal(e)
	}
	r := Snapshot{ID: d.ID, Owner: Owner{Remote: owner(1).Remote, Generation: 1}, Descriptor: d.freeRide()}
	r.Owner.Remote = netip.AddrPortFrom(r.Owner.Remote.Addr(), 45678)
	reply, e := marshalGenericJoined(&r)
	if e != nil {
		t.Fatal(e)
	}
	_, _, _, objects, e := ParseGenericResult(reply)
	if e != nil || len(objects) != 2 || !bytes.Equal(objects[1], source) {
		t.Fatalf("native edNetClientV2 source mismatch %v", e)
	}
}
func TestGenericCreateUpdateBrowserJoinAndCruiseReplacement(t *testing.T) {
	s := testStore(t, StoreOptions{MaxSessions: 2, MaxOwners: 3})
	svc, _ := New(Options{Port: 9002, Store: s})
	_, cruise, _ := s.Create(owner(1), request(1, 0))
	publish(t, s, owner(1), cruise)
	s.Create(owner(2), request(2, 0))
	q := genericRequest(1, "DriveIn_PS2")
	wire, e := q.MarshalBinary(GenericCreate)
	if e != nil {
		t.Fatal(e)
	}
	frames := call(t, svc, ednet.Call, 10, info(1), wire)
	if len(frames) != 1 {
		t.Fatal("generic create is a nested reliable result")
	}
	frame, _ := ednet.ParseFrame(frames[0])
	code, result, id, objects, e := ParseGenericResult(frame.Action)
	if e != nil || frame.Kind != ednet.Reply || frame.RequestID != 10 || code != GenericCreateReply || result != 1 || id == 0 {
		t.Fatalf("generic create %+v %v", frame, e)
	}
	base, e := ParseBaseSession(objects[0])
	if e != nil || base.ID != id || base.Reference != 0xc0000201 || !bytes.Equal(base.Extra, q.Session.Extra) {
		t.Fatal("generic create fields", e)
	}
	if _, ok := s.Get(cruise.ID); ok || s.OwnerConnectionID(owner(2)) != 0 {
		t.Fatal("cruise replacement retained abandoned record or claims")
	}
	retry := call(t, svc, ednet.Call, 10, info(1), wire)
	if !bytes.Equal(retry[0], frames[0]) || len(s.Snapshot()) != 1 {
		t.Fatal("reliable create retry duplicated session")
	}
	// Full-width initial fields survive until an actual restriction update.
	if got := s.Snapshot()[0].BaseSession(); got.Fields != base.Fields {
		t.Fatal("base field projection lost values")
	}
	base.Occupancy = 1
	base.Fields[1] = 456
	base.Extra = []byte{0x56, 0x78}
	update, _ := MarshalGenericUpdate(base)
	frames = call(t, svc, ednet.CallUnreliable, 0, info(1), update)
	if action(t, frames[0])[6] != 0 {
		t.Fatal("owner generic update failed")
	}
	query := FilterQuery{Name: base.Name, Limit: 3, ID: 123, Signed: 0, Category: 9}
	filter, _ := query.MarshalBinary()
	frames = call(t, svc, ednet.Call, 11, info(2), filter)
	if len(frames) != 2 {
		t.Fatal("race absent from filtered browser")
	}
	r := reader{b: action(t, frames[0])}
	if r.u16() != SearchReply || r.u32() != id {
		t.Fatal("search result ID")
	}
	listed, e := ParseBaseSession(r.blob(512))
	if e != nil || listed.Fields[1] != 456 || !bytes.Equal(listed.Extra, base.Extra) {
		t.Fatal("browser lost base fields", e)
	}
	// The target's advertised CID belongs to the host, not the joining caller.
	join := GenericRequest{Client: Client{ConnectionID: 2, Name: "Joiner"}, Session: listed}
	joinWire, _ := join.MarshalBinary(GenericJoin)
	frames = call(t, svc, ednet.Call, 12, info(2), joinWire)
	joinedFrame, _ := ednet.ParseFrame(frames[0])
	code, result, joinedID, _, e := ParseGenericResult(joinedFrame.Action)
	if e != nil || code != GenericJoinReply || result != 1 || joinedID != id {
		t.Fatal("generic join", e)
	}
	if _, ok := s.Membership(2, 1); !ok {
		t.Fatal("generic join lacks scoped membership")
	}
	retry = call(t, svc, ednet.Call, 12, info(2), joinWire)
	if !bytes.Equal(retry[0], frames[0]) {
		t.Fatal("join retry consumed another reservation")
	}
	s.DropOwner(owner(1))
	if len(s.Snapshot()) != 0 || s.OwnerConnectionID(owner(2)) != 0 {
		t.Fatal("generic disconnect cleanup")
	}
}
func TestGenericOwnershipIsolationLimitsAndExpiry(t *testing.T) {
	now := time.Unix(100, 0)
	s := testStore(t, StoreOptions{MaxSessions: 2, MaxOwners: 4, Now: func() time.Time { return now }})
	q := genericRequest(11, "A_PS2")
	a, e := s.CreateGeneric(owner(1), q)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateGeneric(owner(2), q); !errors.Is(e, ErrIdentity) {
		t.Fatal("duplicate claimed CID", e)
	}
	spoof := q
	spoof.Client.ConnectionID = 22
	if _, e = s.CreateGeneric(owner(2), spoof); !errors.Is(e, ErrIdentity) {
		t.Fatal("client/advertised CID mismatch", e)
	}
	b, e := s.CreateGeneric(owner(3), genericRequest(33, "B_PS2"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateGeneric(owner(4), genericRequest(44, "C_PS2")); !errors.Is(e, ErrCapacity) {
		t.Fatal("shared session bound", e)
	}
	join := genericRequest(22, "Unknown_PS2")
	if got, e := s.JoinGeneric(owner(2), join); e != nil || got != nil {
		t.Fatal("joined unrelated name", e)
	}
	join.Session.Name = a.Descriptor.Name
	join.Session.ID = b.ID
	if got, e := s.JoinGeneric(owner(2), join); e != nil || got != nil {
		t.Fatal("ignored explicitly selected session ID", e)
	}
	join.Session.ID = a.ID
	join.Session.Fields[3] = 999
	if got, e := s.JoinGeneric(owner(2), join); e != nil || got != nil {
		t.Fatal("ignored race classification", e)
	}
	join.Session.Fields[3] = 123
	join.Blacklist = []Exclusion{{a.ID, 5}}
	if got, e := s.JoinGeneric(owner(2), join); e != nil || got != nil {
		t.Fatal("ignored join blacklist", e)
	}
	join.Blacklist = nil
	if got, e := s.JoinGeneric(owner(2), join); e != nil || got == nil || got.ID != a.ID {
		t.Fatal("bounded join", e)
	}
	d := a.BaseSession()
	d.Name = "Hijacked_PS2"
	if event, e := s.UpdateGeneric(owner(3), d); e != nil || event != 0xff {
		t.Fatal("foreign generic update", e)
	}
	d = a.BaseSession()
	d.Connection[3] = 33
	if event, e := s.UpdateGeneric(owner(1), d); e != nil || event != 0xff {
		t.Fatal("spoofed generic update CID", e)
	}
	now = now.Add(16 * time.Second)
	if len(s.Snapshot()) != 0 || s.OwnerConnectionID(owner(1)) != 0 || s.OwnerConnectionID(owner(2)) != 0 {
		t.Fatal("shared expiry cleanup")
	}
}
func TestBaseExtraObjectNativeBound(t *testing.T) {
	d := genericRequest(1, "A_PS2").Session
	d.Extra = make([]byte, 128)
	b, e := d.MarshalBinary()
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := ParseBaseSession(b)
	if e != nil || len(parsed.Extra) != 128 {
		t.Fatal("native128-byte extra", e)
	}
	d.Extra = make([]byte, 129)
	if _, e = d.MarshalBinary(); e == nil {
		t.Fatal("oversized native extra accepted")
	}
}
