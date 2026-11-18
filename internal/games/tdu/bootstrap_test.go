package tdu

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"os"
	"testing"
)

// The fixture executes the retail response reader through the UDP send boundary.
// A reversed scalar still looks like an IPv4 address but sends to another host.
func TestBootstrapAddressMatchesNativeDestination(t *testing.T) {
	data, err := os.ReadFile("testdata/native-bootstrap-address.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Address string
			Correct struct {
				WireHex     string `json:"wire_hex"`
				Destination string
				Port        uint16
			}
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 2 {
		t.Fatal("missing native bootstrap cases")
	}
	for _, row := range fixture.Cases {
		t.Run(row.Address, func(t *testing.T) {
			want, err := hex.DecodeString(row.Correct.WireHex)
			if err != nil {
				t.Fatal(err)
			}
			if len(want) != 4 || row.Correct.Destination != row.Address || row.Correct.Port != 8124 {
				t.Fatal("invalid native bootstrap destination fixture")
			}
			cfg := localConfig(t)
			cfg.PublicIP = netip.MustParseAddr(row.Address)
			addresses := startAdapter(t, cfg)
			pid, ticket := loginTicket(t, addresses.Auth, "guest", "h7fyctiuucf", addresses.Secure.Port())
			client := secureProtocol(t, addresses.Secure, pid, ticket, 0x71712000)
			if got := client.rpc(104, 1, nil); !bytes.Equal(got.Payload, want) {
				t.Fatalf("bootstrap address bytes %x; native destination %s requires %x", got.Payload, row.Address, want)
			}
		})
	}
}
