// Adapted from sibling tdu-server; title differences are documented in docs/games/25tolife/analysis.md.
package quazalcrypto

import (
	"encoding/binary"
	"math"
)

// DestinationTicket is the plaintext the receiving principal decrypts from the
// opaque ticket. 0x1f2250 reads PrincipalID and SessionKey; 0x1f7500 installs
// PrincipalID as the remote endpoint's principal. No lifetime field is encoded.
type DestinationTicket struct {
	PrincipalID uint32
	SessionKey  Key
}

func (t DestinationTicket) MarshalBinary() ([]byte, error) {
	out := make([]byte, 4+KeySize)
	binary.LittleEndian.PutUint32(out, t.PrincipalID)
	copy(out[4:], t.SessionKey[:])
	return out, nil
}

func ParseDestinationTicket(data []byte) (DestinationTicket, error) {
	var ticket DestinationTicket
	if len(data) != 4+KeySize {
		return ticket, ErrLength
	}
	ticket.PrincipalID = binary.LittleEndian.Uint32(data)
	copy(ticket.SessionKey[:], data[4:])
	return ticket, nil
}

// ServiceTicketEnvelope is the plaintext inside protocol 10 method 3's encrypted
// Buffer: session key, server PID, then a u32-sized opaque destination ticket.
// The client decrypts this envelope with its password-derived key; it forwards
// Ticket unchanged to the destination, which uses that destination's own key.
type ServiceTicketEnvelope struct {
	SessionKey Key
	ServerPID  uint32
	Ticket     []byte
}

func (t ServiceTicketEnvelope) MarshalBinary() ([]byte, error) {
	if uint64(len(t.Ticket)) > math.MaxUint32 || len(t.Ticket) > int(^uint(0)>>1)-24 {
		return nil, ErrLength
	}
	out := make([]byte, 24+len(t.Ticket))
	copy(out, t.SessionKey[:])
	binary.LittleEndian.PutUint32(out[16:], t.ServerPID)
	binary.LittleEndian.PutUint32(out[20:], uint32(len(t.Ticket)))
	copy(out[24:], t.Ticket)
	return out, nil
}

func ParseServiceTicketEnvelope(data []byte) (ServiceTicketEnvelope, error) {
	var ticket ServiceTicketEnvelope
	if len(data) < 24 || uint64(binary.LittleEndian.Uint32(data[20:])) != uint64(len(data)-24) {
		return ticket, ErrLength
	}
	copy(ticket.SessionKey[:], data[:16])
	ticket.ServerPID = binary.LittleEndian.Uint32(data[16:])
	ticket.Ticket = append([]byte(nil), data[24:]...)
	return ticket, nil
}

// IssueServiceTicket creates both authenticated layers. destinationKey belongs
// to the target principal; clientKey belongs to the requesting client. The caller
// supplies a fresh random SessionKey and owns PID/key lookup and ticket lifetime.
func IssueServiceTicket(clientKey, destinationKey Key, clientPID, serverPID uint32, sessionKey Key) ([]byte, error) {
	inner, _ := (DestinationTicket{PrincipalID: clientPID, SessionKey: sessionKey}).MarshalBinary()
	envelope := ServiceTicketEnvelope{
		SessionKey: sessionKey,
		ServerPID:  serverPID,
		Ticket:     Seal(destinationKey, inner),
	}
	plaintext, err := envelope.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return Seal(clientKey, plaintext), nil
}

// OpenServiceTicket validates and decodes the encrypted response Buffer body.
func OpenServiceTicket(clientKey Key, sealed []byte) (ServiceTicketEnvelope, error) {
	plaintext, err := Open(clientKey, sealed)
	if err != nil {
		return ServiceTicketEnvelope{}, err
	}
	return ParseServiceTicketEnvelope(plaintext)
}
