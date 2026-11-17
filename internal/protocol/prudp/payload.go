// TDU-derived LZO codec. 25 to Life selects the plain wrapper; TDU's optional
// transport RC4 layer is separate from ticket/challenge crypto.
package prudp

import (
	"crypto/rc4"
	"errors"
)

var errCompression = errors.New("invalid or oversized PRUDP compression stream")

// The title installs the same default RC4 object for stream types 2 and 3.
// Retail 0x231820 restores the saved S-box before every payload transform.
func cryptPayload(b []byte) []byte {
	c, _ := rc4.NewCipher([]byte("CD&ML"))
	out := make([]byte, len(b))
	c.XORKeyStream(out, b)
	return out
}
func encodePayload(b []byte) []byte {
	return cryptPayload(encodePlainPayload(b))
}
func encodePlainPayload(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	// The retail decoder accepts selector zero. Compression is optional per frame.
	plain := make([]byte, len(b)+1)
	copy(plain[1:], b)
	return plain
}
func decodePayload(b []byte, limit int) ([]byte, error) {
	return decodePlainPayload(cryptPayload(b), limit)
}
func decodePlainPayload(b []byte, limit int) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	plain := b
	switch plain[0] {
	case 0:
		if len(plain)-1 > limit {
			return nil, errCompression
		}
		return plain[1:], nil
	case 1:
		return decodeLZO1X(plain[1:], limit)
	default:
		return nil, errCompression
	}
}

// decodeLZO1X implements the LZO1X token grammar used by retail 0x220790,
// with explicit input, output, back-reference, and complete-consumption checks.
// No compressor is required: a sender can choose the raw wrapper selector.
func decodeLZO1X(in []byte, limit int) ([]byte, error) {
	if limit < 0 || len(in) == 0 {
		return nil, errCompression
	}
	out := make([]byte, 0, min(limit, 4096))
	ip, t, n, off, a, b, trail := 0, 0, 0, 0, 0, 0, 0
	read := func() (int, error) {
		if ip >= len(in) {
			return 0, errCompression
		}
		v := int(in[ip])
		ip++
		return v, nil
	}
	literal := func(n int) error {
		if n < 0 || n > len(in)-ip || n > limit-len(out) {
			return errCompression
		}
		out = append(out, in[ip:ip+n]...)
		ip += n
		return nil
	}
	match := func(off, n int) error {
		if off <= 0 || off > len(out) || n < 0 || n > limit-len(out) {
			return errCompression
		}
		for j := 0; j < n; j++ {
			out = append(out, out[len(out)-off])
		}
		return nil
	}
	extend := func(base int) (int, error) {
		n := base
		for {
			v, e := read()
			if e != nil {
				return 0, e
			}
			if v != 0 {
				return n + v, nil
			}
			if n > limit {
				return 0, errCompression
			}
			n += 255
		}
	}
	var err error
	if in[0] > 17 {
		t = int(in[0]) - 17
		ip++
		if err = literal(t); err != nil {
			return nil, err
		}
		if t < 4 {
			goto nextMatch
		}
		goto firstMatch
	}
main:
	t, err = read()
	if err != nil {
		return nil, err
	}
	if t >= 16 {
		goto token
	}
	if t == 0 {
		t, err = extend(15)
		if err != nil {
			return nil, err
		}
	}
	if err = literal(t + 3); err != nil {
		return nil, err
	}
firstMatch:
	t, err = read()
	if err != nil {
		return nil, err
	}
	if t >= 16 {
		goto token
	}
	a, err = read()
	if err != nil {
		return nil, err
	}
	off = 2049 + (t >> 2) + (a << 2)
	if err = match(off, 3); err != nil {
		return nil, err
	}
	goto done
nextMatch:
	t, err = read()
	if err != nil {
		return nil, err
	}
token:
	switch {
	case t >= 64:
		a, err = read()
		if err != nil {
			return nil, err
		}
		off = 1 + ((t >> 2) & 7) + (a << 3)
		n = (t >> 5) + 1
	case t >= 32:
		n = t & 31
		if n == 0 {
			n, err = extend(31)
			if err != nil {
				return nil, err
			}
		}
		a, err = read()
		if err != nil {
			return nil, err
		}
		b, err = read()
		if err != nil {
			return nil, err
		}
		off = 1 + (a >> 2) + (b << 6)
		n += 2
	case t >= 16:
		n = t & 7
		if n == 0 {
			n, err = extend(7)
			if err != nil {
				return nil, err
			}
		}
		a, err = read()
		if err != nil {
			return nil, err
		}
		b, err = read()
		if err != nil {
			return nil, err
		}
		off = ((t & 8) << 11) + (a >> 2) + (b << 6)
		if off == 0 {
			if n != 1 || ip != len(in) {
				return nil, errCompression
			}
			return out, nil
		}
		off += 16384
		n += 2
	default:
		a, err = read()
		if err != nil {
			return nil, err
		}
		off = 1 + (t >> 2) + (a << 2)
		n = 2
	}
	if err = match(off, n); err != nil {
		return nil, err
	}
done:
	if ip < 2 {
		return nil, errCompression
	}
	trail = int(in[ip-2] & 3)
	if trail == 0 {
		goto main
	}
	if err = literal(trail); err != nil {
		return nil, err
	}
	goto nextMatch
}
