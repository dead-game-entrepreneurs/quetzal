// Package proxycrypto implements the US PS2 native edNet proxy's cryptography.
// It is separate from Quazal PRUDP and the XOR-obfuscated native peer wrapper.
package proxycrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"math"
	"sync"
)

const (
	HeaderSize  = 10
	TagSize     = 8
	CounterSize = 4
	// MaxDatagram follows edNetServiceUDP's 0x578-byte receive bound (0x136b50).
	MaxDatagram      = 1400
	MaxPlaintext     = MaxDatagram - TagSize - CounterSize
	ReplayWindowSize = 4
)

var (
	ErrPacket         = errors.New("proxycrypto: invalid packet")
	ErrAuthentication = errors.New("proxycrypto: authentication failed")
	ErrDirection      = errors.New("proxycrypto: invalid direction")
	ErrReplay         = errors.New("proxycrypto: replay or packet outside receive window")
	ErrSequence       = errors.New("proxycrypto: sequence exhausted; establish new keys")
	ErrState          = errors.New("proxycrypto: unexpected key exchange state")
)

type Direction uint8

const (
	ClientToProxy Direction = iota
	ProxyToClient
)

type DirectionKeys struct {
	Cipher         [16]byte
	Authentication [20]byte
}

type TrafficKeys struct {
	ClientToProxy DirectionKeys
	ProxyToClient DirectionKeys
}

// DeriveKeys follows MainGame 0xaab730. The DH secret is the minimal unsigned
// big-endian integer representation, not a fixed-width padded group element.
func DeriveKeys(sharedSecret []byte) (TrafficKeys, error) {
	var out TrafficKeys
	if len(sharedSecret) == 0 || len(sharedSecret) > dhMagnitudeSize {
		return out, ErrPacket
	}
	for len(sharedSecret) > 0 && sharedSecret[0] == 0 {
		sharedSecret = sharedSecret[1:]
	}
	if len(sharedSecret) == 0 {
		return out, ErrPacket
	}
	derive := func(suffix string) [20]byte {
		h := sha1.New()
		h.Write(sharedSecret)
		h.Write([]byte(suffix))
		var sum [20]byte
		copy(sum[:], h.Sum(nil))
		return sum
	}
	cenc, penc := derive("CEnc"), derive("PEnc")
	copy(out.ClientToProxy.Cipher[:], cenc[:16])
	copy(out.ProxyToClient.Cipher[:], penc[:16])
	out.ClientToProxy.Authentication = derive("CAuth")
	out.ProxyToClient.Authentication = derive("PAuth")
	return out, nil
}

func (keys TrafficKeys) direction(direction Direction) (DirectionKeys, error) {
	switch direction {
	case ClientToProxy:
		return keys.ClientToProxy, nil
	case ProxyToClient:
		return keys.ProxyToClient, nil
	default:
		return DirectionKeys{}, ErrDirection
	}
}

func envelopeOK(plain []byte, encrypted bool) bool {
	if len(plain) < HeaderSize {
		return false
	}
	flags := binary.BigEndian.Uint16(plain[:2])
	if encrypted {
		if flags != 3 && flags != 5 {
			return false
		}
	} else if flags != 2 && flags != 4 {
		return false
	}
	address := binary.BigEndian.Uint32(plain[6:10])
	if flags&4 != 0 {
		return address == 0
	}
	return address != 0
}

func authenticator(key DirectionKeys, plain []byte) []byte {
	h := hmac.New(sha1.New, key.Authentication[:])
	h.Write(plain)
	return h.Sum(nil)
}

func transform(key DirectionKeys, sequence uint32, data []byte) {
	block, _ := aes.NewCipher(key.Cipher[:]) // Array size guarantees a valid key.
	var counter [aes.BlockSize]byte
	binary.BigEndian.PutUint32(counter[:4], sequence)
	cipher.NewCTR(block, counter[:]).XORKeyStream(data, data)
}

// SealPacket seals a plaintext ten-byte edNet envelope plus payload. Its flags
// must be 2 (forwarded data) or 4 (control). The encrypted bit is added before
// authenticating. Sequence reuse under the same directional keys is forbidden;
// use Sender for a connection-owned counter.
func SealPacket(plain []byte, sequence uint32, direction Direction, keys TrafficKeys) ([]byte, error) {
	key, err := keys.direction(direction)
	if err != nil {
		return nil, err
	}
	if len(plain) > MaxPlaintext || !envelopeOK(plain, false) {
		return nil, ErrPacket
	}
	out := make([]byte, len(plain)+TagSize+CounterSize)
	copy(out, plain)
	binary.BigEndian.PutUint16(out[:2], binary.BigEndian.Uint16(out[:2])|1)
	copy(out[len(plain):], authenticator(key, out[:len(plain)])[:TagSize])
	binary.BigEndian.PutUint32(out[len(out)-CounterSize:], sequence)
	transform(key, sequence, out[4:len(out)-CounterSize])
	return out, nil
}

// OpenPacket authenticates/decrypts without replay state. Returned plaintext
// retains wire flags 3/5. The input is never modified. A listener should use
// Receiver.Open, which also checks duplicate and out-of-order sequences.
func OpenPacket(packet []byte, direction Direction, keys TrafficKeys) ([]byte, uint32, error) {
	key, err := keys.direction(direction)
	if err != nil {
		return nil, 0, err
	}
	if len(packet) < HeaderSize+TagSize+CounterSize || len(packet) > MaxDatagram {
		return nil, 0, ErrPacket
	}
	flags := binary.BigEndian.Uint16(packet[:2])
	if flags != 3 && flags != 5 {
		return nil, 0, ErrPacket
	}
	sequence := binary.BigEndian.Uint32(packet[len(packet)-CounterSize:])
	opened := append([]byte(nil), packet[:len(packet)-CounterSize]...)
	transform(key, sequence, opened[4:])
	plain := opened[:len(opened)-TagSize]
	if !hmac.Equal(authenticator(key, plain)[:TagSize], opened[len(plain):]) {
		return nil, 0, ErrAuthentication
	}
	if !envelopeOK(plain, true) {
		return nil, 0, ErrPacket
	}
	return plain, sequence, nil
}

// Sender owns one direction of one key exchange. Create a new Sender only when
// new traffic keys are established, not when reconnecting a socket with old keys.
type Sender struct {
	mu        sync.Mutex
	keys      TrafficKeys
	direction Direction
	next      uint64
}

func NewSender(keys TrafficKeys, direction Direction) (*Sender, error) {
	if _, err := keys.direction(direction); err != nil {
		return nil, err
	}
	return &Sender{keys: keys, direction: direction}, nil
}
func (s *Sender) Seal(plain []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next > math.MaxUint32 {
		return nil, ErrSequence
	}
	packet, err := SealPacket(plain, uint32(s.next), s.direction, s.keys)
	if err == nil {
		s.next++
	}
	return packet, err
}

// Receiver accepts authenticated packets in a four-packet sliding window and
// rejects every duplicate in that window. Unlike the retail last-accepted
// counter, accepting an out-of-order packet never moves this window backwards.
type Receiver struct {
	mu          sync.Mutex
	keys        TrafficKeys
	direction   Direction
	highest     uint32
	seen        uint8
	initialized bool
}

func NewReceiver(keys TrafficKeys, direction Direction) (*Receiver, error) {
	if _, err := keys.direction(direction); err != nil {
		return nil, err
	}
	return &Receiver{keys: keys, direction: direction}, nil
}
func (r *Receiver) Open(packet []byte) ([]byte, uint32, error) {
	plain, sequence, err := OpenPacket(packet, r.direction, r.keys)
	if err != nil {
		return nil, 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.initialized {
		r.initialized = true
		r.highest = sequence
		r.seen = 1
		return plain, sequence, nil
	}
	if sequence > r.highest {
		shift := sequence - r.highest
		if shift >= ReplayWindowSize {
			r.seen = 1
		} else {
			r.seen = (r.seen<<shift | 1) & ((1 << ReplayWindowSize) - 1)
		}
		r.highest = sequence
		return plain, sequence, nil
	}
	distance := r.highest - sequence
	if distance >= ReplayWindowSize || r.seen&(1<<distance) != 0 {
		return nil, 0, ErrReplay
	}
	r.seen |= 1 << distance
	return plain, sequence, nil
}
