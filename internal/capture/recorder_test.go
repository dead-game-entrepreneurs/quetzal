package capture

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCompleteDatagramsAndConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte{0, 0xff, 0x1b}, 21835) // 65,505 bytes, near UDP's maximum.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Record("25tolife", "127.0.0.1:30120", "127.0.0.1:42000", payload); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, "packets.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 262144)
	n := 0
	for scanner.Scan() {
		var packet Packet
		if err := json.Unmarshal(scanner.Bytes(), &packet); err != nil {
			t.Fatal(err)
		}
		got, err := hex.DecodeString(packet.PayloadHex)
		if err != nil || !bytes.Equal(got, payload) || packet.Length != len(payload) {
			t.Fatal("datagram truncated or corrupted")
		}
		if packet.Time.IsZero() || packet.Game != "25tolife" || packet.Direction != "rx" {
			t.Fatal("missing provenance")
		}
		n++
	}
	if scanner.Err() != nil || n != 4 {
		t.Fatalf("packets=%d, error=%v", n, scanner.Err())
	}
}

func TestRotationRemainsBoundedAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	const limit = 262144
	for range 2 {
		r, err := Open(dir, limit)
		if err != nil {
			t.Fatal(err)
		}
		for range 10 {
			if err := r.Record("25tolife", "local", "remote", bytes.Repeat([]byte{0x5a}, 65507)); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(dir, "packets.ndjson*"))
	if err != nil || len(files) != 4 {
		t.Fatalf("files=%v, err=%v", files, err)
	}
	for _, file := range files {
		st, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if st.Size() > limit || st.Mode().Perm() != 0600 {
			t.Fatalf("unexpected size or permissions: %s %v", file, st)
		}
	}
}
