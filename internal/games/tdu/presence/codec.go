// Package presence implements the recovered US PS2 GPS tracker and USERMAP
// messages. Claimed player IDs are display metadata, not account identities.
package presence

import (
	"encoding/binary"
	"errors"
	"math"
)

const (
	TrackRequestID uint16  = 0x42bb
	TrackReplyID   uint16  = 0xc4d5
	MapRequestID   uint16  = 0x55ae
	MapReplyID     uint16  = 0xd768
	UpdateID       uint16  = 0x89ee
	Columns                = 41
	Rows                   = 33
	Cells                  = Columns * Rows
	StockWidth     float32 = 67584
	StockDepth     float32 = 55296
)

var (
	ErrMalformed   = errors.New("presence: malformed action")
	ErrUnsupported = errors.New("presence: unsupported operation")
	ErrOwnership   = errors.New("presence: activated transport required")
	ErrCapacity    = errors.New("presence: player capacity reached")
)

type Position struct{ X, Y, Z float32 }
type Update struct {
	UserID, TeamID uint32
	Name           string
	ClientType     uint16
	Position       Position
	Level, CarType uint32
	GameID         uint32
	GameType       int32
	CarMask        uint64
	ExtraIdentity  uint32
}
type TrackRequest struct {
	UserID, ClientType, GameID, Limit uint32
	Position                          Position
	CarMask                           uint64
	NATType                           uint32
	Friends                           []uint32
}
type Nearby struct {
	Position         Position
	Name             string
	GameID           uint32
	GameType         int32
	ClientType       uint16
	Level            byte
	CarMask          uint64
	CarType, NATType byte
}

type reader struct {
	b   []byte
	p   int
	bad bool
}

func (r *reader) take(n int) []byte {
	if n < 0 || n > len(r.b)-r.p {
		r.bad = true
		return make([]byte, n)
	}
	b := r.b[r.p : r.p+n]
	r.p += n
	return b
}
func (r *reader) u8() byte     { return r.take(1)[0] }
func (r *reader) u16() uint16  { return binary.BigEndian.Uint16(r.take(2)) }
func (r *reader) u32() uint32  { return binary.BigEndian.Uint32(r.take(4)) }
func (r *reader) u64() uint64  { return binary.BigEndian.Uint64(r.take(8)) }
func (r *reader) f32() float32 { return math.Float32frombits(r.u32()) }
func (r *reader) text() string {
	n := int(r.u16())
	if n > 31 {
		r.bad = true
		return ""
	}
	return string(r.take(n))
}
func (r *reader) done() bool { return !r.bad && r.p == len(r.b) }
func validPosition(p Position) bool {
	for _, v := range []float32{p.X, p.Y, p.Z} {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || math.Abs(float64(v)) > 1e7 {
			return false
		}
	}
	return true
}
func validName(s string) bool {
	if len(s) > 31 {
		return false
	}
	for i := range len(s) {
		if s[i] < 32 || s[i] == 127 {
			return false
		}
	}
	return true
}
func ParseUpdate(b []byte) (Update, error) {
	var v Update
	r := reader{b: b}
	if r.u16() != UpdateID {
		return v, ErrMalformed
	}
	v.UserID = r.u32()
	v.TeamID = r.u32()
	v.Name = r.text()
	v.ClientType = r.u16()
	v.Position.X = r.f32()
	v.Position.Z = r.f32()
	v.Level = r.u32()
	v.CarType = r.u32()
	v.GameID = r.u32()
	v.GameType = int32(r.u32())
	v.CarMask = r.u64()
	v.ExtraIdentity = r.u32()
	v.Position.Y = r.f32()
	if !r.done() || !validPosition(v.Position) || !validName(v.Name) {
		return Update{}, ErrMalformed
	}
	return v, nil
}
func ParseTrackRequest(b []byte) (TrackRequest, error) {
	var v TrackRequest
	if len(b) < 42 || len(b) > 442 || (len(b)-42)%4 != 0 {
		return v, ErrMalformed
	}
	r := reader{b: b}
	if r.u16() != TrackRequestID {
		return v, ErrMalformed
	}
	v.UserID = r.u32()
	v.ClientType = r.u32()
	v.GameID = r.u32()
	v.Limit = r.u32()
	v.Position = Position{r.f32(), r.f32(), r.f32()}
	v.CarMask = r.u64()
	v.NATType = r.u32()
	for r.p < len(b) {
		v.Friends = append(v.Friends, r.u32())
	}
	if !r.done() || v.Limit == 0 || v.Limit > 16 || v.ClientType > 65535 || v.NATType > 255 || !validPosition(v.Position) {
		return TrackRequest{}, ErrMalformed
	}
	return v, nil
}
func ParseMapRequest(b []byte) (level, clientType byte, err error) {
	if len(b) != 4 || binary.BigEndian.Uint16(b) != MapRequestID {
		return 0, 0, ErrMalformed
	}
	return b[2], b[3], nil
}
func u16(b []byte, v uint16) []byte  { return binary.BigEndian.AppendUint16(b, v) }
func u32(b []byte, v uint32) []byte  { return binary.BigEndian.AppendUint32(b, v) }
func f32(b []byte, v float32) []byte { return u32(b, math.Float32bits(v)) }
func text(b []byte, s string) []byte { return append(u16(b, uint16(len(s))), s...) }
func MarshalUpdate(v Update) ([]byte, error) {
	if !validPosition(v.Position) || !validName(v.Name) {
		return nil, ErrMalformed
	}
	b := u16(nil, UpdateID)
	b = u32(b, v.UserID)
	b = u32(b, v.TeamID)
	b = text(b, v.Name)
	b = u16(b, v.ClientType)
	b = f32(b, v.Position.X)
	b = f32(b, v.Position.Z)
	b = u32(b, v.Level)
	b = u32(b, v.CarType)
	b = u32(b, v.GameID)
	b = u32(b, uint32(v.GameType))
	b = binary.BigEndian.AppendUint64(b, v.CarMask)
	b = u32(b, v.ExtraIdentity)
	return f32(b, v.Position.Y), nil
}
func MarshalTrackRequest(v TrackRequest) ([]byte, error) {
	if v.Limit == 0 || v.Limit > 16 || v.ClientType > 65535 || len(v.Friends) > 100 || v.NATType > 255 || !validPosition(v.Position) {
		return nil, ErrMalformed
	}
	b := u16(nil, TrackRequestID)
	for _, n := range []uint32{v.UserID, v.ClientType, v.GameID, v.Limit} {
		b = u32(b, n)
	}
	b = f32(b, v.Position.X)
	b = f32(b, v.Position.Y)
	b = f32(b, v.Position.Z)
	b = binary.BigEndian.AppendUint64(b, v.CarMask)
	b = u32(b, v.NATType)
	for _, n := range v.Friends {
		b = u32(b, n)
	}
	return b, nil
}
func MarshalNearby(rows []Nearby, total uint32) ([]byte, error) {
	if len(rows) > 16 {
		return nil, ErrMalformed
	}
	b := append(u16(nil, TrackReplyID), byte(len(rows)))
	for _, v := range rows {
		if !validPosition(v.Position) || !validName(v.Name) {
			return nil, ErrMalformed
		}
		name := v.Name
		if len(name) > 16 {
			name = name[:16]
		}
		b = f32(b, v.Position.X)
		b = f32(b, v.Position.Y)
		b = f32(b, v.Position.Z)
		b = text(b, name)
		b = u32(b, v.GameID)
		b = u32(b, uint32(v.GameType))
		b = u16(b, v.ClientType)
		b = append(b, v.Level)
		b = binary.BigEndian.AppendUint64(b, v.CarMask)
		b = append(b, v.CarType, v.NATType)
	}
	return u32(b, total), nil
}
func MarshalMap(grid [Cells]byte, total uint32) []byte {
	b := u16(nil, MapReplyID)
	b = u16(b, Cells)
	b = append(b, grid[:]...)
	return u32(b, total)
}
