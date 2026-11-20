// Adapted from sibling tdu-server; title differences are documented in docs/games/25tolife/analysis.md.
package quazal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func TestStorePrivateAtomicAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	key := quazalcrypto.Key{1, 2, 3}
	for _, name := range []string{"guest", "Guest", "ab", "bad name", "bad\x00name", "sixteenlettersxxx"} {
		if _, err := store.Create(name, key); !errors.Is(err, ErrAccountName) {
			t.Fatalf("name %q: %v", name, err)
		}
	}
	first, err := store.Create("Driver", key)
	if err != nil || first.PID != 1000 {
		t.Fatal(first, err)
	}
	if _, err := store.Create("DRIVER", key); !errors.Is(err, ErrAccountExists) {
		t.Fatal("case duplicate")
	}
	if err := store.reservePID(1001); err != nil {
		t.Fatal(err)
	}
	second, err := store.Create("Second", key)
	if err != nil || second.PID != 1002 {
		t.Fatal("reserved PID not skipped")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary files were left behind")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path); err == nil {
		t.Fatal("loaded world-readable account hashes")
	}
}
func TestStoreFailedWriteDoesNotPublishAccount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	if _, err := store.Create("Driver", quazalcrypto.Key{}); err == nil {
		t.Fatal("unexpected persistent write success")
	}
	if _, ok := store.Lookup("Driver"); ok {
		t.Fatal("failed write published an account")
	}
}
func TestStoreRejectsCorruptionAndLinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	for _, data := range []string{
		`{"version":3,"next_pid":1000,"accounts":[]}`,
		`{"version":1,"next_pid":1000,"accounts":[],"unknown":true}`,
		`{"version":1,"next_pid":1000,"accounts":[]} {}`,
		`{"version":1,"next_pid":1001,"accounts":[{"name":"Player","pid":1000,"initial_hash":"bad"}]}`,
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenStore(path); err == nil {
			t.Fatal("loaded corrupt account store")
		}
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(link); err == nil {
		t.Fatal("followed account symlink")
	}
}

func TestContactAndPIDLookupPreserveProfileIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := store.CreateWithContact("ContactUser", quazalcrypto.Key{1}, "test@example.test")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := loaded.LookupPID(a.PID)
	if !ok || got.Name != a.Name || got.Contact != "test@example.test" || got.InitialKey != a.InitialKey {
		t.Fatal("contact/identity persistence")
	}
}

func TestInnerSpaceNamePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := OpenStoreWithNames(path, NamePolicy{InnerSpaces: true})
	if err != nil {
		t.Fatal(err)
	}
	key := quazalcrypto.Key{1, 2, 3}
	for _, name := range []string{" Card", "Card ", "   ", "a\tb", "ab", "guest", "sixteen letters!"} {
		if _, err := store.Create(name, key); !errors.Is(err, ErrAccountName) {
			t.Fatalf("name %q: %v", name, err)
		}
	}
	for _, name := range []string{"Card Shark", "a  b", "Mr X Y Z"} {
		if _, err := store.Create(name, key); err != nil {
			t.Fatalf("name %q: %v", name, err)
		}
	}
	if _, err := store.Create("card shark", key); !errors.Is(err, ErrAccountExists) {
		t.Fatal("case-insensitive duplicate with a space")
	}
	if a, ok := store.Lookup("CARD SHARK"); !ok || a.Name != "Card Shark" {
		t.Fatal("lookup of a name with a space")
	}
	// The policy also governs names loaded from disk.
	reopened, err := OpenStoreWithNames(path, NamePolicy{InnerSpaces: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Lookup("Card Shark"); !ok {
		t.Fatal("stored name lost on reopen")
	}
	if _, err := OpenStore(path); err == nil {
		t.Fatal("default policy accepted a stored name with a space")
	}
}
