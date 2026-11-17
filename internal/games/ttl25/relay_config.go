package ttl25

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
)

// RelayConfig controls native peer forwarding, separately from backend PRUDP.
type RelayConfig struct {
	Mode                string
	Bind, PublicIP      netip.Addr
	FirstPort, LastPort uint16
}

func LoadRelayConfig(bind string, publicIP netip.Addr) (RelayConfig, error) {
	a, err := netip.ParseAddr(bind)
	if err != nil {
		return RelayConfig{}, err
	}
	c := RelayConfig{Mode: "direct", Bind: a, PublicIP: publicIP, FirstPort: 30200, LastPort: 30327}
	if value := os.Getenv("QUETZAL_RELAY_MODE"); value != "" {
		c.Mode = value
	}
	for _, item := range []struct {
		key string
		dst *uint16
	}{{"QUETZAL_RELAY_FIRST_PORT", &c.FirstPort}, {"QUETZAL_RELAY_LAST_PORT", &c.LastPort}} {
		if value := os.Getenv(item.key); value != "" {
			n, err := strconv.ParseUint(value, 10, 16)
			if err != nil {
				return c, fmt.Errorf("invalid %s", item.key)
			}
			*item.dst = uint16(n)
		}
	}
	return c, c.validate()
}

func (c RelayConfig) validate() error {
	if c.Mode != "direct" && c.Mode != "adaptive" && c.Mode != "relay" {
		return fmt.Errorf("invalid QUETZAL_RELAY_MODE")
	}
	if c.Mode == "direct" {
		return nil
	}
	if !c.Bind.Is4() || !c.PublicIP.Is4() || c.PublicIP.IsUnspecified() || c.PublicIP.IsMulticast() {
		return fmt.Errorf("relay requires IPv4 bind and public addresses")
	}
	if c.FirstPort < 1024 || c.LastPort <= c.FirstPort || int(c.LastPort)-int(c.FirstPort)+1 > 512 {
		return fmt.Errorf("relay pool must contain 2 to 512 unprivileged UDP ports")
	}
	for _, port := range []uint16{30120, 30121, 10100, 10105, 10110} {
		if port >= c.FirstPort && port <= c.LastPort {
			return fmt.Errorf("relay pool overlaps a backend port")
		}
	}
	return nil
}
