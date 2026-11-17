package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNamedKeysAreIndependentPrivateAndPersistent(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadKey(dir, "principal.key")
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadKey(dir, "p2p.key")
	if err != nil || a == b {
		t.Fatal("keys not independent", err)
	}
	again, err := LoadKey(dir, "p2p.key")
	if err != nil || again != b {
		t.Fatal("key changed", err)
	}
	for _, name := range []string{"principal.key", "p2p.key"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("key privacy", err)
		}
	}
	for _, name := range []string{"", ".", "..", "../escape", "a/b", "a\\b"} {
		if _, err := LoadKey(dir, name); err == nil {
			t.Fatalf("accepted key name %q", name)
		}
	}
}
