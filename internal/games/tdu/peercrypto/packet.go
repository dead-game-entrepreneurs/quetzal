// Package peercrypto implements the US PS2 native peer packet and EncKey formats.
// The repeating-XOR packet checksum detects corruption; it is not a secure MAC.
package peercrypto

import (
	"encoding/binary"
	"errors"
)

type PacketKey [16]byte

const (
	CommandTicket uint16 = iota
	CommandAccepted
	CommandRejected
	CommandClose
	CommandData
	CommandReset
)

// MaxPayload is the established-data bound enforced by retail routine 0x152b90.
const MaxPayload = 0x545

var (
	ErrPacketLength    = errors.New("peer packet length out of bounds")
	ErrCommand         = errors.New("unknown peer command")
	ErrUnprotectedData = errors.New("peer data/close command requires XOR protection")
	ErrChecksum        = errors.New("peer packet XOR checksum mismatch")
)

type Packet struct {
	Flags   uint16
	Command uint16
	Payload []byte
}

// EncodePacket matches 0x152c80 after association: commands 3/4 are protected;
// handshake commands remain clear. Connection state is the caller's concern.
func EncodePacket(key PacketKey, command uint16, payload []byte) ([]byte, error) {
	if command > CommandReset {
		return nil, ErrCommand
	}
	if len(payload) > MaxPayload {
		return nil, ErrPacketLength
	}
	protected := command == CommandClose || command == CommandData
	n := 4 + len(payload)
	if protected {
		n++
	}
	wire := make([]byte, n)
	binary.BigEndian.PutUint16(wire[2:4], command)
	copy(wire[4:], payload)
	if protected {
		binary.BigEndian.PutUint16(wire[:2], 1)
		for _, b := range wire[:n-1] {
			wire[n-1] ^= b
		}
		for i := 2; i < n; i++ {
			wire[i] ^= key[i&15]
		}
	}
	return wire, nil
}

// DecodePacket matches 0x1520e0's wire protection checks without changing wire.
// Reserved flag bits are retained, as the retail protection path tests bit 0.
// Association state, source endpoint and command-specific bodies need separate
// validation before delivering Payload to a game/session handler.
func DecodePacket(key PacketKey, wire []byte) (Packet, error) {
	if len(wire) < 4 || len(wire) > MaxPayload+5 {
		return Packet{}, ErrPacketLength
	}
	flags := binary.BigEndian.Uint16(wire[:2])
	plain := append([]byte(nil), wire...)
	protected := flags&1 != 0
	if protected {
		if len(plain) < 5 {
			return Packet{}, ErrPacketLength
		}
		for i := 2; i < len(plain); i++ {
			plain[i] ^= key[i&15]
		}
		var checksum byte
		for _, b := range plain[:len(plain)-1] {
			checksum ^= b
		}
		if checksum != plain[len(plain)-1] {
			return Packet{}, ErrChecksum
		}
		plain = plain[:len(plain)-1]
	}
	if len(plain)-4 > MaxPayload {
		return Packet{}, ErrPacketLength
	}
	command := binary.BigEndian.Uint16(plain[2:4])
	if command > CommandReset {
		return Packet{}, ErrCommand
	}
	if !protected && (command == CommandClose || command == CommandData) {
		return Packet{}, ErrUnprotectedData
	}
	return Packet{Flags: flags, Command: command, Payload: plain[4:]}, nil
}
