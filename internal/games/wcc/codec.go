package wcc

import (
	"encoding/binary"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// reader decodes the little-endian SDK primitives used by protocols 21/50.
// Strings are u16 length (including NUL) followed by bytes and a NUL.
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
func (r *reader) u16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}
func (r *reader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}
func (r *reader) str(limit int) string {
	n := int(r.u16())
	if r.err != nil {
		return ""
	}
	if n == 0 {
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
func (r *reader) ids(limit uint32) []uint32 {
	n := r.u32()
	if n > limit {
		r.err = quazal.ErrRMC
		return nil
	}
	ids := make([]uint32, 0, n)
	for range n {
		ids = append(ids, r.u32())
	}
	return ids
}
func (r *reader) any(limit int) quazal.AnyData {
	kind := r.str(64)
	n := r.u32()
	if r.err == nil && n > uint32(limit) {
		r.err = quazal.ErrRMC
	}
	payload := r.take(int(n))
	return quazal.AnyData{Type: kind, Payload: append([]byte(nil), payload...)}
}
func (r *reader) done() bool { return r.err == nil && len(r.b) == 0 }

func putU32(b []byte, v uint32) []byte { return binary.LittleEndian.AppendUint32(b, v) }
func putBool(b []byte, v bool) []byte {
	if v {
		return append(b, 1)
	}
	return append(b, 0)
}
func putString(b []byte, s string) []byte {
	b = binary.LittleEndian.AppendUint16(b, uint16(len(s)+1))
	b = append(b, s...)
	return append(b, 0)
}
func putAny(b []byte, data quazal.AnyData) []byte {
	b = putString(b, data.Type)
	b = putU32(b, uint32(len(data.Payload)))
	return append(b, data.Payload...)
}
