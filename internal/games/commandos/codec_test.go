package commandos

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func nativeGathering(t *testing.T) (gathering, []byte) {
	t.Helper()
	b, err := os.ReadFile("testdata/gathering-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Body  string `json:"field_body_hex"`
		Calls int    `json:"primitive_calls_per_codec"`
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Calls != 559 {
		t.Fatal("native trace is incomplete")
	}
	body, err := hex.DecodeString(fixture.Body)
	if err != nil {
		t.Fatal(err)
	}
	var w quazal.Writer
	var data quazal.Writer
	data.Buffer(body)
	wrapped, err := data.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	w.Any(quazal.AnyData{Type: gatheringType, Payload: wrapped})
	wire, err := w.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	g, err := decodeGathering(wire)
	if err != nil {
		t.Fatal(err)
	}
	return g, wire
}

// Rebuild the stock host request's observed sizes and initial scalar values
// with synthetic names/details. No user capture or player data is embedded.
func TestRetailSizedInitialGatheringRegistration(t *testing.T) {
	fields := make([]byte, 596)
	binary.LittleEndian.PutUint32(fields[16:], 7) // participation policy
	binary.LittleEndian.PutUint32(fields[24:], 16)
	binary.LittleEndian.PutUint16(fields[32:], 10)
	copy(fields[34:], "WADX2Test\x00")
	copy(fields[48:], "Synthetic")
	copy(fields[80:], "synthetic details")
	binary.LittleEndian.PutUint32(fields[592:], 2)
	data := binary.LittleEndian.AppendUint32(nil, uint32(len(fields)))
	data = append(data, fields...)
	buildAny := func(body []byte) []byte {
		b := binary.LittleEndian.AppendUint16(nil, uint16(len(gatheringType)+1))
		b = append(b, gatheringType...)
		b = append(b, 0)
		b = binary.LittleEndian.AppendUint32(b, uint32(len(body)))
		return append(b, body...)
	}
	wire := buildAny(data)
	if len(data) != 600 || len(wire) != 631 {
		t.Fatal("retail request dimensions changed")
	}
	g, err := decodeGathering(wire)
	if err != nil || g.Description != "WADX2Test" || g.Policy != 7 || g.Flags != 16 || g.MaxParticipants != 0 || g.PlayerCount != 0 {
		t.Fatalf("initial host record: %+v %v", g, err)
	}
	encoded, err := g.encode()
	if err != nil || !bytes.Equal(encoded, wire) {
		t.Fatal("Data wrapper did not round-trip", err)
	}
	if _, err = decodeGathering(buildAny(fields)); err == nil {
		t.Fatal("accepted missing inner Data length")
	}
	for _, length := range []uint32{0, 595, 597, 0xffffffff} {
		bad := bytes.Clone(data)
		binary.LittleEndian.PutUint32(bad, length)
		if _, err = decodeGathering(buildAny(bad)); err == nil {
			t.Fatalf("accepted inner length %d", length)
		}
	}
	s := newSessions()
	peer := testPeer(1)
	s.registered(peer, 1000, testURL(1000))
	r := quazal.NewReader(call(t, s, peer, 1000, 21, 1, wire))
	if r.U32() == 0 || !r.Done() || s.count() != 1 {
		t.Fatal("retail-sized host registration failed")
	}
}
func TestGatheringMatchesSyntheticNativeTrace(t *testing.T) {
	g, wire := nativeGathering(t)
	if g.ID != 0x01020304 || g.OwnerPID != 0x11121314 || g.HostPID != 0x21222324 || g.MinParticipants != 1 || g.MaxParticipants != 8 || g.Policy != 7 || g.Flags != 16 || g.State != 2 || g.Description != "Synthetic room" || g.Mode != 2 || g.PlayerCount != 3 || !g.FlagA || g.FlagB || g.Discriminator != 0x10203 {
		t.Fatalf("native field mapping: %+v", g)
	}
	if string(bytes.TrimRight(g.Name[:], "\x00")) != "arena-test" || string(bytes.TrimRight(g.Details[:], "\x00")) != "mode=2;map=ST03_MP" {
		t.Fatal("fixed arrays shifted")
	}
	encoded, err := g.encode()
	if err != nil || !bytes.Equal(encoded, wire) {
		t.Fatalf("native body differs: %v", err)
	}
	for n := 0; n < len(wire); n++ {
		if _, err = decodeGathering(wire[:n]); err == nil {
			t.Fatalf("accepted truncated record %d", n)
		}
	}
	if _, err = decodeGathering(append(bytes.Clone(wire), 0)); err == nil {
		t.Fatal("accepted trailing data")
	}
	for _, mutate := range []func(*gathering){func(g *gathering) { g.PlayerCount = 9 }, func(g *gathering) { g.MaxParticipants = 9 }, func(g *gathering) { g.Mode = 255 }} {
		bad := g
		mutate(&bad)
		encoded, _ := bad.encode()
		if _, err = decodeGathering(encoded); err == nil {
			t.Fatal("accepted invalid title fields")
		}
	}
}
func TestRetailDescriptionFilterGrammar(t *testing.T) {
	for _, test := range []struct {
		pattern, description string
		want                 bool
	}{
		{"^W....", "WADF2Room", true}, {"^WADF2", "WADF2Room", true}, {"^WADF2", "WATF2Room", false}, {"^LPTX.", "LPTXXRoom", true}, {"^W....", "LADF2Room", false}, {"^W....", "W", false},
	} {
		match, err := descriptionFilter(test.pattern)
		if err != nil || match(test.description) != test.want {
			t.Fatalf("filter %+v: %v", test, err)
		}
	}
	for _, bad := range []string{"", ".*", "^W.*", "^W....$", "^WP[DT]F2", "^WAPF2", "^WADS2", "^WADF3"} {
		if _, err := descriptionFilter(bad); err == nil {
			t.Fatalf("accepted arbitrary regex %q", bad)
		}
	}
}
