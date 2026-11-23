package commandos

import (
	"net/netip"
	"testing"
)

func TestCommandosConfigDefaultsAndOverrides(t *testing.T) {
	for _, key := range []string{"BIND", "PUBLIC_IP", "AUTH_PORT", "SECURE_PORT", "PAYLOAD_MODE", "RELAY_MODE", "RELAY_FIRST_PORT", "RELAY_LAST_PORT"} {
		t.Setenv("QUETZAL_COMMANDOS_"+key, "")
	}
	ip := netip.MustParseAddr("192.0.2.10")
	c, err := LoadConfig("127.0.0.1", ip, t.TempDir())
	if err != nil || c.AuthPort != 30060 || c.SecurePort != 30061 || c.PayloadMode != "plain" || c.PublicIP != ip {
		t.Fatalf("defaults %+v %v", c, err)
	}
	if c.RelayMode != "adaptive" || c.RelayFirstPort != 30600 || c.RelayLastPort != 30727 {
		t.Fatal("relay defaults", c)
	}
	t.Setenv("QUETZAL_COMMANDOS_BIND", "127.0.0.2")
	t.Setenv("QUETZAL_COMMANDOS_PUBLIC_IP", "198.51.100.1")
	t.Setenv("QUETZAL_COMMANDOS_AUTH_PORT", "31060")
	t.Setenv("QUETZAL_COMMANDOS_SECURE_PORT", "31061")
	t.Setenv("QUETZAL_COMMANDOS_PAYLOAD_MODE", "plain")
	c, err = LoadConfig("127.0.0.1", ip, c.StateDir)
	if err != nil || c.Bind.String() != "127.0.0.2" || c.PublicIP.String() != "198.51.100.1" || c.AuthPort != 31060 || c.SecurePort != 31061 || c.PayloadMode != "plain" {
		t.Fatalf("overrides %+v %v", c, err)
	}
	for _, mode := range []string{"auto", "", "lzo"} {
		bad := c
		bad.PayloadMode = mode
		if bad.Validate() == nil {
			t.Fatalf("accepted mode %q", mode)
		}
	}
	bad := c
	bad.SecurePort = bad.AuthPort
	if bad.Validate() == nil {
		t.Fatal("overlapping ports accepted")
	}
	bad = c
	bad.RelayFirstPort = 30060
	bad.RelayLastPort = 30100
	bad.AuthPort = 30060
	if bad.Validate() == nil {
		t.Fatal("relay overlapped backend")
	}
	bad = c
	bad.RelayLastPort = bad.RelayFirstPort
	if bad.Validate() == nil {
		t.Fatal("invalid relay pool")
	}
	t.Setenv("QUETZAL_COMMANDOS_AUTH_PORT", "65536")
	if _, err = LoadConfig("127.0.0.1", ip, c.StateDir); err == nil {
		t.Fatal("out of range port accepted")
	}
}
