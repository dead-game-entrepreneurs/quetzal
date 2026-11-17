// Adapted from sibling tdu-server; title differences are documented in docs/games/25tolife/analysis.md.
package quazalcrypto

import (
	"encoding/binary"
	"math"
)

// Challenge is the 12-byte plaintext constructed by 0x1f3e30. The receiving SDK
// uses ConnectionID and Nonce; the ticket, rather than this field, proves PID.
type Challenge struct {
	PrincipalID  uint32
	ConnectionID uint32
	Nonce        uint32
}

func (c Challenge) MarshalBinary() ([]byte, error) {
	out := make([]byte, 12)
	binary.LittleEndian.PutUint32(out, c.PrincipalID)
	binary.LittleEndian.PutUint32(out[4:], c.ConnectionID)
	binary.LittleEndian.PutUint32(out[8:], c.Nonce)
	return out, nil
}

func ParseChallenge(data []byte) (Challenge, error) {
	if len(data) != 12 {
		return Challenge{}, ErrLength
	}
	return Challenge{
		PrincipalID:  binary.LittleEndian.Uint32(data),
		ConnectionID: binary.LittleEndian.Uint32(data[4:]),
		Nonce:        binary.LittleEndian.Uint32(data[8:]),
	}, nil
}

// ConnectRequest is the application payload inside secure PRUDP CONNECT. Both
// fields are independently u32-length-prefixed Buffers, not raw concatenation.
type ConnectRequest struct {
	Ticket             []byte
	EncryptedChallenge []byte
}

func (r ConnectRequest) MarshalBinary() ([]byte, error) {
	n := uint64(len(r.Ticket)) + uint64(len(r.EncryptedChallenge)) + 8
	if uint64(len(r.Ticket)) > math.MaxUint32 || uint64(len(r.EncryptedChallenge)) > math.MaxUint32 || n > uint64(int(^uint(0)>>1)) {
		return nil, ErrLength
	}
	out := make([]byte, int(n))
	binary.LittleEndian.PutUint32(out, uint32(len(r.Ticket)))
	copy(out[4:], r.Ticket)
	offset := 4 + len(r.Ticket)
	binary.LittleEndian.PutUint32(out[offset:], uint32(len(r.EncryptedChallenge)))
	copy(out[offset+4:], r.EncryptedChallenge)
	return out, nil
}

func readBuffer(data []byte) ([]byte, []byte, error) {
	if len(data) < 4 {
		return nil, nil, ErrLength
	}
	n := uint64(binary.LittleEndian.Uint32(data))
	if n > uint64(len(data)-4) {
		return nil, nil, ErrLength
	}
	end := 4 + int(n)
	return data[4:end], data[end:], nil
}

func ParseConnectRequest(data []byte) (ConnectRequest, error) {
	ticket, rest, err := readBuffer(data)
	if err != nil {
		return ConnectRequest{}, err
	}
	challenge, rest, err := readBuffer(rest)
	if err != nil || len(rest) != 0 {
		return ConnectRequest{}, ErrLength
	}
	return ConnectRequest{
		Ticket:             append([]byte(nil), ticket...),
		EncryptedChallenge: append([]byte(nil), challenge...),
	}, nil
}

// BuildConnect creates the exact two-Buffer payload emitted by 0x1f3e30.
// The caller supplies the fresh nonce and the established local connection ID.
func BuildConnect(ticket []byte, sessionKey Key, challenge Challenge) ([]byte, error) {
	plaintext, _ := challenge.MarshalBinary()
	return (ConnectRequest{Ticket: ticket, EncryptedChallenge: Seal(sessionKey, plaintext)}).MarshalBinary()
}

// OpenConnect authenticates both containers and parses their fixed layouts.
// It additionally requires the challenge's claimed PID to match the ticket PID.
// This consistency check is server policy; the retail receive helper trusts the
// authenticated ticket PID and does not itself compare the duplicate PID field.
func OpenConnect(destinationKey Key, wire []byte) (DestinationTicket, Challenge, error) {
	request, err := ParseConnectRequest(wire)
	if err != nil {
		return DestinationTicket{}, Challenge{}, err
	}
	plaintext, err := Open(destinationKey, request.Ticket)
	if err != nil {
		return DestinationTicket{}, Challenge{}, err
	}
	ticket, err := ParseDestinationTicket(plaintext)
	if err != nil {
		return DestinationTicket{}, Challenge{}, err
	}
	plaintext, err = Open(ticket.SessionKey, request.EncryptedChallenge)
	if err != nil {
		return DestinationTicket{}, Challenge{}, err
	}
	challenge, err := ParseChallenge(plaintext)
	if err != nil {
		return DestinationTicket{}, Challenge{}, err
	}
	if challenge.PrincipalID != ticket.PrincipalID {
		return DestinationTicket{}, Challenge{}, ErrPrincipal
	}
	return ticket, challenge, nil
}

// ConnectReply is a Buffer containing LE u32(nonce+1), including uint32 wrap.
// It is the application payload, before PRUDP framing/transport encoding.
func ConnectReply(nonce uint32) []byte {
	out := make([]byte, 8)
	binary.LittleEndian.PutUint32(out, 4)
	binary.LittleEndian.PutUint32(out[4:], nonce+1)
	return out
}

// VerifyConnectReply implements the nonce comparison in 0x1f42f0. The new server
// codec requires exactly eight bytes; the retail parser is more permissive about
// excess bytes inside/after Buffers. Neither failure nor replay policy is hidden.
func VerifyConnectReply(reply []byte, nonce uint32) error {
	if len(reply) != 8 || binary.LittleEndian.Uint32(reply) != 4 {
		return ErrLength
	}
	if binary.LittleEndian.Uint32(reply[4:]) != nonce+1 {
		return ErrChallenge
	}
	return nil
}
