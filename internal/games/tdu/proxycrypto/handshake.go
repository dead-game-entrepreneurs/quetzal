package proxycrypto

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"sync"
)

const HandshakeTagSize = sha256.Size

// Decoded by the loop in 0x154410/0x154f00 from VA 0x4e6e40/0x4e6e60.
// Both embedded directional values decode identically in the US executable.
var handshakeKey = [32]byte{0xf2, 0x29, 0x6c, 0x20, 0xe8, 0x09, 0xa0, 0xcf, 0x98, 0x46, 0x4d, 0xe7, 0xb2, 0xee, 0x75, 0xc2, 0xaf, 0x1c, 0x24, 0xf5, 0x0a, 0x83, 0xb3, 0xce, 0x0d, 0x19, 0xd5, 0xc1, 0x1c, 0xc1, 0xf7, 0x0a}

type ClientHello struct {
	Region  uint32
	Version uint32
	Nonce   [32]byte
}

func handshakeTag(context, message []byte) []byte {
	h := hmac.New(sha256.New, handshakeKey[:])
	h.Write(context)
	h.Write(message)
	return h.Sum(nil)
}
func signHandshake(command, session uint16, payload, context []byte) []byte {
	message := make([]byte, HeaderSize+len(payload), HeaderSize+len(payload)+HandshakeTagSize)
	binary.BigEndian.PutUint16(message[:2], 4)
	binary.BigEndian.PutUint16(message[2:4], session)
	binary.BigEndian.PutUint16(message[4:6], command)
	copy(message[10:], payload)
	return append(message, handshakeTag(context, message)...)
}
func verifyHandshake(packet []byte, command, session uint16, payloadSize int, context []byte) ([]byte, error) {
	if len(packet) != HeaderSize+payloadSize+HandshakeTagSize {
		return nil, ErrPacket
	}
	if binary.BigEndian.Uint16(packet[:2]) != 4 || binary.BigEndian.Uint16(packet[2:4]) != session || binary.BigEndian.Uint16(packet[4:6]) != command || binary.BigEndian.Uint32(packet[6:10]) != 0 {
		return nil, ErrPacket
	}
	body := packet[:len(packet)-HandshakeTagSize]
	if !hmac.Equal(handshakeTag(context, body), packet[len(body):]) {
		return nil, ErrAuthentication
	}
	return body[HeaderSize:], nil
}

// EncodeClientHello is useful for protocol fixtures and client simulators.
func EncodeClientHello(region uint32, nonce [32]byte) []byte {
	var payload [40]byte
	binary.BigEndian.PutUint32(payload[:4], region)
	binary.BigEndian.PutUint32(payload[4:8], 4)
	copy(payload[8:], nonce[:])
	return signHandshake(1, 0xffff, payload[:], nil)
}
func ParseClientHello(packet []byte) (ClientHello, error) {
	var hello ClientHello
	payload, err := verifyHandshake(packet, 1, 0xffff, 40, nil)
	if err != nil {
		return hello, err
	}
	hello.Region = binary.BigEndian.Uint32(payload[:4])
	hello.Version = binary.BigEndian.Uint32(payload[4:8])
	if hello.Version != 4 {
		return ClientHello{}, ErrPacket
	}
	copy(hello.Nonce[:], payload[8:])
	return hello, nil
}

// ServerHandshake implements key exchange only. The embedded handshake key is
// shared by all copies of the game; its MAC does not establish account ownership.
// The hello identifies a region, not an account. The server must authenticate
// later native activation and bind it to a Quazal session before application traffic.
type ServerHandshake struct {
	mu           sync.Mutex
	hello        ClientHello
	session      uint16
	private      *DHPrivate
	serverPublic [PublicKeySize]byte
	completed    bool
	clientKey    []byte
	finish       []byte
	keys         TrafficKeys
}

// BeginServerHandshake verifies command 1 and returns signed command 2. Its
// state must be retained for retries; do not generate a fresh exponent for each
// duplicate UDP hello. Session 0xffff is reserved for the initial request.
func BeginServerHandshake(packet []byte, session uint16, random io.Reader) (*ServerHandshake, []byte, error) {
	if session == 0xffff {
		return nil, nil, ErrPacket
	}
	hello, err := ParseClientHello(packet)
	if err != nil {
		return nil, nil, err
	}
	private, err := GenerateDH(random)
	if err != nil {
		return nil, nil, err
	}
	state := &ServerHandshake{hello: hello, session: session, private: private, serverPublic: private.Public()}
	return state, state.ServerHello(), nil
}
func (h *ServerHandshake) Hello() ClientHello { return h.hello }
func (h *ServerHandshake) ServerHello() []byte {
	return signHandshake(2, h.session, h.serverPublic[:], h.hello.Nonce[:])
}

// Complete verifies command 3 against nonce||serverPublic, derives the traffic
// keys and returns signed command 8 over nonce||serverPublic||clientPublic.
// An exact retransmission returns the original finish/keys; a different key
// after completion is rejected. This does not perform native activation (29/30).
func (h *ServerHandshake) Complete(packet []byte) ([]byte, TrafficKeys, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.completed {
		if !bytes.Equal(packet, h.clientKey) {
			return nil, TrafficKeys{}, ErrState
		}
		return append([]byte(nil), h.finish...), h.keys, nil
	}
	context := make([]byte, 0, 32+PublicKeySize*2)
	context = append(context, h.hello.Nonce[:]...)
	context = append(context, h.serverPublic[:]...)
	public, err := verifyHandshake(packet, 3, h.session, PublicKeySize, context)
	if err != nil {
		return nil, TrafficKeys{}, err
	}
	shared, err := h.private.Shared(public)
	if err != nil {
		return nil, TrafficKeys{}, err
	}
	keys, err := DeriveKeys(shared)
	if err != nil {
		return nil, TrafficKeys{}, err
	}
	context = append(context, public...)
	finish := signHandshake(8, h.session, nil, context)
	h.completed = true
	h.keys = keys
	h.clientKey = append([]byte(nil), packet...)
	h.finish = finish
	return append([]byte(nil), finish...), keys, nil
}
