// Package ednet implements the recovered PS2 edNet action envelope and public,
// read-only service directory and resource operations. It authenticates no account.
package ednet

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	Call             uint16 = 0x509a
	CallUnreliable   uint16 = 0xd572
	Reply            uint16 = 0x984e
	Ack              uint16 = 0x08e7
	DirectoryRequest uint16 = 0xc7bc
	DirectoryReply   uint16 = 0xee87 // Also a peer physics hash: dispatch by service context.
	OpenRequest      uint16 = 0x3be2
	OpenReply        uint16 = 0xb715
	GetRequest       uint16 = 0xd976
	GetReply         uint16 = 0x49a3
	MaxFrame                = 1378 // Proxy's 1388-byte plaintext limit minus its ten-byte envelope.
)

var ErrMalformed = errors.New("malformed edNet action")
var ErrUnsupported = errors.New("unsupported edNet operation")

// Frame wraps a complete action including that action's own BEu16 signature.
// Timeout is the request's millisecond lifetime, not an authentication token.
type Frame struct {
	Kind               uint16
	RequestID, Timeout uint32
	Action             []byte
}

func ParseFrame(b []byte) (Frame, error) {
	var f Frame
	if len(b) < 2 || len(b) > MaxFrame {
		return f, ErrMalformed
	}
	f.Kind = binary.BigEndian.Uint16(b)
	pos := 2
	switch f.Kind {
	case Call:
		if len(b) < 12 {
			return f, ErrMalformed
		}
		f.RequestID = binary.BigEndian.Uint32(b[2:])
		f.Timeout = binary.BigEndian.Uint32(b[6:])
		pos = 10
	case Reply:
		if len(b) < 8 {
			return f, ErrMalformed
		}
		f.RequestID = binary.BigEndian.Uint32(b[2:])
		pos = 6
	case CallUnreliable:
	case Ack:
		if len(b) != 6 {
			return f, ErrMalformed
		}
		f.RequestID = binary.BigEndian.Uint32(b[2:])
		return f, nil
	default:
		return f, fmt.Errorf("%w: envelope %#04x", ErrUnsupported, f.Kind)
	}
	if len(b) < pos+2 {
		return f, ErrMalformed
	}
	n := int(binary.BigEndian.Uint16(b[pos:]))
	pos += 2
	if n != len(b)-pos || (n < 2 && !(f.Kind == Reply && n == 0)) {
		return f, ErrMalformed
	}
	f.Action = append([]byte(nil), b[pos:]...)
	return f, nil
}

func (f Frame) MarshalBinary() ([]byte, error) {
	var b []byte
	switch f.Kind {
	case Call:
		b = make([]byte, 12)
		binary.BigEndian.PutUint32(b[2:], f.RequestID)
		binary.BigEndian.PutUint32(b[6:], f.Timeout)
	case Reply:
		b = make([]byte, 8)
		binary.BigEndian.PutUint32(b[2:], f.RequestID)
	case CallUnreliable:
		b = make([]byte, 4)
	case Ack:
		if len(f.Action) != 0 {
			return nil, ErrMalformed
		}
		b = make([]byte, 6)
		binary.BigEndian.PutUint16(b, f.Kind)
		binary.BigEndian.PutUint32(b[2:], f.RequestID)
		return b, nil
	default:
		return nil, ErrUnsupported
	}
	if (len(f.Action) < 2 && !(f.Kind == Reply && len(f.Action) == 0)) || len(b)+len(f.Action) > MaxFrame {
		return nil, ErrMalformed
	}
	binary.BigEndian.PutUint16(b, f.Kind)
	binary.BigEndian.PutUint16(b[len(b)-2:], uint16(len(f.Action)))
	return append(b, f.Action...), nil
}
