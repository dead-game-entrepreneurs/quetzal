package snowblind

import (
	"net/netip"
	"testing"
)

func TestSnowblindRelayConfiguration(t *testing.T) {
	for _, key := range []string{"BIND", "PUBLIC_IP", "AUTH_PORT", "SECURE_PORT", "PAYLOAD_MODE", "RELAY_MODE", "RELAY_FIRST_PORT", "RELAY_LAST_PORT"} {
		t.Setenv("QUETZAL_SNOWBLIND_"+key, "")
	}
	c, err := LoadConfig("127.0.0.1", netip.MustParseAddr("192.0.2.10"), t.TempDir())
	if err != nil || c.RelayMode != "adaptive" || c.RelayFirstPort != 30800 || c.RelayLastPort != 30927 {
		t.Fatal("relay defaults", c, err)
	}
	t.Setenv("QUETZAL_SNOWBLIND_RELAY_MODE", "relay")
	t.Setenv("QUETZAL_SNOWBLIND_RELAY_FIRST_PORT", "31000")
	t.Setenv("QUETZAL_SNOWBLIND_RELAY_LAST_PORT", "31007")
	overridden, err := LoadConfig("127.0.0.1", c.PublicIP, c.StateDir)
	if err != nil || overridden.RelayMode != "relay" || overridden.RelayFirstPort != 31000 || overridden.RelayLastPort != 31007 {
		t.Fatal("relay overrides", overridden, err)
	}
	for _, change := range []func(*Config){func(c *Config) { c.RelayMode = "auto" }, func(c *Config) { c.RelayLastPort = c.RelayFirstPort }, func(c *Config) { c.RelayFirstPort = 13015; c.RelayLastPort = 13016 }, func(c *Config) { c.RelayFirstPort = 1023 }} {
		bad := c
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid relay config accepted", bad)
		}
	}
	t.Setenv("QUETZAL_SNOWBLIND_RELAY_FIRST_PORT", "65536")
	if _, err := LoadConfig("127.0.0.1", c.PublicIP, c.StateDir); err == nil {
		t.Fatal("out-of-range relay port accepted")
	}
	c.RelayMode = "direct"
	c.RelayFirstPort = 0
	c.RelayLastPort = 0
	if err := c.Validate(); err != nil {
		t.Fatal("direct mode should not reserve a pool", err)
	}
}
