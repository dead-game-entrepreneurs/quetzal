// Package sessionmaster serves the recovered PS2 FreeRide registry. Claimed
// player identities are unverified; transport ownership grants no account rights.
package sessionmaster

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
)

const (
	GetTime           uint16 = 0x7426
	GetTimeReply      uint16 = 0x9f27
	CreateOrJoin      uint16 = 0xe2dd
	CreateOrJoinReply uint16 = 0x5ce5
	Update            uint16 = 0xa518
	GetInfo           uint16 = 0x6427
	GetInfoReply      uint16 = 0x7848
	Delete            uint16 = 0x586e
	Search            uint16 = 0xecd1
	SearchReply       uint16 = 0x4fe2
	Notify            uint16 = 0x8d1c
	FreeRideTag       uint16 = 0x70ce
	SessionClientTag  uint16 = 0xfae0
	BlacklistTag      uint16 = 0xdc69
)

var ErrMalformed = errors.New("malformed SessionMaster action")
var ErrUnsupported = errors.New("unsupported SessionMaster operation")
var ErrOwnership = errors.New("SessionMaster transport does not own record")
var ErrIdentity = errors.New("SessionMaster claimed connection CID already held by another transport")
var ErrCapacity = errors.New("SessionMaster registry capacity reached")

type Bounds struct{ MinX, MinZ, MaxX, MaxZ float32 }

func (b Bounds) valid() bool {
	return finite(b.MinX) && finite(b.MinZ) && finite(b.MaxX) && finite(b.MaxZ) && b.MinX <= b.MaxX && b.MinZ <= b.MaxZ
}
func (b Bounds) Overlaps(other Bounds) bool {
	return b.MinX <= other.MaxX && other.MinX <= b.MaxX && b.MinZ <= other.MaxZ && other.MinZ <= b.MaxZ
}
func finite(f float32) bool { return !math.IsNaN(float64(f)) && !math.IsInf(float64(f), 0) }

// FreeRide is the exact V3 descriptor, not the different edNetSessionV3 schema.
// Reference is canonical IPv4. Connection's four fields retain their recovered
// byte order and must not be substituted with guessed authentication values.
type FreeRide struct {
	Identity                   uint64
	ID, Reference              uint32
	Connection                 [4]uint32
	NAT                        byte
	Game, Name                 string
	Occupancy, Capacity, Flags byte
	Bounds                     Bounds
	Extra                      [128]byte
}

func (d FreeRide) validate() error {
	if !validString(d.Game, 32) || !validString(d.Name, 32) || d.Capacity < 1 || d.Capacity > 8 || d.Occupancy > d.Capacity || !d.Bounds.valid() {
		return ErrMalformed
	}
	return nil
}
func validString(s string, max int) bool {
	return len(s) > 0 && len(s) <= max && !strings.ContainsRune(s, 0)
}
func (d FreeRide) MarshalBinary() ([]byte, error) {
	if e := d.validate(); e != nil {
		return nil, e
	}
	w := writer{}
	w.u16(FreeRideTag)
	w.u64(d.Identity)
	w.u32(d.ID)
	w.u32(d.Reference)
	for _, v := range d.Connection {
		w.u32(v)
	}
	w.byte(d.NAT)
	w.blob([]byte(d.Game))
	w.blob([]byte(d.Name))
	w.byte(d.Occupancy)
	w.byte(d.Capacity)
	w.byte(d.Flags)
	for _, v := range []float32{d.Bounds.MinX, d.Bounds.MinZ, d.Bounds.MaxX, d.Bounds.MaxZ} {
		w.f32(v)
	}
	w.blob(d.Extra[:])
	return w.b, nil
}
func ParseFreeRide(b []byte) (FreeRide, error) {
	r := reader{b: b}
	d := FreeRide{}
	if r.u16() != FreeRideTag {
		return d, ErrMalformed
	}
	d.Identity = r.u64()
	d.ID = r.u32()
	d.Reference = r.u32()
	for i := range d.Connection {
		d.Connection[i] = r.u32()
	}
	d.NAT = r.byte()
	d.Game = string(r.blob(32))
	d.Name = string(r.blob(32))
	d.Occupancy = r.byte()
	d.Capacity = r.byte()
	d.Flags = r.byte()
	d.Bounds = Bounds{r.f32(), r.f32(), r.f32(), r.f32()}
	extra := r.blob(128)
	if len(extra) != 128 {
		return FreeRide{}, ErrMalformed
	}
	copy(d.Extra[:], extra)
	if !r.done() || d.validate() != nil {
		return FreeRide{}, ErrMalformed
	}
	return d, nil
}

// Client is edNetSessionClientV2, a different record from peercrypto.ClientDescriptor.
// ConnectionID is the self-claimed native Quazal connection registration CID. Optional Extension is raw PS2 bytes.
type Client struct {
	Name                    string
	Flags                   byte
	Address                 uint32
	Port                    uint16
	NAT                     byte
	ConnectionID            uint32
	Fields                  [4]uint32
	Reference               uint32
	ClientPort, ClientFlags uint16
	Extension               []byte
}

func (c Client) MarshalBinary() ([]byte, error) {
	if (len(c.Name) > 16 || strings.ContainsRune(c.Name, 0)) || (len(c.Extension) != 0 && len(c.Extension) != 44) {
		return nil, ErrMalformed
	}
	w := writer{}
	w.u16(SessionClientTag)
	w.blob([]byte(c.Name))
	w.byte(c.Flags)
	w.u32(c.Address)
	w.u16(c.Port)
	w.byte(c.NAT)
	w.u32(c.ConnectionID)
	for _, v := range c.Fields {
		w.u32(v)
	}
	w.u32(c.Reference)
	w.u16(c.ClientPort)
	w.u16(c.ClientFlags)
	if len(c.Extension) > 0 {
		w.blob(c.Extension)
	}
	return w.b, nil
}
func ParseClient(b []byte) (Client, error) {
	r := reader{b: b}
	c := Client{}
	if r.u16() != SessionClientTag {
		return c, ErrMalformed
	}
	c.Name = string(r.blob(16))
	c.Flags = r.byte()
	c.Address = r.u32()
	c.Port = r.u16()
	c.NAT = r.byte()
	c.ConnectionID = r.u32()
	for i := range c.Fields {
		c.Fields[i] = r.u32()
	}
	c.Reference = r.u32()
	c.ClientPort = r.u16()
	c.ClientFlags = r.u16()
	if r.pos < len(b) {
		c.Extension = append([]byte(nil), r.blob(44)...)
		if len(c.Extension) != 44 {
			return Client{}, ErrMalformed
		}
	}
	if !r.done() || (len(c.Name) > 16 || strings.ContainsRune(c.Name, 0)) {
		return Client{}, ErrMalformed
	}
	return c, nil
}

type Exclusion struct {
	ID      uint32
	Seconds float32
}
type CreateRequest struct {
	Client    Client
	Blacklist []Exclusion
	Session   FreeRide
	Mode      uint32
}

func ParseCreateRequest(b []byte) (CreateRequest, error) {
	r := reader{b: b}
	q := CreateRequest{}
	if r.u16() != CreateOrJoin {
		return q, ErrMalformed
	}
	var e error
	q.Client, e = ParseClient(r.blob(256))
	if e != nil {
		return q, e
	}
	bl := reader{b: r.blob(130)}
	if bl.u16() != BlacklistTag {
		return q, ErrMalformed
	}
	for bl.pos < len(bl.b) && bl.err == nil {
		if len(q.Blacklist) == 16 {
			return q, ErrMalformed
		}
		x := Exclusion{bl.u32(), bl.f32()}
		if !finite(x.Seconds) || x.Seconds < 0 || x.Seconds > 86400 {
			return q, ErrMalformed
		}
		q.Blacklist = append(q.Blacklist, x)
	}
	if !bl.done() {
		return q, ErrMalformed
	}
	q.Session, e = ParseFreeRide(r.blob(512))
	if e != nil {
		return q, e
	}
	q.Mode = r.u32()
	if !r.done() {
		return q, ErrMalformed
	}
	return q, nil
}
func (q CreateRequest) MarshalBinary() ([]byte, error) {
	c, e := q.Client.MarshalBinary()
	if e != nil {
		return nil, e
	}
	d, e := q.Session.MarshalBinary()
	if e != nil {
		return nil, e
	}
	if len(q.Blacklist) > 16 {
		return nil, ErrMalformed
	}
	bl := writer{}
	bl.u16(BlacklistTag)
	for _, x := range q.Blacklist {
		if !finite(x.Seconds) || x.Seconds < 0 || x.Seconds > 86400 {
			return nil, ErrMalformed
		}
		bl.u32(x.ID)
		bl.f32(x.Seconds)
	}
	w := writer{}
	w.u16(CreateOrJoin)
	w.blob(c)
	w.blob(bl.b)
	w.blob(d)
	w.u32(q.Mode)
	return w.b, nil
}
func MarshalCreateReply(result byte, d *FreeRide) ([]byte, error) {
	w := writer{}
	w.u16(CreateOrJoinReply)
	w.byte(result)
	if d != nil {
		b, e := d.MarshalBinary()
		if e != nil {
			return nil, e
		}
		w.blob(b)
	}
	return w.b, nil
}
func MarshalGetReply(d *FreeRide) ([]byte, error) {
	w := writer{}
	w.u16(GetInfoReply)
	if d == nil {
		w.u32(0)
		return w.b, nil
	}
	b, e := d.MarshalBinary()
	if e != nil {
		return nil, e
	}
	w.u32(d.ID)
	w.blob(b)
	return w.b, nil
}
func MarshalUpdate(d FreeRide) ([]byte, error) {
	b, e := d.MarshalBinary()
	if e != nil {
		return nil, e
	}
	w := writer{}
	w.u16(Update)
	w.blob(b)
	return w.b, nil
}
func MarshalID(action uint16, id uint32) []byte { w := writer{}; w.u16(action); w.u32(id); return w.b }
func marshalNotify(id uint32, event byte, reference uint32, port uint16) []byte {
	w := writer{}
	w.u16(Notify)
	w.u32(id)
	w.byte(event)
	w.u32(reference)
	w.u16(port)
	return w.b
}

type reader struct {
	b   []byte
	pos int
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || n < 0 || n > len(r.b)-r.pos {
		r.err = ErrMalformed
		return nil
	}
	b := r.b[r.pos : r.pos+n]
	r.pos += n
	return b
}
func (r *reader) byte() byte {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}
func (r *reader) u16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint16(b)
}
func (r *reader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}
func (r *reader) u64() uint64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}
func (r *reader) f32() float32 { return math.Float32frombits(r.u32()) }
func (r *reader) blob(max int) []byte {
	n := int(r.u16())
	if n > max {
		r.err = ErrMalformed
		return nil
	}
	return r.take(n)
}
func (r *reader) done() bool { return r.err == nil && r.pos == len(r.b) }

type writer struct{ b []byte }

func (w *writer) byte(v byte)   { w.b = append(w.b, v) }
func (w *writer) u16(v uint16)  { w.b = binary.BigEndian.AppendUint16(w.b, v) }
func (w *writer) u32(v uint32)  { w.b = binary.BigEndian.AppendUint32(w.b, v) }
func (w *writer) u64(v uint64)  { w.b = binary.BigEndian.AppendUint64(w.b, v) }
func (w *writer) f32(v float32) { w.u32(math.Float32bits(v)) }
func (w *writer) blob(v []byte) { w.u16(uint16(len(v))); w.b = append(w.b, v...) }
