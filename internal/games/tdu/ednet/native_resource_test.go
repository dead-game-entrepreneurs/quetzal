package ednet

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"testing"
)

// Requests were emitted by retail instruction execution; replies below were
// consumed by the retail resource job, including real counted-buffer readers.
func TestNativeResourceWireFixtures(t *testing.T) {
	var fixture struct {
		Requests []struct{ Name, ActionHex string }
		Replies  []struct {
			Name, ActionHex                                 string
			State, Error, Offset, Consumed, Stage, ResumeAt uint32
			CopiedHex                                       string
		}
	}
	b, err := os.ReadFile("testdata/native-resources.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Requests) != 4 || len(fixture.Replies) != 8 {
		t.Fatal("incomplete native fixture")
	}
	replies := make(map[string][]byte)
	for _, r := range fixture.Replies {
		replies[r.Name] = mustHex(t, r.ActionHex)
	}
	content := mustHex(t, "00aaff7e615800aaff7e61")
	s, err := New(Options{PublicIP: netip.MustParseAddr("192.0.2.7"), DirectoryPort: 9000, ResourcePort: 9001, Resources: map[string][]byte{"netconfig.xml": content}})
	if err != nil {
		t.Fatal(err)
	}
	check := func(request, want []byte) {
		t.Helper()
		got, err := s.ResourceAction(request)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("request %x: got %x want %x: %v", request, got, want, err)
		}
	}
	check(mustHex(t, fixture.Requests[0].ActionHex), replies["open-success"])
	check(mustHex(t, fixture.Requests[2].ActionHex), replies["get-complete"])
	check(mustHex(t, fixture.Requests[3].ActionHex), replies["get-unknown"])
	if _, err = s.ResourceAction(mustHex(t, fixture.Requests[1].ActionHex)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("native upload accepted: %v", err)
	}
	check(open("unknown.xml", 0), replies["open-error"])
	check(get(1, 6), replies["get-final"])
	// These outcomes exercise native branch decisions, not a second Go decoder.
	for _, r := range fixture.Replies {
		switch r.Name {
		case "open-success":
			if r.State != 1 || r.Stage != 1 || r.Consumed != 19 {
				t.Fatalf("native open outcome %+v", r)
			}
		case "get-success":
			if r.Offset != 5 || r.Stage != 1 || r.CopiedHex != "00aaff7e61" {
				t.Fatalf("native partial outcome %+v", r)
			}
		case "get-wait":
			if r.Stage != 3 || r.ResumeAt != 9660 || r.Consumed != 13 {
				t.Fatalf("native wait outcome %+v", r)
			}
		case "get-final", "get-complete":
			if r.State != 3 || r.Offset != 11 || r.Error != 0 {
				t.Fatalf("native completion %+v", r)
			}
		case "open-error", "get-error", "get-unknown":
			if r.State != 3 || r.Error != 1 {
				t.Fatalf("native failure %+v", r)
			}
		}
	}
}

func TestNativeConfigurationResourceDownloads(t *testing.T) {
	data, err := os.ReadFile("testdata/native-resources.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ConfigurationRequests []struct{ Resource, ActionHex string }
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.ConfigurationRequests) != 3 {
		t.Fatal("missing native configuration requests")
	}
	resources := map[string][]byte{
		"netconfig.xml": DefaultNetworkConfig(),
		"p2pkey":        bytes.Repeat([]byte{0xab}, 16),
		"prize.xml":     NoAwardsConfig(),
	}
	service, err := New(Options{PublicIP: netip.MustParseAddr("192.0.2.7"), DirectoryPort: 9000, ResourcePort: 9001, Resources: resources})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range fixture.ConfigurationRequests {
		t.Run(row.Resource, func(t *testing.T) {
			want, ok := resources[row.Resource]
			if !ok {
				t.Fatal("unexpected native resource")
			}
			opened, err := service.ResourceAction(mustHex(t, row.ActionHex))
			if err != nil {
				t.Fatal(err)
			}
			if len(opened) != 19 || opened[2] != 0 || binary.BigEndian.Uint32(opened[7:]) != uint32(len(want)) {
				t.Fatalf("open reply %x", opened)
			}
			id := binary.BigEndian.Uint32(opened[3:])
			var content []byte
			for len(content) < len(want) {
				chunk, err := service.ResourceAction(get(id, uint32(len(content))))
				if err != nil {
					t.Fatal(err)
				}
				if len(chunk) <= 15 || chunk[2] != 0 {
					t.Fatalf("get reply %x", chunk)
				}
				content = append(content, chunk[15:]...)
			}
			if !bytes.Equal(content, want) {
				t.Fatal("configuration resource changed during download")
			}
		})
	}
}
