package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServicePrincipalPersistsAcrossRestart(t *testing.T) {
	directory := t.TempDir()
	first, err := principalKey(directory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := principalKey(directory)
	if err != nil || first != second {
		t.Fatalf("persistent key changed: %v", err)
	}
	info, err := os.Stat(filepath.Join(directory, "principal.key"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("key permissions: %v %v", info, err)
	}
}

func TestInvalidStoredKeyIsNotRegenerated(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "principal.key")
	if err := os.WriteFile(path, []byte("truncated"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := principalKey(directory); err == nil {
		t.Fatal("accepted invalid key")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "truncated" {
		t.Fatal("changed an existing invalid key")
	}
}

func TestStateDirectoryHasSingleWriter(t *testing.T) {
	directory := t.TempDir()
	first, err := lockState(directory)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockState(directory); err == nil {
		_ = second.Close()
		t.Fatal("allowed second writer")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := lockState(directory)
	if err != nil {
		t.Fatal(err)
	}
	_ = third.Close()
}
