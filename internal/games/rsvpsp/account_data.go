package rsvpsp

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

var errUnsupported = errors.New("rsvpsp: unsupported title RPC")

const (
	publicType  = "AccountInfoPublicData"
	privateType = "AccountInfoPrivateData"
	// Longest text field accepted in a profile record, excluding the NUL.
	maxField = 255
)

// ValidateAccountData checks the Spark profile records sent with signup
// (AccountManagement 25/21). Each any<Data> buffer starts with its own u32
// body length, as in other titles on this SDK. Field writers:
//
//   - AccountInfoPublicData 0x08b9c638: String, bool, DateTime (u64), then
//     four Strings (the sign-up form's real name, gender, birthdate, country
//     and related fields; order not yet matched to the form).
//   - AccountInfoPrivateData 0x08b9bf58: two Strings (most likely the secret
//     question and answer).
func ValidateAccountData(public, private quazal.AnyData) error {
	if public.Type != publicType || private.Type != privateType {
		return fmt.Errorf("rsvpsp: unexpected profile record types %q and %q", public.Type, private.Type)
	}
	if err := checkRecord(public.Payload, "sbqssss"); err != nil {
		// The public record holds no secrets, so its bytes help the next field test.
		return fmt.Errorf("rsvpsp: %s %w (%d bytes: %s)", publicType, err, len(public.Payload), preview(public.Payload))
	}
	if err := checkRecord(private.Payload, "ss"); err != nil {
		// The private record likely holds the secret answer; report its size only.
		return fmt.Errorf("rsvpsp: %s %w (%d bytes)", privateType, err, len(private.Payload))
	}
	return nil
}

// checkRecord validates a length-prefixed Data body against a field layout:
// s String, b bool, q 8-byte integer.
func checkRecord(b []byte, layout string) error {
	if len(b) < 4 || uint64(binary.LittleEndian.Uint32(b)) != uint64(len(b)-4) {
		return errors.New("has a bad body length")
	}
	b = b[4:]
	for _, field := range layout {
		switch field {
		case 's':
			if len(b) < 2 {
				return errors.New("is truncated")
			}
			n := int(binary.LittleEndian.Uint16(b))
			b = b[2:]
			if n == 0 {
				continue
			}
			if n > maxField+1 || n > len(b) || b[n-1] != 0 {
				return errors.New("has a bad String")
			}
			for _, c := range b[:n-1] {
				if c == 0 {
					return errors.New("has a bad String")
				}
			}
			b = b[n:]
		case 'b':
			if len(b) < 1 || b[0] > 1 {
				return errors.New("has a bad bool")
			}
			b = b[1:]
		case 'q':
			if len(b) < 8 {
				return errors.New("is truncated")
			}
			b = b[8:]
		}
	}
	if len(b) != 0 {
		return errors.New("has trailing bytes")
	}
	return nil
}

func preview(b []byte) string {
	if len(b) > 128 {
		return hex.EncodeToString(b[:128]) + "…"
	}
	return hex.EncodeToString(b)
}
