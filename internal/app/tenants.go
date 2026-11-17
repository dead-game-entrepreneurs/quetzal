package app

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/princebyyourlogic/quetzal/internal/game"
)

func validKey(key string) bool {
	if len(key) < 1 || len(key) > 64 {
		return false
	}
	for _, b := range key {
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return false
		}
	}
	return true
}

// ValidateTenants rejects conflicting static sockets, lazy reservations and HTTP
// ownership before anything is bound. IPv4 wildcard binds overlap every address.
func ValidateTenants(c Config, defs []game.Definition) error {
	if len(defs) == 0 {
		return fmt.Errorf("at least one game tenant is required")
	}
	type reservation struct {
		key         string
		ip          netip.Addr
		first, last int
	}
	var reservations []reservation
	keys, paths := map[string]bool{}, map[string]string{}
	for _, def := range defs {
		if !validKey(def.Key) || keys[def.Key] {
			return fmt.Errorf("invalid or duplicate tenant key %q", def.Key)
		}
		keys[def.Key] = true
		if def.NewUDP != nil && def.NewServices != nil {
			return fmt.Errorf("tenant %s defines both UDP constructors", def.Key)
		}
		for _, s := range def.Auxiliary {
			if s == nil {
				return fmt.Errorf("tenant %s has a nil auxiliary service", def.Key)
			}
		}
		bind := def.Bind
		if bind == "" {
			bind = c.Bind
		}
		ip, err := netip.ParseAddr(bind)
		if err != nil || !ip.Is4() || ip.IsMulticast() {
			return fmt.Errorf("tenant %s bind must be IPv4", def.Key)
		}
		add := func(first, last int) error {
			if first < 1 || last < first || last > 65535 {
				return fmt.Errorf("tenant %s has invalid UDP reservation %d..%d", def.Key, first, last)
			}
			for _, old := range reservations {
				if (old.ip == ip || old.ip.IsUnspecified() || ip.IsUnspecified()) && first <= old.last && old.first <= last {
					return fmt.Errorf("tenant %s UDP %d..%d conflicts with tenant %s UDP %d..%d on overlapping bind addresses; choose non-overlapping listener/relay ports", def.Key, first, last, old.key, old.first, old.last)
				}
			}
			reservations = append(reservations, reservation{def.Key, ip, first, last})
			return nil
		}
		for _, port := range def.UDPPorts {
			if port == 0 {
				continue
			}
			if err := add(port, port); err != nil {
				return err
			}
		}
		for _, r := range def.ReservedUDP {
			if err := add(r.First, r.Last); err != nil {
				return err
			}
		}
		for _, path := range def.HTTPPaths {
			if !strings.HasPrefix(path, "/") || path == "/" || strings.ContainsAny(path, " {}?#%\t\r\n") || strings.HasPrefix(path, "/healthz") || strings.HasPrefix(path, "/games/") {
				return fmt.Errorf("tenant %s has invalid HTTP path %q", def.Key, path)
			}
			for oldPath, oldKey := range paths {
				if path == oldPath || strings.HasSuffix(path, "/") && strings.HasPrefix(oldPath, path) || strings.HasSuffix(oldPath, "/") && strings.HasPrefix(path, oldPath) {
					return fmt.Errorf("HTTP paths %s and %s claimed by tenants %s and %s overlap", path, oldPath, oldKey, def.Key)
				}
			}
			paths[path] = def.Key
		}
	}
	return nil
}
