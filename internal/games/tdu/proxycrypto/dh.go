package proxycrypto

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
)

const (
	PublicKeySize   = 140
	dhMagnitudeSize = 130
	dhPrivateSize   = 128
)

var ErrPublicKey = errors.New("proxycrypto: invalid native DH public key")

// The retail group at main VA 0x544720 is labelled DH-1024 and advertises a
// 128-byte private exponent, but its radix-64 modulus is actually 1036 bits.
// 0xab3880/0xab30c0 use this exact modulus and generator 4.
func dhPrime() *big.Int {
	return new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 1036), big.NewInt(0x0ff33ef9))
}
func dhSubgroup() *big.Int { return new(big.Int).Rsh(new(big.Int).Sub(dhPrime(), big.NewInt(1)), 1) }

// DHPrivate holds the private exponent. This legacy finite-field group requires
// math/big; its exponentiation is not guaranteed constant time by the Go API.
type DHPrivate struct {
	private *big.Int
	public  [PublicKeySize]byte
}

// NewDHPrivate matches the client's 128-byte unsigned exponent input. It is
// exposed for reproducible fixtures; runtime callers should use GenerateDH.
func NewDHPrivate(exponent []byte) (*DHPrivate, error) {
	if len(exponent) != dhPrivateSize {
		return nil, ErrPublicKey
	}
	x := new(big.Int).SetBytes(exponent)
	if x.Cmp(big.NewInt(2)) < 0 {
		return nil, ErrPublicKey
	}
	y := new(big.Int).Exp(big.NewInt(4), x, dhPrime())
	if !validElement(y) {
		return nil, ErrPublicKey
	}
	key := &DHPrivate{private: x}
	copy(key.public[:6], []byte{5, 1, 1, 0, 1, 0x10})
	// The receiver permits leading zero magnitude bytes. Fixed width avoids
	// leaking uninitialized padding when a generated public element is shorter.
	binary.LittleEndian.PutUint32(key.public[6:10], dhMagnitudeSize)
	y.FillBytes(key.public[10:])
	return key, nil
}
func GenerateDH(random io.Reader) (*DHPrivate, error) {
	if random == nil {
		random = rand.Reader
	}
	for attempts := 0; attempts < 16; attempts++ {
		var exponent [dhPrivateSize]byte
		if _, err := io.ReadFull(random, exponent[:]); err != nil {
			return nil, err
		}
		key, err := NewDHPrivate(exponent[:])
		if err == nil {
			return key, nil
		}
	}
	return nil, ErrPublicKey
}
func (key *DHPrivate) Public() [PublicKeySize]byte { return key.public }
func validElement(y *big.Int) bool {
	if y.Cmp(big.NewInt(2)) < 0 || y.Cmp(new(big.Int).Sub(dhPrime(), big.NewInt(2))) > 0 {
		return false
	}
	return new(big.Int).Exp(y, dhSubgroup(), dhPrime()).Cmp(big.NewInt(1)) == 0
}

// ParsePublic validates the sole group/public-key encoding used by this build.
// The retail importer reads a minimal magnitude and ignores remaining fixed
// payload padding; preserve the original 140 bytes separately for transcript MACs.
func ParsePublic(encoded []byte) (*big.Int, error) {
	if len(encoded) != PublicKeySize || encoded[0] != 5 || encoded[1] != 1 || encoded[2] != 1 || encoded[3] != 0 || encoded[4] != 1 || encoded[5] != 0x10 {
		return nil, ErrPublicKey
	}
	size := binary.LittleEndian.Uint32(encoded[6:10])
	if size == 0 || size > dhMagnitudeSize {
		return nil, ErrPublicKey
	}
	y := new(big.Int).SetBytes(encoded[10 : 10+int(size)])
	if !validElement(y) {
		return nil, ErrPublicKey
	}
	return y, nil
}
func (key *DHPrivate) Shared(encoded []byte) ([]byte, error) {
	y, err := ParsePublic(encoded)
	if err != nil {
		return nil, err
	}
	secret := new(big.Int).Exp(y, key.private, dhPrime())
	if secret.Cmp(big.NewInt(2)) < 0 {
		return nil, ErrPublicKey
	}
	return secret.Bytes(), nil
}
