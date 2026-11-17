package tdu

import (
	"net/netip"
	"testing"
)

func TestConfigDefaultsAndValidation(t *testing.T) {
	good := DefaultConfig()
	good.PublicIP = netip.MustParseAddr("192.0.2.10")
	good.StateDir = t.TempDir()
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	if good.AuthPort != 30200 || good.SecurePort != 30201 || good.MasterPort != 8124 || good.ProxyPort != 8122 || good.DirectoryPort != 9000 || good.ResourcePort != 9001 || good.SessionPort != 9002 || good.DensityPort != 9003 || good.UserMapPort != 9004 {
		t.Fatal("retail endpoints changed")
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.Bind = netip.IPv6Loopback() }, func(c *Config) { c.PublicIP = netip.IPv4Unspecified() }, func(c *Config) { c.PublicIP = netip.MustParseAddr("239.1.2.3") }, func(c *Config) { c.PublicIP = netip.MustParseAddr("255.255.255.255") },
		func(c *Config) { c.AuthPort = c.SecurePort }, func(c *Config) { c.MasterPort = c.ProxyPort }, func(c *Config) { c.StateDir = "" }, func(c *Config) { c.DirectoryPort = 0 }, func(c *Config) { c.ResourcePort = c.DirectoryPort },
		func(c *Config) { c.ResourcePort = 0; c.NetConfigPath = "private.xml" },
		func(c *Config) { c.SessionPort = 0 }, func(c *Config) { c.DensityPort = 0 }, func(c *Config) { c.UserMapPort = 0 },
		func(c *Config) { c.SessionPort = c.DirectoryPort }, func(c *Config) { c.UserMapPort = c.DensityPort },
	} {
		c := good
		change(&c)
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted invalid config %+v", c)
		}
	}
	c := good
	c.AuthPort, c.SecurePort, c.MasterPort, c.ProxyPort = 0, 0, 0, 0
	if err := c.Validate(); err != nil {
		t.Fatal("ephemeral synthetic endpoints", err)
	}
	c.DirectoryPort, c.ResourcePort = 0, 0
	if err := c.Validate(); err != nil {
		t.Fatal("disabled resources", err)
	}
}
func TestLoadConfigOnlyUsesTDUOverrides(t *testing.T) {
	keys := []string{"BIND", "PUBLIC_IP", "AUTH_PORT", "SECURE_PORT", "MASTER_PORT", "PROXY_PORT", "DIRECTORY_PORT", "RESOURCE_PORT", "SESSION_PORT", "DENSITY_PORT", "USERMAP_PORT", "ACTIVITY_CATALOG", "NETCONFIG", "PRIZE"}
	for _, key := range keys {
		t.Setenv("QUETZAL_TDU_"+key, "")
	}
	ip := netip.MustParseAddr("192.0.2.10")
	dir := t.TempDir()
	c, err := LoadConfig("127.0.0.1", ip, dir)
	if err != nil || c.Bind != netip.MustParseAddr("127.0.0.1") || c.PublicIP != ip || c.StateDir != dir {
		t.Fatal(c, err)
	}
	t.Setenv("QUETZAL_TDU_BIND", "127.0.0.2")
	t.Setenv("QUETZAL_TDU_PUBLIC_IP", "192.0.2.20")
	t.Setenv("QUETZAL_TDU_SECURE_PORT", "0")
	t.Setenv("QUETZAL_TDU_NETCONFIG", "private.xml")
	t.Setenv("QUETZAL_TDU_SESSION_PORT", "19002")
	t.Setenv("QUETZAL_TDU_ACTIVITY_CATALOG", "activity-catalog.json")
	c, err = LoadConfig("127.0.0.1", ip, dir)
	if err != nil || c.Bind.String() != "127.0.0.2" || c.PublicIP.String() != "192.0.2.20" || c.SecurePort != 0 || c.NetConfigPath != "private.xml" || c.SessionPort != 19002 || c.ActivityCatalogPath != "activity-catalog.json" {
		t.Fatal(c, err)
	}
	for _, value := range []string{"-1", "65536", "NaN"} {
		t.Setenv("QUETZAL_TDU_AUTH_PORT", value)
		if _, err = LoadConfig("127.0.0.1", ip, dir); err == nil {
			t.Fatal("accepted invalid port", value)
		}
	}
}
