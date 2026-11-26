package rsvpsp

import (
	"encoding/binary"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// reader decodes the little-endian SDK primitives. Strings are a u16 length
// (including the NUL) followed by the bytes and the NUL.
type reader struct {
	b   []byte
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || n < 0 || n > len(r.b) {
		r.err = quazal.ErrRMC
		return nil
	}
	b := r.b[:n]
	r.b = r.b[n:]
	return b
}

func (r *reader) u8() byte {
	if b := r.take(1); b != nil {
		return b[0]
	}
	return 0
}

func (r *reader) bool() bool {
	v := r.u8()
	if v > 1 {
		r.err = quazal.ErrRMC
	}
	return v == 1
}

func (r *reader) u16() uint16 {
	if b := r.take(2); b != nil {
		return binary.LittleEndian.Uint16(b)
	}
	return 0
}

func (r *reader) u32() uint32 {
	if b := r.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

func (r *reader) str(limit int) string {
	n := int(r.u16())
	if r.err != nil || n == 0 {
		return ""
	}
	if n > limit+1 {
		r.err = quazal.ErrRMC
		return ""
	}
	b := r.take(n)
	if b == nil || b[n-1] != 0 {
		r.err = quazal.ErrRMC
		return ""
	}
	for _, c := range b[:n-1] {
		if c == 0 {
			r.err = quazal.ErrRMC
			return ""
		}
	}
	return string(b[:n-1])
}

func (r *reader) done() bool { return r.err == nil && len(r.b) == 0 }

func putU32(b []byte, v uint32) []byte { return binary.LittleEndian.AppendUint32(b, v) }

func putString(b []byte, v string) []byte {
	if v == "" {
		return append(b, 0, 0)
	}
	b = binary.LittleEndian.AppendUint16(b, uint16(len(v)+1))
	return append(append(b, v...), 0)
}

func putAny(b []byte, data quazal.AnyData) []byte {
	b = putString(b, data.Type)
	b = putU32(b, uint32(len(data.Payload)))
	return append(b, data.Payload...)
}
