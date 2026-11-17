package peercrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

const (
	TicketSize                 = 130
	TicketPlaintextSize        = 82
	DescriptorSize             = 56
	encKeyTag           uint16 = 0x33bc
	clientDescriptorTag uint16 = 0x269e
)

type TicketKey [32]byte

// DefaultTicketKey returns the build's protocol key, recovered from the identical
// constants at 0x4e6e80/0x4e6eb0 after their bytewise deobfuscation. This shared
// client-side key does not prove an authenticated central server issued a ticket.
func DefaultTicketKey() TicketKey {
	return TicketKey{
		0xf9, 0x85, 0x1f, 0x49, 0x1a, 0x8b, 0xd6, 0xb8,
		0xc7, 0xb2, 0x1e, 0xe9, 0x63, 0xa6, 0x58, 0x4e,
		0xa1, 0xd2, 0xc4, 0xcb, 0x4e, 0xda, 0x13, 0x0e,
		0xbc, 0x84, 0x4f, 0x9b, 0x2d, 0x61, 0xce, 0xcd,
	}
}

var (
	ErrTicketLength         = errors.New("EncKey ticket must be 130 bytes")
	ErrTicketAuthentication = errors.New("EncKey HMAC-SHA256 mismatch")
	ErrTicketFormat         = errors.New("invalid EncKey field layout")
	ErrDescriptor           = errors.New("unsupported edNet client descriptor")
	ErrRecipient            = errors.New("EncKey recipient does not match local Quazal connection ID")
	ErrSource               = errors.New("EncKey source does not match advertised peer address")
)

// ClientDescriptor is the fixed 56-byte V2 form carried in this build's ticket.
// Address is the canonical IPv4 integer (for example 192.0.2.1 = 0xc0000201).
// Extension is a raw PS2 memory record; its numeric fields are little-endian,
// while the descriptor's outer scalar fields are serialized big-endian.
type ClientDescriptor struct {
	Address   uint32
	Port      uint16
	Flags     uint16
	Extension [44]byte
}

// NewClientDescriptor builds the observed Quazal-aware extension, version 2.
// Other extension fields retain their retail zero-initialized values.
func NewClientDescriptor(address uint32, port, flags uint16, connectionID, localAddress uint32, localPort uint16, natType byte) ClientDescriptor {
	d := ClientDescriptor{Address: address, Port: port, Flags: flags}
	binary.LittleEndian.PutUint32(d.Extension[0:4], 2)
	binary.LittleEndian.PutUint32(d.Extension[4:8], connectionID)
	binary.LittleEndian.PutUint32(d.Extension[8:12], localAddress)
	binary.LittleEndian.PutUint16(d.Extension[12:14], localPort)
	d.Extension[14] = natType
	return d
}

func (d ClientDescriptor) Version() uint32      { return binary.LittleEndian.Uint32(d.Extension[0:4]) }
func (d ClientDescriptor) ConnectionID() uint32 { return binary.LittleEndian.Uint32(d.Extension[4:8]) }
func (d ClientDescriptor) LocalAddress() uint32 { return binary.LittleEndian.Uint32(d.Extension[8:12]) }
func (d ClientDescriptor) LocalPort() uint16    { return binary.LittleEndian.Uint16(d.Extension[12:14]) }
func (d ClientDescriptor) NATType() byte        { return d.Extension[14] }

func (d ClientDescriptor) marshal(dst []byte) {
	binary.BigEndian.PutUint16(dst[0:2], clientDescriptorTag)
	binary.BigEndian.PutUint32(dst[2:6], d.Address)
	binary.BigEndian.PutUint16(dst[6:8], d.Port)
	binary.BigEndian.PutUint16(dst[8:10], d.Flags)
	binary.BigEndian.PutUint16(dst[10:12], uint16(len(d.Extension)))
	copy(dst[12:], d.Extension[:])
}

// Ticket carries a random field, the intended receiving Quazal connection ID, and the
// initiating client's descriptor. Retail validates Random's length, not its value.
type Ticket struct {
	Random                [16]byte
	RecipientConnectionID uint32
	Descriptor            ClientDescriptor
}

// SealTicket matches native peer issuer 0x151c40 and overlay wrapper 0xac5440.
// IV must be fresh random data for real sends; it is explicit for reproducible
// validation. The 82-byte body and its 32-byte HMAC are encrypted with AES-CTR;
// the clear 16-byte IV follows. No padding, timestamp or server signature is added.
func SealTicket(key TicketKey, iv [16]byte, ticket Ticket) ([TicketSize]byte, error) {
	var wire [TicketSize]byte
	if ticket.Descriptor.Version() < 2 {
		return wire, ErrDescriptor
	}
	plain := make([]byte, TicketPlaintextSize+sha256.Size)
	binary.BigEndian.PutUint16(plain[0:2], encKeyTag)
	binary.BigEndian.PutUint16(plain[2:4], uint16(len(ticket.Random)))
	copy(plain[4:20], ticket.Random[:])
	binary.BigEndian.PutUint32(plain[20:24], ticket.RecipientConnectionID)
	binary.BigEndian.PutUint16(plain[24:26], DescriptorSize)
	ticket.Descriptor.marshal(plain[26:TicketPlaintextSize])
	mac := hmac.New(sha256.New, key[:])
	mac.Write(plain[:TicketPlaintextSize])
	copy(plain[TicketPlaintextSize:], mac.Sum(nil))
	block, _ := aes.NewCipher(key[:]) // The fixed-size key is always valid.
	cipher.NewCTR(block, iv[:]).XORKeyStream(wire[:len(plain)], plain)
	copy(wire[len(plain):], iv[:])
	return wire, nil
}

// OpenTicket authenticates before parsing and returns no plaintext on failure.
// ValidateBinding must then check the receiving identity and the source endpoint.
func OpenTicket(key TicketKey, wire []byte) (Ticket, error) {
	if len(wire) != TicketSize {
		return Ticket{}, ErrTicketLength
	}
	const encryptedSize = TicketPlaintextSize + sha256.Size
	plain := make([]byte, encryptedSize)
	block, _ := aes.NewCipher(key[:])
	cipher.NewCTR(block, wire[encryptedSize:]).XORKeyStream(plain, wire[:encryptedSize])
	mac := hmac.New(sha256.New, key[:])
	mac.Write(plain[:TicketPlaintextSize])
	if !hmac.Equal(mac.Sum(nil), plain[TicketPlaintextSize:]) {
		return Ticket{}, ErrTicketAuthentication
	}
	if binary.BigEndian.Uint16(plain[0:2]) != encKeyTag ||
		binary.BigEndian.Uint16(plain[2:4]) != 16 ||
		binary.BigEndian.Uint16(plain[24:26]) != DescriptorSize {
		return Ticket{}, ErrTicketFormat
	}
	desc := plain[26:TicketPlaintextSize]
	if binary.BigEndian.Uint16(desc[0:2]) != clientDescriptorTag ||
		binary.BigEndian.Uint16(desc[10:12]) != 44 {
		return Ticket{}, ErrDescriptor
	}
	var ticket Ticket
	copy(ticket.Random[:], plain[4:20])
	ticket.RecipientConnectionID = binary.BigEndian.Uint32(plain[20:24])
	ticket.Descriptor.Address = binary.BigEndian.Uint32(desc[2:6])
	ticket.Descriptor.Port = binary.BigEndian.Uint16(desc[6:8])
	ticket.Descriptor.Flags = binary.BigEndian.Uint16(desc[8:10])
	copy(ticket.Descriptor.Extension[:], desc[12:])
	if ticket.Descriptor.Version() < 2 {
		return Ticket{}, ErrDescriptor
	}
	return ticket, nil
}

// ValidateBinding covers 0x152460's receiving connection ID and source-address checks.
// SourceAddress uses the same canonical IPv4 integer as Descriptor.Address.
// Pending association, source port selection, NAT policy and replay/state checks
// belong to the caller; this shared-key ticket alone does not authorize a user.
func (ticket Ticket) ValidateBinding(recipientConnectionID, sourceAddress uint32) error {
	if ticket.RecipientConnectionID != recipientConnectionID {
		return ErrRecipient
	}
	if ticket.Descriptor.Version() < 2 {
		return ErrDescriptor
	}
	if sourceAddress != ticket.Descriptor.Address && sourceAddress != ticket.Descriptor.LocalAddress() {
		return ErrSource
	}
	return nil
}
