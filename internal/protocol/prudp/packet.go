// Adapted from sibling tdu-server/internal/prudp/packet.go.
// Layout cross-checked against 25 to Life SLUS-210.16 reader/writer.
package prudp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	SYN byte = iota
	CONNECT
	DATA
	DISCONNECT
	PING
	USER
	ACK
)
const (
	FlagAck      byte = 0x08
	FlagReliable byte = 0x10
	FlagNeedsAck byte = 0x20
	FlagSize     byte = 0x40
)

var ErrPacket = errors.New("invalid PRUDP packet")

// Packet contains the wire payload, before decryption/decompression.
// Flags excludes the low three type bits. ConnectionSignature is present only
// on SYN/CONNECT; Fragment is present only on DATA.
type Packet struct {
	Source, Destination, Type, Flags, Session byte
	Signature                                 uint32
	Sequence                                  uint16
	ConnectionSignature                       uint32
	Fragment                                  byte
	Payload                                   []byte
}

// Checksum matches TDU 0x235210 and 25 to Life 0x274a88.
// Carry between words is discarded; the seed belongs to the game/stream.
func Checksum(b []byte, seed byte) byte {
	var words uint32
	i := 0
	for ; i+4 <= len(b); i += 4 {
		words += binary.LittleEndian.Uint32(b[i:])
	}
	sum := uint32(seed) + uint32(byte(words)) + uint32(byte(words>>8)) + uint32(byte(words>>16)) + uint32(byte(words>>24))
	for ; i < len(b); i++ {
		sum += uint32(b[i])
	}
	return byte(sum)
}

func (p Packet) Marshal(seed byte) ([]byte, error) {
	if p.Type > ACK || p.Flags&7 != 0 || p.Source>>4 >= 8 || p.Destination>>4 >= 8 || len(p.Payload) > 65507-17 {
		return nil, ErrPacket
	}
	n := 10
	if p.Type == SYN || p.Type == CONNECT {
		n += 4
	} else if p.Type == DATA {
		n++
	}
	if p.Flags&FlagSize != 0 {
		n += 2
	}
	b := make([]byte, n+len(p.Payload)+1)
	b[0], b[1], b[2], b[3] = p.Source, p.Destination, p.Type|p.Flags, p.Session
	binary.LittleEndian.PutUint32(b[4:], p.Signature)
	binary.LittleEndian.PutUint16(b[8:], p.Sequence)
	i := 10
	if p.Type == SYN || p.Type == CONNECT {
		binary.LittleEndian.PutUint32(b[i:], p.ConnectionSignature)
		i += 4
	} else if p.Type == DATA {
		b[i] = p.Fragment
		i++
	}
	if p.Flags&FlagSize != 0 {
		binary.LittleEndian.PutUint16(b[i:], uint16(len(p.Payload)))
		i += 2
	}
	copy(b[i:], p.Payload)
	b[len(b)-1] = Checksum(b[:len(b)-1], seed)
	return b, nil
}

// ParseFrame returns one frame and the consumed byte count. The explicit-size
// flag permits several frames in a datagram; without it the frame uses the rest.
func ParseFrame(b []byte, seed byte) (Packet, int, error) {
	if len(b) < 11 || len(b) > 65507 {
		return Packet{}, 0, ErrPacket
	}
	p := Packet{Source: b[0], Destination: b[1], Type: b[2] & 7, Flags: b[2] & 0xf8, Session: b[3], Signature: binary.LittleEndian.Uint32(b[4:]), Sequence: binary.LittleEndian.Uint16(b[8:])}
	if p.Type > ACK || p.Source>>4 >= 8 || p.Destination>>4 >= 8 {
		return Packet{}, 0, ErrPacket
	}
	i := 10
	if p.Type == SYN || p.Type == CONNECT {
		if i+4 >= len(b) {
			return Packet{}, 0, ErrPacket
		}
		p.ConnectionSignature = binary.LittleEndian.Uint32(b[i:])
		i += 4
	} else if p.Type == DATA {
		if i+1 >= len(b) {
			return Packet{}, 0, ErrPacket
		}
		p.Fragment = b[i]
		i++
	}
	end := len(b)
	if p.Flags&FlagSize != 0 {
		if i+2 >= len(b) {
			return Packet{}, 0, ErrPacket
		}
		n := int(binary.LittleEndian.Uint16(b[i:]))
		i += 2
		end = i + n + 1
		if end > len(b) {
			return Packet{}, 0, ErrPacket
		}
	}
	if Checksum(b[:end-1], seed) != b[end-1] {
		return Packet{}, 0, fmt.Errorf("%w: checksum", ErrPacket)
	}
	p.Payload = append([]byte(nil), b[i:end-1]...)
	return p, end, nil
}
