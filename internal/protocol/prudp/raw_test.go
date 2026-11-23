package prudp

import (
	"bytes"
	"testing"
)

func TestRawPayloadPreservesBytesAndBounds(t *testing.T) {
	s := &Server{opts: Options{RawPayload: true, MaxMessageSize: 8}}
	for _, input := range [][]byte{nil, {0}, {1, 2, 3, 4}, {0xff, 0, 0x80, 0x81}} {
		encoded := s.encodePayload(input)
		decoded, err := s.decodePayload(encoded)
		if err != nil || !bytes.Equal(decoded, input) || !bytes.Equal(encoded, input) {
			t.Fatalf("raw payload altered: %x %v", decoded, err)
		}
		if len(decoded) > 0 {
			decoded[0] ^= 0xff
			if decoded[0] == encoded[0] {
				t.Fatal("decoded payload aliases wire buffer")
			}
		}
	}
	if _, err := s.decodePayload(make([]byte, 9)); err == nil {
		t.Fatal("raw payload exceeded message limit")
	}
	for _, opts := range []Options{{RawPayload: true, PlainPayload: true}, {RawPayload: true, OpaquePayload: true}, {PlainPayload: true, OpaquePayload: true}} {
		if _, err := New(opts); err == nil {
			t.Fatal("conflicting payload modes accepted")
		}
	}
}
