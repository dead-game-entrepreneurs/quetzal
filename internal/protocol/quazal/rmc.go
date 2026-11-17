// Adapted from sibling tdu-server; title differences are documented in docs/games/25tolife/analysis.md.
package quazal

import (
	"bytes"
	"encoding/binary"
	"errors"
)

const (
	MaxMessageSize        = 64 * 1024
	Success        uint32 = 0x00010001
	GenericFailure uint32 = 0x80010002
)

var ErrRMC = errors.New("quazal: malformed RPC message")

// Request includes the four-byte Message length prefix, as emitted by 0x21aa80.
type Request struct {
	Protocol       byte
	CallID, Method uint32
	Args           []byte
}
type Response struct {
	Protocol                  byte
	Success                   bool
	CallID, Method, ErrorCode uint32
	Payload                   []byte
}

func validRecord(wire []byte) bool {
	return len(wire) >= 4 && len(wire) <= MaxMessageSize && uint64(binary.LittleEndian.Uint32(wire)) == uint64(len(wire)-4)
}
func ParseRequest(wire []byte) (Request, error) {
	if !validRecord(wire) || len(wire) < 13 || wire[4]&0x80 == 0 {
		return Request{}, ErrRMC
	}
	method := binary.LittleEndian.Uint32(wire[9:])
	if method == 0 || method >= 0x8000 {
		return Request{}, ErrRMC
	}
	return Request{Protocol: wire[4] & 0x7f, CallID: binary.LittleEndian.Uint32(wire[5:]), Method: method, Args: bytes.Clone(wire[13:])}, nil
}
func EncodeRequest(r Request) ([]byte, error) {
	if r.Protocol > 127 || r.Method == 0 || r.Method >= 0x8000 || len(r.Args) > MaxMessageSize-13 {
		return nil, ErrRMC
	}
	wire := make([]byte, 13+len(r.Args))
	binary.LittleEndian.PutUint32(wire, uint32(len(wire)-4))
	wire[4] = r.Protocol | 0x80
	binary.LittleEndian.PutUint32(wire[5:], r.CallID)
	binary.LittleEndian.PutUint32(wire[9:], r.Method)
	copy(wire[13:], r.Args)
	return wire, nil
}
func SuccessReply(r Request, payload []byte) ([]byte, error) {
	if len(payload) > MaxMessageSize-14 {
		return nil, ErrRMC
	}
	wire := make([]byte, 14+len(payload))
	binary.LittleEndian.PutUint32(wire, uint32(len(wire)-4))
	wire[4] = r.Protocol
	wire[5] = 1
	binary.LittleEndian.PutUint32(wire[6:], r.CallID)
	binary.LittleEndian.PutUint32(wire[10:], r.Method|0x8000)
	copy(wire[14:], payload)
	return wire, nil
}
func ErrorReply(r Request, code uint32) []byte {
	wire := make([]byte, 14)
	binary.LittleEndian.PutUint32(wire, 10)
	wire[4] = r.Protocol
	binary.LittleEndian.PutUint32(wire[6:], code|0x80000000)
	binary.LittleEndian.PutUint32(wire[10:], r.CallID)
	return wire
}
func ParseResponse(wire []byte) (Response, error) {
	if !validRecord(wire) || len(wire) < 10 || wire[4]&0x80 != 0 || wire[5] > 1 {
		return Response{}, ErrRMC
	}
	r := Response{Protocol: wire[4], Success: wire[5] == 1}
	if !r.Success {
		if len(wire) != 14 {
			return Response{}, ErrRMC
		}
		r.ErrorCode = binary.LittleEndian.Uint32(wire[6:])
		r.CallID = binary.LittleEndian.Uint32(wire[10:])
		return r, nil
	}
	if len(wire) < 14 {
		return Response{}, ErrRMC
	}
	r.CallID = binary.LittleEndian.Uint32(wire[6:])
	method := binary.LittleEndian.Uint32(wire[10:])
	if method&0x8000 == 0 {
		return Response{}, ErrRMC
	}
	r.Method = method &^ 0x8000
	r.Payload = bytes.Clone(wire[14:])
	return r, nil
}

type reader struct {
	data []byte
	err  error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > len(r.data) {
		r.err = ErrRMC
		return nil
	}
	out := r.data[:n]
	r.data = r.data[n:]
	return out
}
func (r *reader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
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
		r.err = ErrRMC
		return ""
	}
	b = r.take(n)
	if b == nil {
		return ""
	}
	if b[n-1] != 0 || bytes.IndexByte(b[:n-1], 0) >= 0 {
		r.err = ErrRMC
		return ""
	}
	return string(b[:n-1])
}
func (r *reader) buffer(max int) []byte {
	n := r.u32()
	if r.err != nil {
		return nil
	}
	if uint64(n) > uint64(max) {
		r.err = ErrRMC
		return nil
	}
	return r.take(int(n))
}
func (r *reader) done() bool { return r.err == nil && len(r.data) == 0 }

type writer struct {
	data []byte
	err  error
}

func (w *writer) raw(data []byte) {
	if w.err != nil {
		return
	}
	if len(data) > MaxMessageSize-len(w.data) {
		w.err = ErrRMC
		return
	}
	w.data = append(w.data, data...)
}
func (w *writer) u32(value uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], value)
	w.raw(b[:])
}
func (w *writer) str(value string) {
	if len(value) > 65534 || bytes.IndexByte([]byte(value), 0) >= 0 {
		w.err = ErrRMC
		return
	}
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], uint16(len(value)+1))
	w.raw(b[:])
	w.raw([]byte(value))
	w.raw([]byte{0})
}
func (w *writer) buffer(value []byte) {
	if len(value) > MaxMessageSize {
		w.err = ErrRMC
		return
	}
	w.u32(uint32(len(value)))
	w.raw(value)
}
