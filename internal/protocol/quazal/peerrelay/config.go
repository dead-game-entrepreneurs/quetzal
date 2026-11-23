package peerrelay

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"time"
)

type Owner struct {
	LocalPort uint16
	PeerID    uint64
	PID       uint32
}
type Config struct {
	Mode                string
	Bind, PublicIP      netip.Addr
	FirstPort, LastPort uint16
	// Some browsers require a successful candidate NAT probe before allowing
	// a join. Probe responses may prove mappings; gameplay still needs a SYN.
	ProbeCandidates bool
	// IntroduceCandidates also queues the counterpart's virtual URL on an
	// authenticated browser probe. Commandos needs this before a session SYN;
	// titles that probe peers independently leave it disabled.
	IntroduceCandidates bool
}
type Options struct {
	Config Config
	Log    *slog.Logger
	Now    func() time.Time
	Listen func(uint16) (*net.UDPConn, error)
}

func (c Config) Validate() error {
	if c.Mode != "direct" && c.Mode != "adaptive" && c.Mode != "relay" {
		return fmt.Errorf("relay mode must be direct, adaptive or relay")
	}
	if c.Mode == "direct" {
		return nil
	}
	if !c.Bind.Is4() || c.Bind.IsMulticast() || !c.PublicIP.Is4() || c.PublicIP.IsUnspecified() || c.PublicIP.IsMulticast() || c.PublicIP.String() == "255.255.255.255" {
		return fmt.Errorf("relay requires usable IPv4 addresses")
	}
	if c.FirstPort < 1024 || c.LastPort <= c.FirstPort || int(c.LastPort)-int(c.FirstPort)+1 > 512 {
		return fmt.Errorf("relay pool must contain 2 to 512 unprivileged UDP ports")
	}
	return nil
}

type Snapshot struct {
	Mode             string `json:"mode"`
	Candidates       int    `json:"candidates"`
	Active           int    `json:"active"`
	Draining         int    `json:"draining"`
	ProvedMappings   int    `json:"proved_mappings"`
	ForwardedPackets uint64 `json:"forwarded_packets"`
	ForwardedBytes   uint64 `json:"forwarded_bytes"`
}

func (r *Relay) Snapshot() Snapshot {
	state := Snapshot{Mode: "direct"}
	if r == nil {
		return state
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state.Mode = r.cfg.Mode
	state.Candidates = len(r.pairs)
	for _, p := range r.pairs {
		if !p.closing.IsZero() {
			state.Draining++
		}
		if p.active {
			state.Active++
		}
		for i, m := range p.mapping {
			if m.proved {
				state.ProvedMappings++
			}
			state.ForwardedPackets += p.packets[i]
			state.ForwardedBytes += p.bytes[i]
		}
	}
	return state
}
func Field(url, key string) string { return stationField(url, key) }
func StationURL(original string, address netip.AddrPort, cid uint32) string {
	return relayStationURL(original, address, cid)
}
