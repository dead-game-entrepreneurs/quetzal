package snowblind

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal/peerrelay"
)

type Config struct {
	Bind, PublicIP       netip.Addr
	AuthPort, SecurePort uint16
	StateDir             string
	// The stock US guest-login capture uses the plain compression wrapper.
	PayloadMode                   string
	RelayMode                     string
	RelayFirstPort, RelayLastPort uint16
}

func DefaultConfig() Config {
	return Config{AuthPort: 13015, SecurePort: 13016, PayloadMode: "plain", RelayMode: "adaptive", RelayFirstPort: 30800, RelayLastPort: 30927}
}
func LoadConfig(bind string, publicIP netip.Addr, stateDir string) (Config, error) {
	c := DefaultConfig()
	c.PublicIP = publicIP
	c.StateDir = stateDir
	if value := os.Getenv("QUETZAL_SNOWBLIND_BIND"); value != "" {
		bind = value
	}
	var err error
	c.Bind, err = netip.ParseAddr(bind)
	if err != nil {
		return c, fmt.Errorf("invalid Snowblind bind: %w", err)
	}
	if value := os.Getenv("QUETZAL_SNOWBLIND_PUBLIC_IP"); value != "" {
		c.PublicIP, err = netip.ParseAddr(value)
		if err != nil {
			return c, fmt.Errorf("invalid Snowblind public IP: %w", err)
		}
	}
	for _, entry := range []struct {
		name string
		port *uint16
	}{{"AUTH_PORT", &c.AuthPort}, {"SECURE_PORT", &c.SecurePort}, {"RELAY_FIRST_PORT", &c.RelayFirstPort}, {"RELAY_LAST_PORT", &c.RelayLastPort}} {
		key := "QUETZAL_SNOWBLIND_" + entry.name
		if value := os.Getenv(key); value != "" {
			n, e := strconv.ParseUint(value, 10, 16)
			if e != nil {
				return c, fmt.Errorf("invalid %s", key)
			}
			*entry.port = uint16(n)
		}
	}
	if value := os.Getenv("QUETZAL_SNOWBLIND_PAYLOAD_MODE"); value != "" {
		c.PayloadMode = value
	}
	if value := os.Getenv("QUETZAL_SNOWBLIND_RELAY_MODE"); value != "" {
		c.RelayMode = value
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if !c.Bind.Is4() || c.Bind.IsMulticast() || c.Bind.String() == "255.255.255.255" {
		return errors.New("Snowblind bind must be IPv4")
	}
	if !c.PublicIP.Is4() || c.PublicIP.IsUnspecified() || c.PublicIP.IsMulticast() || c.PublicIP.String() == "255.255.255.255" {
		return errors.New("Snowblind requires a usable public IPv4 address")
	}
	if c.StateDir == "" {
		return errors.New("Snowblind state directory is required")
	}
	if c.AuthPort != 0 && c.AuthPort == c.SecurePort {
		return errors.New("Snowblind auth and secure ports overlap")
	}
	switch c.PayloadMode {
	case "raw", "plain", "rc4":
	default:
		return errors.New("Snowblind payload mode must be raw, plain or rc4")
	}
	if err := c.relayConfig().Validate(); err != nil {
		return err
	}
	if c.RelayMode != "direct" {
		for _, port := range []uint16{c.AuthPort, c.SecurePort} {
			if port != 0 && port >= c.RelayFirstPort && port <= c.RelayLastPort {
				return errors.New("Snowblind relay pool overlaps a backend port")
			}
		}
	}
	return nil
}
func (c Config) relayConfig() peerrelay.Config {
	return peerrelay.Config{Mode: c.RelayMode, Bind: c.Bind, PublicIP: c.PublicIP, FirstPort: c.RelayFirstPort, LastPort: c.RelayLastPort, ProbeCandidates: true, IntroduceCandidates: true}
}
