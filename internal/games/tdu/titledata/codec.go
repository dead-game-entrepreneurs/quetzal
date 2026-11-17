// Package titledata implements the PS2 title's challenge and statistics records.
// Numeric fields without established game semantics retain their wire order.
package titledata

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
)

var ErrWire = errors.New("malformed TDU title data")

const MaxBlob = 48 * 1024

type EventStats struct {
	EventID uint64
	Name    string
	Values  [4]uint32
}

// DoubleBits preserves the three 64-bit fields exactly, including sentinel bits.
type GameStats struct {
	Name       string
	Values     [3]uint32
	DoubleBits [3]uint64
	Counter    uint32
}
type FileStats struct {
	ID                         uint64
	Category                   uint32
	Name, Description, Creator string
	Reserved                   [7]uint32
	Flag                       byte
}
type reader struct {
	b   []byte
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > len(r.b) {
		r.err = ErrWire
		return nil
	}
	b := r.b[:n]
	r.b = r.b[n:]
	return b
}
func (r *reader) u8() byte {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}
func (r *reader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}
func (r *reader) u64() uint64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}
func (r *reader) str(max int) string {
	b := r.take(2)
	if b == nil {
		return ""
	}
	n := int(binary.LittleEndian.Uint16(b))
	if n == 0 {
		return ""
	}
	if n > max+1 {
		r.err = ErrWire
		return ""
	}
	b = r.take(n)
	if b == nil {
		return ""
	}
	if b[n-1] != 0 || bytes.IndexByte(b[:n-1], 0) >= 0 {
		r.err = ErrWire
		return ""
	}
	return string(b[:n-1])
}
func (r *reader) blob() []byte {
	n := r.u32()
	if n > MaxBlob {
		r.err = ErrWire
		return nil
	}
	return append([]byte(nil), r.take(int(n))...)
}
func (r *reader) done() bool          { return r.err == nil && len(r.b) == 0 }
func put32(b []byte, v uint32) []byte { return binary.LittleEndian.AppendUint32(b, v) }
func put64(b []byte, v uint64) []byte { return binary.LittleEndian.AppendUint64(b, v) }
func putstr(b []byte, v string) []byte {
	b = binary.LittleEndian.AppendUint16(b, uint16(len(v)+1))
	b = append(b, []byte(v)...)
	return append(b, 0)
}
func putblob(b, v []byte) []byte      { return append(put32(b, uint32(len(v))), v...) }
func validstr(v string, max int) bool { return len(v) <= max && bytes.IndexByte([]byte(v), 0) < 0 }
func readEvent(r *reader) EventStats {
	d := EventStats{EventID: r.u64(), Name: r.str(15)}
	for i := range d.Values {
		d.Values[i] = r.u32()
	}
	return d
}
func readGame(r *reader) GameStats {
	d := GameStats{Name: r.str(15)}
	for i := range d.Values {
		d.Values[i] = r.u32()
	}
	d.DoubleBits[0] = r.u64()
	d.Counter = r.u32()
	d.DoubleBits[1] = r.u64()
	d.DoubleBits[2] = r.u64()
	return d
}
func ParseEvent(b []byte) (EventStats, error) {
	r := reader{b: b}
	d := readEvent(&r)
	if !r.done() {
		return EventStats{}, ErrWire
	}
	return d, nil
}
func ParseGame(b []byte) (GameStats, error) {
	r := reader{b: b}
	d := readGame(&r)
	if !r.done() {
		return GameStats{}, ErrWire
	}
	return d, nil
}
func (d EventStats) MarshalBinary() ([]byte, error) {
	if !validstr(d.Name, 15) {
		return nil, ErrWire
	}
	b := putstr(put64(nil, d.EventID), d.Name)
	for _, v := range d.Values {
		b = put32(b, v)
	}
	return b, nil
}
func (d GameStats) MarshalBinary() ([]byte, error) {
	if !validstr(d.Name, 15) {
		return nil, ErrWire
	}
	b := putstr(nil, d.Name)
	for _, v := range d.Values {
		b = put32(b, v)
	}
	b = put64(b, d.DoubleBits[0])
	b = put32(b, d.Counter)
	b = put64(b, d.DoubleBits[1])
	b = put64(b, d.DoubleBits[2])
	return b, nil
}
func (d FileStats) marshal() []byte {
	b := put64(nil, d.ID)
	b = put32(b, d.Category)
	b = putstr(b, d.Name)
	b = putstr(b, d.Description)
	b = putstr(b, d.Creator)
	for _, v := range d.Reserved {
		b = put32(b, v)
	}
	return append(b, d.Flag)
}
func (d GameStats) metric(selector uint32) float64 {
	switch selector {
	case 2:
		if d.Values[0] == ^uint32(0) {
			return math.Inf(-1)
		}
		return float64(d.Values[0])
	case 3:
		if d.Values[1] == ^uint32(0) {
			return math.Inf(-1)
		}
		return float64(d.Values[1])
	case 4:
		return float64(math.Float32frombits(d.Values[2]))
	case 5:
		return math.Float64frombits(d.DoubleBits[0])
	case 6:
		if d.Counter == ^uint32(0) {
			return math.Inf(-1)
		}
		return float64(d.Counter)
	case 7:
		return math.Float64frombits(d.DoubleBits[1])
	}
	return math.Inf(-1)
}
