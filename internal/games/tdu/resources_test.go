package tdu

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/state"
)

func TestNativeResourcesBoundFilesAndPersistPeerKey(t *testing.T) {
	cfg := localConfig(t)
	cfg.DirectoryPort, cfg.ResourcePort = 9000, 9001
	cfg.NetConfigPath = filepath.Join(t.TempDir(), "private.xml")
	for _, bad := range [][]byte{nil, []byte("<unclosed>"), []byte("no XML"), []byte("<first/><second/>"), []byte("<root/>trailing"), bytes.Repeat([]byte{'x'}, 4097)} {
		if err := os.WriteFile(cfg.NetConfigPath, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := nativeResources(cfg); err == nil {
			t.Fatal("accepted invalid resource")
		}
	}
	data := []byte("<synthetic_test_fixture/>")
	if err := os.WriteFile(cfg.NetConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	first, err := nativeResources(cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := nativeResources(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first["netconfig.xml"], data) || len(first["p2pkey"]) != 16 || !bytes.Equal(first["p2pkey"], second["p2pkey"]) {
		t.Fatal("resource contents or persistent key changed")
	}
	if !bytes.Equal(first["prize.xml"], ednet.NoAwardsConfig()) {
		t.Fatal("default prize policy changed")
	}
	principal, err := state.PrincipalKey(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(principal[:], first["p2pkey"]) {
		t.Fatal("reused private ticket key as public peer resource")
	}
}

func TestDefaultNativeResourcesUseRecoveredPolicy(t *testing.T) {
	cfg := localConfig(t)
	cfg.DirectoryPort, cfg.ResourcePort = 9000, 9001
	resources, err := nativeResources(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resources["netconfig.xml"], ednet.DefaultNetworkConfig()) || !bytes.Equal(resources["prize.xml"], ednet.NoAwardsConfig()) || len(resources["p2pkey"]) != 16 {
		t.Fatal("default native resource policy changed")
	}
}

func TestResourceOverridesArePrivateRegularFiles(t *testing.T) {
	cfg := localConfig(t)
	cfg.NetConfigPath = filepath.Join(t.TempDir(), "netconfig.xml")
	if err := os.WriteFile(cfg.NetConfigPath, []byte("<NETCONFIG/>"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg.NetConfigPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeResources(cfg); err == nil {
		t.Fatal("accepted public override")
	}
	if err := os.Chmod(cfg.NetConfigPath, 0600); err != nil {
		t.Fatal(err)
	}
	link := cfg.NetConfigPath + ".link"
	if err := os.Symlink(cfg.NetConfigPath, link); err != nil {
		t.Fatal(err)
	}
	cfg.NetConfigPath = link
	if _, err := nativeResources(cfg); err == nil {
		t.Fatal("accepted symlink override")
	}
	cfg.NetConfigPath = filepath.Dir(link)
	if _, err := nativeResources(cfg); err == nil {
		t.Fatal("accepted directory override")
	}
}
