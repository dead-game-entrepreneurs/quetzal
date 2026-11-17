package presence

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"math"
	"os"
	"reflect"
	"strconv"
	"testing"
)

func TestWireGolden(t *testing.T) {
	v := Update{UserID: 1, TeamID: 2, Name: "A", ClientType: 2, Position: Position{1, 3, 2}, Level: 0, CarType: 4, GameID: 5, GameType: 6, CarMask: 0x0102030405060708, ExtraIdentity: 7}
	want, _ := hex.DecodeString("89ee000000010000000200014100023f800000400000000000000000000004000000050000000601020304050607080000000740400000")
	got, err := MarshalUpdate(v)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("update %x %v", got, err)
	}
	parsed, err := ParseUpdate(want)
	if err != nil || parsed != v {
		t.Fatalf("parsed %+v %v", parsed, err)
	}
	q := TrackRequest{UserID: 1, ClientType: 2, GameID: 5, Limit: 16, Position: Position{1, 3, 2}, CarMask: 0x0102030405060708, NATType: 3, Friends: []uint32{9}}
	want, _ = hex.DecodeString("42bb000000010000000200000005000000103f800000404000004000000001020304050607080000000300000009")
	got, err = MarshalTrackRequest(q)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("track %x %v", got, err)
	}
	query, err := ParseTrackRequest(want)
	if err != nil || !reflect.DeepEqual(query, q) {
		t.Fatalf("parsed %+v %v", query, err)
	}
	row := Nearby{Position: v.Position, Name: v.Name, GameID: v.GameID, GameType: int32(v.GameType), ClientType: v.ClientType, Level: byte(v.Level), CarMask: v.CarMask, CarType: byte(v.CarType), NATType: 3}
	want, _ = hex.DecodeString("c4d5013f800000404000004000000000014100000005000000060002000102030405060708040300000001")
	got, err = MarshalNearby([]Nearby{row}, 1)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("nearby %x %v", got, err)
	}
}
func TestMalformedActions(t *testing.T) {
	b, _ := MarshalUpdate(Update{Name: "valid", ClientType: 2})
	for n := range len(b) {
		if _, err := ParseUpdate(b[:n]); err == nil {
			t.Fatalf("accepted update prefix %d", n)
		}
	}
	bad := append(append([]byte{}, b...), 0)
	if _, err := ParseUpdate(bad); err == nil {
		t.Fatal("trailing update byte")
	}
	bad = append([]byte{}, b...)
	binary.BigEndian.PutUint16(bad[10:], 65535)
	if _, err := ParseUpdate(bad); err == nil {
		t.Fatal("oversize name")
	}
	if _, err := MarshalUpdate(Update{Position: Position{X: float32(math.Inf(1))}}); err == nil {
		t.Fatal("nonfinite")
	}
	q, _ := MarshalTrackRequest(TrackRequest{ClientType: 2, Limit: 16})
	for n := range len(q) {
		if _, err := ParseTrackRequest(q[:n]); err == nil {
			t.Fatalf("accepted track prefix %d", n)
		}
	}
	if _, err := ParseTrackRequest(append(q, 0)); err == nil {
		t.Fatal("partial friend")
	}
	binary.BigEndian.PutUint32(q[14:], 17)
	if _, err := ParseTrackRequest(q); err == nil {
		t.Fatal("oversize result limit")
	}
	if _, _, err := ParseMapRequest([]byte{0x55, 0xae, 0}); err == nil {
		t.Fatal("truncated map")
	}
}

func TestRetailMapFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/native-map.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		ActionHex    string
		Total        uint32
		NonzeroCells map[string]byte
	}
	if err = json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	var grid [Cells]byte
	for s, v := range f.NonzeroCells {
		i, e := strconv.Atoi(s)
		if e != nil || i < 0 || i >= Cells {
			t.Fatal(s)
		}
		grid[i] = v
	}
	want, err := hex.DecodeString(f.ActionHex)
	if err != nil {
		t.Fatal(err)
	}
	if got := MarshalMap(grid, f.Total); !bytes.Equal(got, want) {
		t.Fatalf("retail map fixture mismatch %x", got[:4])
	}
}

// These datagrams were emitted by the actual retail sender entrypoints, not a
// second implementation of the Go layout. In particular they catch u32 fields
// whose decompiler argument types appear narrower than the serializer writes.
func TestRetailSenderFixtures(t *testing.T) {
	b, err := os.ReadFile("testdata/native-senders.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Entries []struct{ Name, FrameHex string }
	}
	if err = json.Unmarshal(b, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures.Entries {
		t.Run(f.Name, func(t *testing.T) {
			wire, e := hex.DecodeString(f.FrameHex)
			if e != nil {
				t.Fatal(e)
			}
			frame, e := ednet.ParseFrame(wire)
			if e != nil || frame.Kind != ednet.CallUnreliable {
				t.Fatalf("frame %v %x", e, wire)
			}
			var got []byte
			switch f.Name {
			case "track", "track-friends":
				v, e := ParseTrackRequest(frame.Action)
				if e != nil {
					t.Fatal(e)
				}
				if v.UserID != 1 || v.ClientType != 2 || v.GameID != 5 || v.Limit != 16 || v.Position != (Position{1, 3, 2}) || v.CarMask != 0x12345678 || v.NATType != 3 {
					t.Fatalf("native track %+v", v)
				}
				if f.Name == "track-friends" && !reflect.DeepEqual(v.Friends, []uint32{9, 10}) {
					t.Fatal(v.Friends)
				}
				got, e = MarshalTrackRequest(v)
				if e != nil {
					t.Fatal(e)
				}
			case "map":
				level, ct, e := ParseMapRequest(frame.Action)
				if e != nil || level != 0 || ct != 2 {
					t.Fatalf("map %x %v", frame.Action, e)
				}
				got = []byte{0x55, 0xae, 0, 2}
			case "session-density":
				if !bytes.Equal(frame.Action, []byte{0x51, 0x0b, 0, 0, 0, 2}) {
					t.Fatalf("sessiondensity %x", frame.Action)
				}
				got = frame.Action
			case "update":
				v, e := ParseUpdate(frame.Action)
				if e != nil {
					t.Fatal(e)
				}
				want := Update{UserID: 1, TeamID: 2, Name: "A", ClientType: 2, Position: Position{1, 3, 2}, Level: 0x11223344, CarType: 0x55667788, GameID: 5, GameType: -6, CarMask: 0x0102030405060708, ExtraIdentity: 7}
				if v != want {
					t.Fatalf("native update %+v", v)
				}
				got, e = MarshalUpdate(v)
				if e != nil {
					t.Fatal(e)
				}
			default:
				t.Fatal("unknown fixture", f.Name)
			}
			if !bytes.Equal(got, frame.Action) {
				t.Fatalf("native mismatch %x vs %x", got, frame.Action)
			}
		})
	}
}

func TestRetailCatalogCounts(t *testing.T) {
	b, e := os.ReadFile("testdata/native-catalog.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct{ SourceSpots, ExcludedType7, BaseCount, MissionCount, CombinedCount, AggregateIndex, DensityCells int }
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	if f.SourceSpots-f.ExcludedType7 != f.BaseCount || f.BaseCount+f.MissionCount != StockActivityCount || f.CombinedCount != StockActivityCount || f.AggregateIndex != AggregateDensityIndex || f.DensityCells != SessionDensityCells {
		t.Fatalf("retail catalog constants %+v", f)
	}
}
