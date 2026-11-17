package sessionmaster

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/presence"
	"os"
	"testing"
)

func TestDensityUsesNativeCatalogAndPublishedSessionCounts(t *testing.T) {
	if stockAggregateIndex != presence.StockActivityCount || stockDensityCells != presence.SessionDensityCells {
		t.Fatal("catalog diverged from native evidence")
	}
	s := testStore(t, StoreOptions{})
	svc, _ := New(Options{Port: 9002, Store: s})
	frames := call(t, svc, ednet.CallUnreliable, 0, info(1), MarshalID(SessionDensity, 2))
	a := action(t, frames[0])
	if len(a) != 10 || binary.BigEndian.Uint16(a) != SessionDensityReply || binary.BigEndian.Uint32(a[2:]) != 449 || binary.BigEndian.Uint32(a[6:]) != 0 {
		t.Fatal("zero density must omit buffer")
	}
	_, host, _ := s.Create(owner(1), request(1, 0))
	host = publish(t, s, owner(1), host)
	s.Create(owner(2), request(2, 0))     // A joining driver does not create another session.
	s.Create(owner(3), request(3, 20000)) // An unpublished session is not discoverable.
	frames = call(t, svc, ednet.CallUnreliable, 0, info(1), MarshalID(SessionDensity, 2))
	a = action(t, frames[0])
	if len(a) != 10 || binary.BigEndian.Uint32(a[6:]) != 0 {
		t.Fatal("session aggregation or conditional layout")
	}
	for _, v := range a[10:] {
		if v != 0 {
			t.Fatal("advertised unimplemented activity hosting")
		}
	}
	s.DropOwner(owner(1))
	frames = call(t, svc, ednet.CallUnreliable, 0, info(1), MarshalID(SessionDensity, 2))
	if len(action(t, frames[0])) != 10 {
		t.Fatal("disconnected host remained in density")
	}
}

func TestActivityDensityExactNamesAndImmutableOptionalCatalog(t *testing.T) {
	store := testStore(t, StoreOptions{})
	catalog := map[string]uint16{"RACE001_PS2": 7}
	svc, e := New(Options{Port: 9002, Store: store, ActivityCatalog: catalog})
	if e != nil {
		t.Fatal(e)
	}
	catalog["RACE001_PS2"] = 8
	store.CreateGeneric(owner(1), genericRequest(1, "DriveIn_PS2"))
	store.CreateGeneric(owner(2), genericRequest(2, "RACE001_PS2"))
	store.CreateGeneric(owner(3), genericRequest(3, "Unmapped_PS2"))
	a := svc.SessionDensity()
	if len(a) != 461 || binary.BigEndian.Uint32(a[6:]) != 2 || a[12+7] != 1 || a[12+8] != 0 || a[len(a)-1] != 1 {
		t.Fatal("activity classification/count or caller-mutated catalog")
	}
	for _, m := range []map[string]uint16{{"bad": 448}, {"": 1}, {"DriveIn_PS2": 7}} {
		if _, e := New(Options{Port: 9002, ActivityCatalog: m}); e == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
}

func TestActivityDensityMatchesExecutedNativeConsumerCases(t *testing.T) {
	raw, e := os.ReadFile("../presence/testdata/native-density-consumer.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Entries []struct {
			Second uint32 `json:"secondScalar"`
			Action string `json:"actionHex"`
		} `json:"entries"`
	}
	if e = json.Unmarshal(raw, &f); e != nil {
		t.Fatal(e)
	}
	for _, x := range f.Entries {
		store := testStore(t, StoreOptions{})
		svc, _ := New(Options{Port: 9002, Store: store})
		for i := uint32(1); i <= x.Second; i++ {
			if _, e := store.CreateGeneric(owner(uint64(i)), genericRequest(i, "DriveIn_PS2")); e != nil {
				t.Fatal(e)
			}
		}
		expected, e := hex.DecodeString(x.Action)
		if e != nil {
			t.Fatal(e)
		}
		if actual := svc.SessionDensity(); !bytes.Equal(actual, expected) {
			t.Fatalf("native consumer case%d mismatch", x.Second)
		}
	}
}
