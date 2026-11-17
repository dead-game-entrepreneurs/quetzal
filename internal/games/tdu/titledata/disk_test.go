package titledata

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func challengeUpload(name, description string) []byte {
	return putblob(putstr(putstr(putstr(put32(nil, 7), name), description), "Ace"), []byte{0, 0xaa, 0xff})
}

func TestSDKChallengeTextSurvivesRestart(t *testing.T) {
	for _, tc := range []struct {
		name               string
		title, description []byte
	}{
		{"legacy-bytes", []byte{0xff, 0x80, 'A'}, []byte{0xc0, 0xaf, 0xfe}},
		{"max-byte-length", bytes.Repeat([]byte{0xff}, 255), bytes.Repeat([]byte{0x80}, 255)},
		{"empty-description", []byte{0x81}, []byte{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			upload := challengeUpload(string(tc.title), string(tc.description))
			id := f.rpc(t, f.ace.PID, 102, 1, upload)
			before := f.rpc(t, f.ace.PID, 102, 3, put32(nil, 7))
			store, err := Open(f.store.path)
			if err != nil {
				t.Fatal(err)
			}
			service, err := New(store, f.service.accounts)
			if err != nil {
				t.Fatal(err)
			}
			after, err := service.Handle(f.bee.PID, quazal.Request{Protocol: 102, Method: 3, Args: put32(nil, 7)})
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("listing changed across restart: %x != %x: %v", before, after, err)
			}
			r := reader{b: after}
			if r.u32() != 1 || r.u64() != 1 || r.u32() != 7 || !bytes.Equal([]byte(r.str(255)), tc.title) || !bytes.Equal([]byte(r.str(255)), tc.description) || r.str(15) != "Ace" {
				t.Fatal("SDK text bytes changed")
			}
			for range 7 {
				r.u32()
			}
			if r.u8() != 0 || !r.done() {
				t.Fatal("malformed listing")
			}
			again, err := service.Handle(f.ace.PID, quazal.Request{Protocol: 102, Method: 1, Args: upload})
			if err != nil || !bytes.Equal(id, again) || len(store.data.Files) != 1 {
				t.Fatal("restarted upload lost idempotency", err)
			}
			wire, err := os.ReadFile(store.path)
			if err != nil {
				t.Fatal(err)
			}
			var disk stateV2
			if err = json.Unmarshal(wire, &disk); err != nil {
				t.Fatal(err)
			}
			if disk.Version != diskVersion || !bytes.Equal(disk.Files[1].Stats.Name, tc.title) || !bytes.Equal(disk.Files[1].Stats.Description, tc.description) {
				t.Fatal("disk bytes differ")
			}
		})
	}
}

func TestVersionOneStateMigratesOnSuccessfulCommit(t *testing.T) {
	f := setup(t)
	f.rpc(t, f.ace.PID, 102, 1, challengeUpload("Café", "legacy UTF-8 description"))
	legacy := f.store.data
	legacy.Version = 1
	original, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.store.path, original, 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(f.store.path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.data.Version != diskVersion || reopened.data.Files[1].Stats.Name != "Café" {
		t.Fatal("legacy content changed")
	}
	unchanged, _ := os.ReadFile(f.store.path)
	if !bytes.Equal(original, unchanged) {
		t.Fatal("read-only reopen rewrote version1")
	}
	service, err := New(reopened, f.service.accounts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Handle(f.ace.PID, quazal.Request{Protocol: 102, Method: 1, Args: challengeUpload("Café", "legacy UTF-8 description")}); err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(f.store.path)
	if err != nil {
		t.Fatal(err)
	}
	var header struct{ Version int }
	if err = json.Unmarshal(committed, &header); err != nil || header.Version != diskVersion {
		t.Fatal("successful commit did not migrate", err)
	}
	again, err := Open(f.store.path)
	if err != nil || again.data.Files[1].Stats.Name != "Café" || len(again.data.Files) != 1 {
		t.Fatal("migrated state failed to reopen", err)
	}
}

func TestMalformedVersionTwoStateIsRetained(t *testing.T) {
	f := setup(t)
	f.rpc(t, f.ace.PID, 102, 1, challengeUpload("title", "description"))
	valid, err := os.ReadFile(f.store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"invalid-base64", func(d map[string]any) { fileStats(d)["name_bytes"] = "!" }},
		{"missing-name", func(d map[string]any) { delete(fileStats(d), "name_bytes") }},
		{"null-description", func(d map[string]any) { fileStats(d)["description_bytes"] = nil }},
		{"embedded-NUL", func(d map[string]any) { fileStats(d)["name_bytes"] = base64.StdEncoding.EncodeToString([]byte{'a', 0}) }},
		{"overlong-name", func(d map[string]any) {
			fileStats(d)["name_bytes"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, 256))
		}},
		{"unknown-metadata-field", func(d map[string]any) { fileStats(d)["extra"] = true }},
		{"unknown-root-field", func(d map[string]any) { d["extra"] = true }},
		{"unknown-version", func(d map[string]any) { d["version"] = 3 }},
		{"missing-files", func(d map[string]any) { delete(d, "files") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var d map[string]any
			if err := json.Unmarshal(valid, &d); err != nil {
				t.Fatal(err)
			}
			tc.change(d)
			bad, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "title.json")
			if err = os.WriteFile(path, bad, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = Open(path); err == nil {
				t.Fatal("malformed state accepted")
			}
			retained, _ := os.ReadFile(path)
			if !bytes.Equal(retained, bad) {
				t.Fatal("malformed state replaced")
			}
		})
	}
}
func fileStats(d map[string]any) map[string]any {
	return d["files"].(map[string]any)["1"].(map[string]any)["stats"].(map[string]any)
}

func TestFailedSDKTextCommitRetainsPriorSnapshotAndFile(t *testing.T) {
	f := setup(t)
	f.rpc(t, f.ace.PID, 102, 1, challengeUpload(string([]byte{0xff}), "kept"))
	path := f.store.path
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), "directory")
	if err = os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	f.store.path = blocked // The temporary file can be written, but rename must fail.
	if _, err = f.service.Handle(f.ace.PID, quazal.Request{Protocol: 102, Method: 1, Args: challengeUpload(string(bytes.Repeat([]byte{0x80}, 255)), "uncommitted")}); err == nil {
		t.Fatal("failed rename returned success")
	}
	if f.store.data.NextFile != 2 || len(f.store.data.Files) != 1 || f.store.data.Files[1].Stats.Name != string([]byte{0xff}) {
		t.Fatal("failed commit published metadata or consumed ID")
	}
	retained, _ := os.ReadFile(path)
	if !bytes.Equal(original, retained) {
		t.Fatal("prior disk state changed")
	}
	if _, err = Open(path); err != nil {
		t.Fatal("prior state no longer loads", err)
	}
	temps, err := filepath.Glob(filepath.Join(filepath.Dir(blocked), ".title-*"))
	if err != nil || len(temps) != 0 {
		t.Fatal("failed commit left temporary files", temps, err)
	}
}
