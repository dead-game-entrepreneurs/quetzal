package ttl25

import (
	"net/netip"
	"testing"
)

func TestRelayConfigurationBounds(t *testing.T) {
	for _, key := range []string{"QUETZAL_RELAY_MODE", "QUETZAL_RELAY_FIRST_PORT", "QUETZAL_RELAY_LAST_PORT"} {
		t.Setenv(key, "")
	}
	ip := netip.MustParseAddr("127.0.0.1")
	c, err := LoadRelayConfig(ip.String(), ip)
	if err != nil || c.Mode != "direct" {
		t.Fatal("default mode", err)
	}
	t.Setenv("QUETZAL_RELAY_MODE", "adaptive")
	c, err = LoadRelayConfig(ip.String(), ip)
	if err != nil || c.FirstPort != 30200 || c.LastPort != 30327 {
		t.Fatal("default pool", err)
	}
	for _, test := range []struct{ key, value string }{{"QUETZAL_RELAY_MODE", "other"}, {"QUETZAL_RELAY_FIRST_PORT", "bad"}, {"QUETZAL_RELAY_FIRST_PORT", "1"}, {"QUETZAL_RELAY_FIRST_PORT", "30327"}, {"QUETZAL_RELAY_FIRST_PORT", "30121"}, {"QUETZAL_RELAY_LAST_PORT", "65535"}} {
		t.Run(test.key+test.value, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if _, err := LoadRelayConfig(ip.String(), ip); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
