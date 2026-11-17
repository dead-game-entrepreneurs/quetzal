package proxycrypto

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"testing"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func fixtureKeys(t *testing.T) TrafficKeys {
	t.Helper()
	keys, err := DeriveKeys(unhex(t, "0102030405060708090a0b0c0d0e0f10"))
	if err != nil {
		t.Fatal(err)
	}
	return keys
}
func control(command uint16, payload []byte) []byte {
	out := make([]byte, HeaderSize+len(payload))
	binary.BigEndian.PutUint16(out[:2], 4)
	binary.BigEndian.PutUint16(out[2:4], 0x1234)
	binary.BigEndian.PutUint16(out[4:6], command)
	copy(out[10:], payload)
	return out
}

func TestIndependentPacketVectors(t *testing.T) {
	// Generated independently with Python hashlib/hmac and OpenSSL AES-128-CTR,
	// not with this package's seal/open implementation.
	tests := []struct {
		direction   Direction
		seq         uint32
		plain, wire string
	}{
		{ClientToProxy, 0, "00041234001d00000000", "000512340418eede369d2cdcd3afc20e45bc00000000"},
		{ProxyToClient, 0x10203040, "00025a0122b9c0000201000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20", "00035a01993eb797aee6b8a8f08307dd003e306f6a76443635e5e47e9c310aa2a21d1f961d9526de2b90250d5e45b9b53c8efa10203040"},
	}
	keys := fixtureKeys(t)
	for _, tc := range tests {
		t.Run(tc.wire[:8], func(t *testing.T) {
			plain, want := unhex(t, tc.plain), unhex(t, tc.wire)
			got, err := SealPacket(plain, tc.seq, tc.direction, keys)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("seal = %x,%v; want %x", got, err, want)
			}
			saved := append([]byte(nil), want...)
			opened, seq, err := OpenPacket(want, tc.direction, keys)
			expected := append([]byte(nil), plain...)
			expected[1] |= 1
			if err != nil || seq != tc.seq || !bytes.Equal(opened, expected) {
				t.Fatalf("open = %x,%x,%v", opened, seq, err)
			}
			if !bytes.Equal(want, saved) {
				t.Fatal("open modified ciphertext")
			}
		})
	}
}
func TestKDFIndependentDigestsAndCanonicalSecret(t *testing.T) {
	keys := fixtureKeys(t)
	if hex.EncodeToString(keys.ClientToProxy.Cipher[:]) != "14ab5c77ca06776f331d964ef9a8e0f3" || hex.EncodeToString(keys.ClientToProxy.Authentication[:]) != "cadc2c2ebabd45c5d8ab00634a1926fe36820625" || hex.EncodeToString(keys.ProxyToClient.Cipher[:]) != "8df1cb6c05091196941e7bb9c8ba14b8" || hex.EncodeToString(keys.ProxyToClient.Authentication[:]) != "3f266447209579cbe46ec378375f1320647b1ad2" {
		t.Fatalf("unexpected directional KDF: %+v", keys)
	}
	padded := append([]byte{0, 0}, unhex(t, "0102030405060708090a0b0c0d0e0f10")...)
	got, err := DeriveKeys(padded)
	if err != nil || got != keys {
		t.Fatal("secret is not canonical")
	}
	for _, bad := range [][]byte{nil, {0, 0}, make([]byte, 131)} {
		if _, err := DeriveKeys(bad); err == nil {
			t.Fatal("accepted invalid secret")
		}
	}
}
func TestTamperAndDirectionRejected(t *testing.T) {
	keys := fixtureKeys(t)
	packet, err := SealPacket(control(29, []byte("native")), 7, ClientToProxy, keys)
	if err != nil {
		t.Fatal(err)
	}
	for i := range packet {
		changed := append([]byte(nil), packet...)
		changed[i] ^= 1
		plain, _, err := OpenPacket(changed, ClientToProxy, keys)
		if err == nil || plain != nil {
			t.Fatalf("accepted changed byte %d", i)
		}
	}
	if _, _, err := OpenPacket(packet, ProxyToClient, keys); err == nil {
		t.Fatal("accepted wrong direction")
	}
	if _, err := SealPacket(control(29, nil), 0, Direction(255), keys); !errors.Is(err, ErrDirection) {
		t.Fatal(err)
	}
}
func TestPacketBoundsAndEnvelope(t *testing.T) {
	keys := fixtureKeys(t)
	for n := 0; n < HeaderSize; n++ {
		if _, err := SealPacket(make([]byte, n), 0, ClientToProxy, keys); err == nil {
			t.Fatal("accepted short envelope")
		}
	}
	max := control(29, make([]byte, MaxPlaintext-HeaderSize))
	packet, err := SealPacket(max, 0, ClientToProxy, keys)
	if err != nil || len(packet) != MaxDatagram {
		t.Fatalf("max packet: %v", err)
	}
	if _, err := SealPacket(append(max, 0), 0, ClientToProxy, keys); err == nil {
		t.Fatal("accepted oversized packet")
	}
	for n := 0; n < HeaderSize+TagSize+CounterSize; n++ {
		if _, _, err := OpenPacket(make([]byte, n), ClientToProxy, keys); err == nil {
			t.Fatal("accepted short packet")
		}
	}
	if _, _, err := OpenPacket(make([]byte, MaxDatagram+1), ClientToProxy, keys); err == nil {
		t.Fatal("accepted oversized packet")
	}
	bad := control(29, nil)
	bad[1] = 3
	if _, err := SealPacket(bad, 0, ClientToProxy, keys); err == nil {
		t.Fatal("accepted caller-supplied encrypted flags")
	}
	bad = control(29, nil)
	bad[9] = 1
	if _, err := SealPacket(bad, 0, ClientToProxy, keys); err == nil {
		t.Fatal("accepted control with destination")
	}
}
func TestSenderCounterAndExhaustion(t *testing.T) {
	sender, _ := NewSender(fixtureKeys(t), ProxyToClient)
	if _, err := sender.Seal(nil); err == nil {
		t.Fatal("invalid packet accepted")
	}
	p, err := sender.Seal(control(30, nil))
	if err != nil || binary.BigEndian.Uint32(p[len(p)-4:]) != 0 {
		t.Fatal("first valid packet did not use sequence zero")
	}
	sender.next = math.MaxUint32
	p, err = sender.Seal(control(30, nil))
	if err != nil || binary.BigEndian.Uint32(p[len(p)-4:]) != math.MaxUint32 {
		t.Fatal("last sequence failed")
	}
	if _, err := sender.Seal(control(30, nil)); !errors.Is(err, ErrSequence) {
		t.Fatal("sequence wrapped")
	}
}
func TestReceiverWindowAndAuthenticationBeforeState(t *testing.T) {
	keys := fixtureKeys(t)
	r, _ := NewReceiver(keys, ClientToProxy)
	seal := func(seq uint32) []byte {
		p, err := SealPacket(control(29, nil), seq, ClientToProxy, keys)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, seq := range []uint32{100, 98, 99, 101} {
		if _, _, err := r.Open(seal(seq)); err != nil {
			t.Fatalf("sequence %d: %v", seq, err)
		}
	}
	for _, seq := range []uint32{100, 98, 97} {
		if _, _, err := r.Open(seal(seq)); !errors.Is(err, ErrReplay) {
			t.Fatalf("replay %d: %v", seq, err)
		}
	}
	corrupt := seal(1000)
	corrupt[5] ^= 1
	if _, _, err := r.Open(corrupt); err == nil {
		t.Fatal("accepted tamper")
	}
	if _, _, err := r.Open(seal(102)); err != nil {
		t.Fatalf("tamper advanced receive state: %v", err)
	}
}
