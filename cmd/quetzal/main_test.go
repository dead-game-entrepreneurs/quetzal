package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/app"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func testTenantsConfig(t *testing.T) app.Config {
	t.Helper()
	for _, key := range []string{
		"QUETZAL_RELAY_MODE", "QUETZAL_RELAY_FIRST_PORT", "QUETZAL_RELAY_LAST_PORT",
		"QUETZAL_TDU_BIND", "QUETZAL_TDU_PUBLIC_IP",
		"QUETZAL_TDU_AUTH_PORT", "QUETZAL_TDU_SECURE_PORT",
		"QUETZAL_TDU_MASTER_PORT", "QUETZAL_TDU_PROXY_PORT",
		"QUETZAL_TDU_DIRECTORY_PORT", "QUETZAL_TDU_RESOURCE_PORT",
		"QUETZAL_TDU_SESSION_PORT", "QUETZAL_TDU_DENSITY_PORT", "QUETZAL_TDU_USERMAP_PORT",
		"QUETZAL_TDU_ACTIVITY_CATALOG", "QUETZAL_TDU_NETCONFIG", "QUETZAL_TDU_PRIZE",
	} {
		t.Setenv(key, "")
	}
	return app.Config{Bind: "127.0.0.1", PublicIP: netip.MustParseAddr("192.0.2.10"), StateDir: t.TempDir(), Games: []string{"25tolife", "tdu"}}
}
func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func TestBothAdaptersKeepLegacyAccountsAndIsolateTenantKeys(t *testing.T) {
	cfg := testTenantsConfig(t)
	legacyKey := bytes.Repeat([]byte{0x42}, 16)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "principal.key"), legacyKey, 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := quazal.OpenStore(filepath.Join(cfg.StateDir, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	initial := quazalcrypto.InitialKey([]byte("synthetic password"))
	old, err := legacy.Create("same_name", initial)
	if err != nil {
		t.Fatal(err)
	}
	defs, closeTenants, err := buildTenants(cfg, testLog())
	if err != nil {
		t.Fatal(err)
	}
	defer closeTenants()
	if len(defs) != 2 || defs[0].Key != "25tolife" || defs[1].Key != "tdu" {
		t.Fatalf("definitions %+v", defs)
	}
	persisted, _ := os.ReadFile(filepath.Join(cfg.StateDir, "principal.key"))
	if !bytes.Equal(persisted, legacyKey) {
		t.Fatal("legacy key changed")
	}
	tduDir := filepath.Join(cfg.StateDir, "tenants", "tdu")
	tduKey, err := os.ReadFile(filepath.Join(tduDir, "principal.key"))
	if err != nil || bytes.Equal(tduKey, legacyKey) {
		t.Fatal("tenant principal not isolated", err)
	}
	peerKey, err := os.ReadFile(filepath.Join(tduDir, "p2p.key"))
	if err != nil || bytes.Equal(peerKey, tduKey) {
		t.Fatal("public peer key reused private key", err)
	}
	tduStore, err := quazal.OpenStore(filepath.Join(tduDir, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tduStore.Lookup("same_name"); ok {
		t.Fatal("legacy account leaked into TDU")
	}
	created, err := tduStore.Create("same_name", quazalcrypto.InitialKey([]byte("different password")))
	if err != nil {
		t.Fatal(err)
	}
	if created.PID != old.PID || created.InitialKey == old.InitialKey {
		t.Fatal("tenant account namespaces are not independent")
	}
	again, err := quazal.OpenStore(filepath.Join(cfg.StateDir, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	kept, ok := again.Lookup("same_name")
	if !ok || kept != old {
		t.Fatal("legacy account was changed")
	}
	for _, dir := range []string{cfg.StateDir, tduDir} {
		if lock, _, err := app.OpenState(dir); err == nil {
			lock.Close()
			t.Fatal("tenant state lock not held")
		}
	}
	closeTenants()
	for _, dir := range []string{cfg.StateDir, tduDir} {
		lock, _, err := app.OpenState(dir)
		if err != nil {
			t.Fatal("lock not released", err)
		}
		lock.Close()
	}
}
func TestRelayPoolOverlapFailsBeforeServingAndReleasesTenantLocks(t *testing.T) {
	cfg := testTenantsConfig(t)
	t.Setenv("QUETZAL_RELAY_MODE", "adaptive")
	if _, _, err := buildTenants(cfg, testLog()); err == nil {
		t.Fatal("accepted relay/TDU listener overlap")
	}
	for _, dir := range []string{cfg.StateDir, filepath.Join(cfg.StateDir, "tenants", "tdu")} {
		lock, _, err := app.OpenState(dir)
		if err != nil {
			t.Fatal("failed init leaked lock", err)
		}
		lock.Close()
	}
	t.Setenv("QUETZAL_RELAY_FIRST_PORT", "30400")
	t.Setenv("QUETZAL_RELAY_LAST_PORT", "30527")
	defs, cleanup, err := buildTenants(cfg, testLog())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(defs[0].ReservedUDP) != 1 || defs[0].ReservedUDP[0].First != 30400 {
		t.Fatal("relay pool not reserved")
	}
}
func TestDisabledAdaptersDoNotLoadTheirConfiguration(t *testing.T) {
	cfg := testTenantsConfig(t)
	cfg.Games = []string{"25tolife"}
	t.Setenv("QUETZAL_TDU_AUTH_PORT", "invalid")
	defs, cleanup, err := buildTenants(cfg, testLog())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(defs) != 1 {
		t.Fatal("enabled wrong adapters")
	}
}
func TestUnknownGameFailsBeforeCreatingState(t *testing.T) {
	cfg := testTenantsConfig(t)
	cfg.StateDir = filepath.Join(cfg.StateDir, "new")
	cfg.Games = []string{"unknown"}
	if _, _, err := buildTenants(cfg, testLog()); err == nil {
		t.Fatal("unknown adapter accepted")
	}
	if _, err := os.Stat(cfg.StateDir); !os.IsNotExist(err) {
		t.Fatal("created state for unknown game")
	}
}
func TestWCCTenantIsolatesStateAndRejectsDefaultRelayOverlap(t *testing.T) {
	cfg := testTenantsConfig(t)
	cfg.Games = []string{"25tolife", "wcc"}
	// WCC's retail auth port 30280 sits inside 25 to Life's default relay pool.
	t.Setenv("QUETZAL_RELAY_MODE", "adaptive")
	if _, _, err := buildTenants(cfg, testLog()); err == nil {
		t.Fatal("accepted relay/WCC listener overlap")
	}
	t.Setenv("QUETZAL_RELAY_FIRST_PORT", "30400")
	t.Setenv("QUETZAL_RELAY_LAST_PORT", "30527")
	defs, cleanup, err := buildTenants(cfg, testLog())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(defs) != 2 || defs[1].Key != "wcc" || len(defs[1].UDPPorts) != 2 || defs[1].UDPPorts[0] != 30280 || defs[1].UDPPorts[1] != 30281 {
		t.Fatalf("WCC definition %+v", defs[1])
	}
	if _, err := os.Stat(filepath.Join(cfg.StateDir, "tenants", "wcc", "principal.key")); err != nil {
		t.Fatal("WCC tenant state not isolated", err)
	}
}
