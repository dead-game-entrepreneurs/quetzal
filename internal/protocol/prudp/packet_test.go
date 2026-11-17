package prudp

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestTitleSeedAndSyntheticWire(t *testing.T) {
	// Same shape as the first retail SYN, with a synthetic connection signature.
	// Expected bytes independently checked with TDU's Python checksum inspector.
	wire, _ := hex.DecodeString("3f31200000000000000078563412a3")
	p, n, err := ParseFrame(wire, 0xff)
	if err != nil || n != 15 || p.Source != 0x3f || p.Destination != 0x31 || p.Type != SYN ||
		p.Flags != FlagNeedsAck || p.ConnectionSignature != 0x12345678 {
		t.Fatalf("%+v %d %v", p, n, err)
	}
	got, err := p.Marshal(0xff)
	if err != nil || !bytes.Equal(got, wire) {
		t.Fatalf("wire %x: %v", got, err)
	}
	if _, _, err := ParseFrame(wire, 0xbe); err == nil {
		t.Fatal("TDU's title seed accepted for 25 to Life")
	}
	for i := range wire {
		bad := append([]byte(nil), wire...)
		bad[i] ^= 1
		if _, _, err := ParseFrame(bad, 0xff); err == nil {
			t.Fatalf("corruption accepted at %d", i)
		}
		if _, _, err := ParseFrame(wire[:i], 0xff); err == nil {
			t.Fatalf("truncation accepted at %d", i)
		}
	}
}

func TestChecksumCarriesAndWrap(t *testing.T) {
	for _, item := range []struct {
		hex  string
		want byte
	}{
		{"ffffffff01000000", 0xff}, // u32 accumulation wraps before byte folding.
		{"ffffffffffffffff", 0xfa},
		{"313f080000000000000044332211", 0x21},
	} {
		b, _ := hex.DecodeString(item.hex)
		if got := Checksum(b, 0xff); got != item.want {
			t.Fatalf("%s checksum %x want %x", item.hex, got, item.want)
		}
	}
}

func TestTypedExtensionsAndExplicitBounds(t *testing.T) {
	for kind := byte(0); kind <= ACK; kind++ {
		p := Packet{Source: 0x31, Destination: 0x3f, Type: kind, Flags: FlagSize, Session: 7,
			Sequence: 65535, ConnectionSignature: 0x12345678, Fragment: 2, Payload: []byte{0, 0xff, 2}}
		b, err := p.Marshal(0xff)
		if err != nil {
			t.Fatal(err)
		}
		q, n, err := ParseFrame(append(b, 1, 2), 0xff)
		if err != nil || n != len(b) || q.Sequence != p.Sequence || !bytes.Equal(q.Payload, p.Payload) {
			t.Fatalf("kind %d: %+v %v", kind, q, err)
		}
		for size := 0; size < len(b); size++ {
			if _, _, err := ParseFrame(b[:size], 0xff); err == nil {
				t.Fatalf("kind %d accepts length %d", kind, size)
			}
		}
	}
}
