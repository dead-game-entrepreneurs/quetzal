// Package tdu composes the US PS2 Test Drive Unlimited backend as a tenant.
package tdu

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
)

type Config struct {
	Bind, PublicIP                              netip.Addr
	StateDir                                    string
	AuthPort, SecurePort, MasterPort, ProxyPort uint16
	// These destinations are carried inside the native proxy, not UDP listeners.
	DirectoryPort, ResourcePort           uint16
	SessionPort, DensityPort, UserMapPort uint16
	NetConfigPath, PrizePath              string
	ActivityCatalogPath                   string
}

func DefaultConfig() Config {
	return Config{
		Bind:          netip.IPv4Unspecified(),
		AuthPort:      30200,
		SecurePort:    30201,
		MasterPort:    8124,
		ProxyPort:     8122,
		DirectoryPort: 9000,
		ResourcePort:  9001,
		SessionPort:   9002,
		DensityPort:   9003,
		UserMapPort:   9004,
	}
}

// LoadConfig is called only when the TDU tenant is enabled. StateDir is already
// scoped by the application; the adapter neither shares another tenant's state
// nor acquires a second process lock.
func LoadConfig(bind string, publicIP netip.Addr, stateDir string) (Config, error) {
	c := DefaultConfig()
	c.PublicIP = publicIP
	c.StateDir = stateDir
	if value := os.Getenv("QUETZAL_TDU_BIND"); value != "" {
		bind = value
	}
	var err error
	c.Bind, err = netip.ParseAddr(bind)
	if err != nil {
		return c, fmt.Errorf("invalid TDU bind: %w", err)
	}
	if value := os.Getenv("QUETZAL_TDU_PUBLIC_IP"); value != "" {
		c.PublicIP, err = netip.ParseAddr(value)
		if err != nil {
			return c, fmt.Errorf("invalid QUETZAL_TDU_PUBLIC_IP: %w", err)
		}
	}
	for _, entry := range []struct {
		name string
		port *uint16
	}{
		{"AUTH_PORT", &c.AuthPort},
		{"SECURE_PORT", &c.SecurePort},
		{"MASTER_PORT", &c.MasterPort},
		{"PROXY_PORT", &c.ProxyPort},
		{"DIRECTORY_PORT", &c.DirectoryPort},
		{"RESOURCE_PORT", &c.ResourcePort},
		{"SESSION_PORT", &c.SessionPort},
		{"DENSITY_PORT", &c.DensityPort},
		{"USERMAP_PORT", &c.UserMapPort},
	} {
		key := "QUETZAL_TDU_" + entry.name
		if value := os.Getenv(key); value != "" {
			n, e := strconv.ParseUint(value, 10, 16)
			if e != nil {
				return c, fmt.Errorf("invalid %s", key)
			}
			*entry.port = uint16(n)
		}
	}
	c.NetConfigPath = os.Getenv("QUETZAL_TDU_NETCONFIG")
	c.PrizePath = os.Getenv("QUETZAL_TDU_PRIZE")
	c.ActivityCatalogPath = os.Getenv("QUETZAL_TDU_ACTIVITY_CATALOG")
	return c, c.Validate()
}
func (c Config) Validate() error {
	if !c.Bind.Is4() || c.Bind.IsMulticast() || c.Bind == netip.MustParseAddr("255.255.255.255") {
		return errors.New("TDU bind must be an IPv4 address")
	}
	if !c.PublicIP.Is4() || c.PublicIP.IsUnspecified() || c.PublicIP.IsMulticast() || c.PublicIP == netip.MustParseAddr("255.255.255.255") {
		return errors.New("TDU public address must be a reachable IPv4 address")
	}
	if c.StateDir == "" {
		return errors.New("TDU state directory is required")
	}
	ports := map[uint16]string{}
	for _, entry := range []struct {
		name string
		port uint16
	}{{"auth", c.AuthPort}, {"secure", c.SecurePort}, {"master", c.MasterPort}, {"proxy", c.ProxyPort}} {
		if entry.port == 0 {
			continue
		}
		if previous, exists := ports[entry.port]; exists {
			return fmt.Errorf("TDU %s and %s UDP ports overlap", previous, entry.name)
		}
		ports[entry.port] = entry.name
	}
	if c.DirectoryPort == 0 && (c.ResourcePort != 0 || c.NetConfigPath != "" || c.PrizePath != "" || c.ActivityCatalogPath != "") {
		return errors.New("TDU native services require a directory logical port")
	}
	if c.DirectoryPort != 0 && c.DirectoryPort == c.ResourcePort {
		return errors.New("TDU directory and resource logical ports must differ")
	}
	if c.ResourcePort == 0 && (c.NetConfigPath != "" || c.PrizePath != "") {
		return errors.New("TDU resource overrides require a resource logical port")
	}
	if c.DirectoryPort != 0 {
		logical := map[uint16]string{c.DirectoryPort: "directory"}
		for _, entry := range []struct {
			name string
			port uint16
		}{{"resource", c.ResourcePort}, {"session", c.SessionPort}, {"density", c.DensityPort}, {"usermap", c.UserMapPort}} {
			if entry.port == 0 {
				if entry.name == "resource" {
					continue
				}
				return fmt.Errorf("TDU %s logical port is required with the directory", entry.name)
			}
			if old, ok := logical[entry.port]; ok {
				return fmt.Errorf("TDU %s and %s logical ports overlap", old, entry.name)
			}
			logical[entry.port] = entry.name
		}
	}
	return nil
}
