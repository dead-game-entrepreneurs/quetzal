package quazal

import "encoding/binary"

// Reader exposes the same bounded SDK primitives used by authentication to
// title adapters. Concrete Any bodies remain the adapter's responsibility.
type Reader struct{ reader }

func NewReader(data []byte) *Reader { return &Reader{reader{data: data}} }
func (r *Reader) Raw(n int) []byte  { return r.take(n) }
func (r *Reader) U32() uint32       { return r.u32() }
func (r *Reader) U16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}
func (r *Reader) U8() byte {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}
func (r *Reader) Bool() bool {
	b := r.U8()
	if b > 1 {
		r.err = ErrRMC
	}
	return b == 1
}
func (r *Reader) String(limit int) string { return r.str(limit) }
func (r *Reader) Buffer(limit int) []byte { return r.buffer(limit) }
func (r *Reader) Done() bool              { return r.done() }
func (r *Reader) Err() error              { return r.err }
func (r *Reader) Remaining() int          { return len(r.data) }
func (r *Reader) Any(limit int) AnyData {
	return AnyData{Type: r.str(128), Payload: r.buffer(limit)}
}

type Writer struct{ writer }

func (w *Writer) Raw(b []byte) { w.raw(b) }
func (w *Writer) U32(v uint32) { w.u32(v) }
func (w *Writer) U16(v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	w.raw(b[:])
}
func (w *Writer) U8(v byte) { w.raw([]byte{v}) }
func (w *Writer) Bool(v bool) {
	if v {
		w.U8(1)
	} else {
		w.U8(0)
	}
}
func (w *Writer) String(v string) { w.str(v) }
func (w *Writer) Buffer(v []byte) { w.buffer(v) }
func (w *Writer) Any(v AnyData) {
	w.str(v.Type)
	w.buffer(v.Payload)
}
func (w *Writer) Bytes() ([]byte, error) { return w.data, w.err }

// Gathering contains the common Rendez-Vous base fields. Some concrete titles
// wrap their entire Data body with another length; that is outside this codec.
type Gathering struct {
	ID, OwnerPID, HostPID                uint32
	MinParticipants, MaxParticipants     uint16
	Policy, PolicyArgument, Flags, State uint32
	Description                          string
}

func (r *Reader) Gathering() Gathering {
	return Gathering{ID: r.U32(), OwnerPID: r.U32(), HostPID: r.U32(),
		MinParticipants: r.U16(), MaxParticipants: r.U16(), Policy: r.U32(),
		PolicyArgument: r.U32(), Flags: r.U32(), State: r.U32(), Description: r.String(512)}
}
func (w *Writer) Gathering(g Gathering) {
	w.U32(g.ID)
	w.U32(g.OwnerPID)
	w.U32(g.HostPID)
	w.U16(g.MinParticipants)
	w.U16(g.MaxParticipants)
	w.U32(g.Policy)
	w.U32(g.PolicyArgument)
	w.U32(g.Flags)
	w.U32(g.State)
	w.String(g.Description)
}
