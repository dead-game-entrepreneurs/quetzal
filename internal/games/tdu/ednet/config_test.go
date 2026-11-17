package ednet

import (
	"bytes"
	"encoding/xml"
	"io"
	"math"
	"net/netip"
	"strconv"
	"testing"
)

func TestDefaultConfigSchemaAndPolicy(t *testing.T) {
	b := DefaultNetworkConfig()
	if len(b) == 0 || len(b) > 4096 {
		t.Fatalf("resource length %d", len(b))
	}
	dec := xml.NewDecoder(bytes.NewReader(b))
	settings := make(map[string]string)
	depth := 0
	for {
		token, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		switch token := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				if token.Name.Local != "NETCONFIG" || len(token.Attr) != 0 {
					t.Fatal("wrong config root")
				}
				continue
			}
			if depth != 2 || len(token.Attr) != 1 || token.Attr[0].Name.Local != "value" {
				t.Fatal("wrong setting schema")
			}
			if _, exists := settings[token.Name.Local]; exists {
				t.Fatal("duplicate setting")
			}
			v := token.Attr[0].Value
			n, e := strconv.ParseFloat(v, 64)
			if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
				t.Fatalf("invalid numeric setting %q", v)
			}
			settings[token.Name.Local] = v
		case xml.EndElement:
			depth--
		}
	}
	if len(settings) != 39 || depth != 0 {
		t.Fatalf("settings%d depth%d", len(settings), depth)
	}
	// Recovered values that govern proxy timing, directory retry, action retry and
	// group size/range. These intentionally assert retail defaults, not availability.
	for name, value := range map[string]string{"PROXY_HEARTBEAT": "8.0", "ORB_REFRESH_DT": "10000", "ORB_LOST_RPC_DT": "60000", "EDNETACTION_DEFAULT_TIMEOUT": "0.250", "EDNETACTION_DEFAULT_MAX_RETRY": "20", "P2PFR_STRENGTH_RADIUS": "1500", "MAX_SESSIONS_QUERY": "10"} {
		if settings[name] != value {
			t.Fatalf("%s=%s", name, settings[name])
		}
	}
	first := DefaultSettings()
	first[0].Value = "999"
	if DefaultSettings()[0].Value != "1300" {
		t.Fatal("aliased settings")
	}
	b[0] = '?'
	if !bytes.HasPrefix(DefaultNetworkConfig(), []byte("<NETCONFIG>")) {
		t.Fatal("aliased XML")
	}
}
func TestNoAwardsConfigHasNoMutatingEntries(t *testing.T) {
	var policy struct {
		XMLName xml.Name `xml:"PRIZES"`
		Prizes  []struct {
			ID    string `xml:"id,attr"`
			Type  string `xml:"type,attr"`
			Value string `xml:"value,attr"`
		} `xml:"PRIZE"`
	}
	b := NoAwardsConfig()
	if e := xml.Unmarshal(b, &policy); e != nil {
		t.Fatal(e)
	}
	if policy.XMLName.Local != "PRIZES" || len(policy.Prizes) != 0 {
		t.Fatal("unexpected prize grants")
	}
	if !bytes.Equal(b, NoAwardsConfig()) {
		t.Fatal("nondeterministic")
	}
	s, e := New(Options{PublicIP: netip.MustParseAddr("192.0.2.1"), DirectoryPort: 9000, ResourcePort: 9001, Resources: map[string][]byte{"netconfig.xml": DefaultNetworkConfig(), "prize.xml": NoAwardsConfig()}})
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"netconfig.xml", "prize.xml"} {
		out, e := s.ResourceAction(open(name, 0))
		if e != nil || out[2] != 0 {
			t.Fatalf("generated resource unavailable: %s %v", name, e)
		}
	}
}
