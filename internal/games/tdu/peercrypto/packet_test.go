package peercrypto

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
)

func packetTestKey() (key PacketKey) {
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	b, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPacketFixtureAndKeyIndexWrap(t *testing.T) {
	payload := mustHex(t, "123456789abcdef0010203040506070809")
	// Independently calculated from the clear BE header and absolute i&15 key
	// indexing. The payload crosses a key-period boundary and includes zero.
	want := mustHex(t, "000102071631507f92b5d4fb0d0f0d0b0507050b0d01")
	wire, err := EncodePacket(packetTestKey(), CommandData, payload)
	if err != nil || !bytes.Equal(wire, want) {
		t.Fatalf("wire=%x err=%v", wire, err)
	}
	saved := bytes.Clone(wire)
	packet, err := DecodePacket(packetTestKey(), wire)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Flags != 1 || packet.Command != CommandData || !bytes.Equal(packet.Payload, payload) {
		t.Fatalf("decoded %+v", packet)
	}
	if !bytes.Equal(wire, saved) {
		t.Fatal("decoder changed caller's wire buffer")
	}
	packet.Payload[0] ^= 1
	if !bytes.Equal(wire, saved) {
		t.Fatal("returned payload aliases wire")
	}
}

func TestPacketControlAndEstablishedCommands(t *testing.T) {
	for command := CommandTicket; command <= CommandReset; command++ {
		payload := []byte{0x10, 0x20, 0x30}
		wire, err := EncodePacket(packetTestKey(), command, payload)
		if err != nil {
			t.Fatal(err)
		}
		packet, err := DecodePacket(packetTestKey(), wire)
		if err != nil || packet.Command != command || !bytes.Equal(packet.Payload, payload) {
			t.Fatalf("command=%d packet=%+v err=%v", command, packet, err)
		}
		if command != CommandClose && command != CommandData {
			if !bytes.Equal(wire, []byte{0, 0, 0, byte(command), 0x10, 0x20, 0x30}) {
				t.Fatalf("clear command %d: %x", command, wire)
			}
		}
	}
}

func TestPacketRejectsTamperAndMalformedInput(t *testing.T) {
	wire, _ := EncodePacket(packetTestKey(), CommandData, []byte{1, 2, 3, 4, 5})
	for i := range wire {
		broken := bytes.Clone(wire)
		broken[i] ^= 0x80
		if _, err := DecodePacket(packetTestKey(), broken); err == nil {
			t.Fatalf("accepted bit flip at %d", i)
		}
	}
	for _, wire := range [][]byte{nil, {0}, {0, 0}, {0, 0, 0}, {0, 1, 0, 4}} {
		if _, err := DecodePacket(packetTestKey(), wire); !errors.Is(err, ErrPacketLength) {
			t.Fatalf("wire=%x err=%v", wire, err)
		}
	}
	for _, command := range []byte{3, 4} {
		if _, err := DecodePacket(packetTestKey(), []byte{0, 0, 0, command}); !errors.Is(err, ErrUnprotectedData) {
			t.Fatalf("command=%d err=%v", command, err)
		}
	}
	if _, err := EncodePacket(packetTestKey(), 6, nil); !errors.Is(err, ErrCommand) {
		t.Fatal(err)
	}
	if _, err := DecodePacket(packetTestKey(), []byte{0, 0, 0, 6}); !errors.Is(err, ErrCommand) {
		t.Fatal(err)
	}
}

func TestPacketLimits(t *testing.T) {
	for _, command := range []uint16{CommandTicket, CommandData} {
		wire, err := EncodePacket(packetTestKey(), command, make([]byte, MaxPayload))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodePacket(packetTestKey(), wire); err != nil {
			t.Fatal(err)
		}
		if _, err := EncodePacket(packetTestKey(), command, make([]byte, MaxPayload+1)); !errors.Is(err, ErrPacketLength) {
			t.Fatal(err)
		}
	}
	if _, err := DecodePacket(packetTestKey(), make([]byte, MaxPayload+6)); !errors.Is(err, ErrPacketLength) {
		t.Fatal(err)
	}
}

func TestPacketChecksumIsNotAuthentication(t *testing.T) {
	// Two equal bit flips cancel in the retail XOR checksum. Preserve this
	// behavior explicitly; callers must never treat it as a cryptographic MAC.
	payload := []byte{1, 2, 3, 4}
	wire, _ := EncodePacket(packetTestKey(), CommandData, payload)
	wire[4] ^= 0x40
	wire[5] ^= 0x40
	packet, err := DecodePacket(packetTestKey(), wire)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(packet.Payload, payload) {
		t.Fatal("tampering should change the payload")
	}
}

// These are synthetic vectors produced by executing the retail sender, with
// only memcpy/sendto intercepted and scoped EE low-word ABI adaptations.
func TestPacketNativeSenderVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/native-packets.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		KeyHex  string `json:"key_hex"`
		Vectors []struct {
			Command    uint16 `json:"command"`
			PayloadHex string `json:"payload_hex"`
			WireHex    string `json:"wire_hex"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	keyBytes := mustHex(t, fixture.KeyHex)
	if len(keyBytes) != 16 {
		t.Fatal("fixture packet key must be 16 bytes")
	}
	var key PacketKey
	copy(key[:], keyBytes)
	if len(fixture.Vectors) != 54 {
		t.Fatalf("unexpected vector count: %d", len(fixture.Vectors))
	}
	for i, vector := range fixture.Vectors {
		t.Run(fmt.Sprintf("vector_%02d_command_%d", i, vector.Command), func(t *testing.T) {
			payload := mustHex(t, vector.PayloadHex)
			want := mustHex(t, vector.WireHex)
			wire, err := EncodePacket(key, vector.Command, payload)
			if err != nil || !bytes.Equal(wire, want) {
				t.Fatalf("retail sender mismatch: wire=%x want=%x err=%v", wire, want, err)
			}
			packet, err := DecodePacket(key, want)
			if err != nil || packet.Command != vector.Command || !bytes.Equal(packet.Payload, payload) {
				t.Fatalf("retail vector decode mismatch: packet=%+v err=%v", packet, err)
			}
		})
	}
}
