package proxycrypto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"testing"
)

func exponent(n byte) []byte { out := make([]byte, 128); out[127] = n; return out }
func TestRetailDHGroupAndExchange(t *testing.T) {
	if dhPrime().BitLen() != 1036 || !dhPrime().ProbablyPrime(32) || !dhSubgroup().ProbablyPrime(32) {
		t.Fatal("wrong retail safe-prime group")
	}
	a, err := NewDHPrivate(exponent(2))
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewDHPrivate(exponent(3))
	if err != nil {
		t.Fatal(err)
	}
	ap, bp := a.Public(), b.Public()
	if !bytes.Equal(ap[:10], []byte{5, 1, 1, 0, 1, 0x10, 130, 0, 0, 0}) {
		t.Fatalf("public header %x", ap[:10])
	}
	ay, err := ParsePublic(ap[:])
	if err != nil || ay.Cmp(big.NewInt(16)) != 0 {
		t.Fatalf("public y %v %v", ay, err)
	}
	as, err := a.Shared(bp[:])
	if err != nil {
		t.Fatal(err)
	}
	bs, err := b.Shared(ap[:])
	if err != nil || !bytes.Equal(as, bs) || !bytes.Equal(as, []byte{0x10, 0}) {
		t.Fatalf("shared %x %x %v", as, bs, err)
	}
}
func TestDHPublicMinimalMagnitudePadding(t *testing.T) {
	key, _ := NewDHPrivate(exponent(2))
	encoded := make([]byte, PublicKeySize)
	copy(encoded, []byte{5, 1, 1, 0, 1, 0x10})
	binary.LittleEndian.PutUint32(encoded[6:10], 1)
	encoded[10] = 64
	for i := 11; i < len(encoded); i++ {
		encoded[i] = 0xa5
	}
	got, err := key.Shared(encoded)
	if err != nil || !bytes.Equal(got, []byte{0x10, 0}) {
		t.Fatalf("minimal padded import: %x %v", got, err)
	}
}
func TestDHRejectsInvalidInputs(t *testing.T) {
	key, _ := NewDHPrivate(exponent(2))
	valid := key.Public()
	for _, pos := range []int{0, 1, 2, 3, 4, 5} {
		bad := valid
		bad[pos] ^= 1
		if _, err := ParsePublic(bad[:]); err == nil {
			t.Fatalf("accepted header byte %d", pos)
		}
	}
	for _, size := range []uint32{0, 131, 0xffffffff} {
		bad := valid
		binary.LittleEndian.PutUint32(bad[6:10], size)
		if _, err := ParsePublic(bad[:]); err == nil {
			t.Fatal("accepted bad magnitude length")
		}
	}
	for _, y := range []*big.Int{big.NewInt(0), big.NewInt(1), new(big.Int).Sub(dhPrime(), big.NewInt(1)), new(big.Int).Sub(dhPrime(), big.NewInt(2)), dhPrime()} {
		bad := valid
		y.FillBytes(bad[10:])
		if _, err := ParsePublic(bad[:]); err == nil {
			t.Fatalf("accepted invalid element %x", y)
		}
	}
	for _, bad := range [][]byte{nil, exponent(0), exponent(1), make([]byte, 129)} {
		if _, err := NewDHPrivate(bad); err == nil {
			t.Fatal("accepted invalid exponent")
		}
	}
	if _, err := GenerateDH(bytes.NewReader([]byte{1})); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	if _, err := GenerateDH(bytes.NewReader(make([]byte, 128*16))); err == nil {
		t.Fatal("accepted zero RNG output")
	}
}
