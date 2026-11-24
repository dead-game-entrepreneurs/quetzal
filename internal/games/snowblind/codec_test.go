package snowblind

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func TestGatheringMatchesNativeWriterAndReader(t *testing.T) {
	fixture, err := os.ReadFile("testdata/gathering-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Body    string `json:"field_body_hex"`
		Matches bool   `json:"reader_matches"`
	}
	if err = json.Unmarshal(fixture, &native); err != nil {
		t.Fatal(err)
	}
	body, err := hex.DecodeString(native.Body)
	if err != nil || !native.Matches {
		t.Fatal("native fixture", err)
	}
	g := gathering{Kind: "BO3Match", Gathering: quazal.Gathering{ID: 10000, OwnerPID: 1000, HostPID: 1000, MinParticipants: 1, MaxParticipants: 8, Policy: 7, Flags: 16, State: 2, Description: "Synthetic room"}, Platform: 1, Mode: 2, Mods: 0x3182, Players: 3, Map: 300}
	args, err := g.encode()
	if err != nil {
		t.Fatal(err)
	}
	r := quazal.NewReader(args)
	any := r.Any(2048)
	if !r.Done() || any.Type != "BO3Match" {
		t.Fatal("Any envelope")
	}
	d := quazal.NewReader(any.Payload)
	encoded := d.Buffer(2048)
	if !d.Done() || !bytes.Equal(body, encoded) {
		t.Fatal("native field widths/order differ")
	}
	decoded, err := decodeGathering(args)
	if err != nil || decoded != g {
		t.Fatal("native record decode", err)
	}
}
