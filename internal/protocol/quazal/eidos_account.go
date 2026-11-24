package quazal

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// The 25 to Life method-13 capture and the Eidos DDL agree: country u32 in public;
// flags u8, postal-code String and 8-byte date in private. Each Data record has
// its own u32 body length inside the AnyData buffer.
func ValidateEidosAccountData(public, private AnyData) error {
	bad := errors.New("invalid Eidos account data")
	if public.Type != "EidosAccountPublic" || private.Type != "EidosAccountPrivate" {
		return bad
	}
	if len(public.Payload) != 8 || binary.LittleEndian.Uint32(public.Payload) != 4 {
		return bad
	}
	b := private.Payload
	if len(b) < 15 || uint64(binary.LittleEndian.Uint32(b)) != uint64(len(b)-4) {
		return bad
	}
	n := int(binary.LittleEndian.Uint16(b[5:]))
	if n > 256 || len(b) != 4+1+2+n+8 {
		return bad
	}
	postal := b[7 : 7+n]
	if n > 0 && (postal[n-1] != 0 || bytes.IndexByte(postal[:n-1], 0) >= 0) {
		return bad
	}
	return nil
}
