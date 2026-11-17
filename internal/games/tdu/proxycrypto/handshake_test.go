package proxycrypto

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
)

func TestIndependentClientHelloVector(t *testing.T) {
	var nonce [32]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	want := unhex(t, "0004ffff0001000000000000757300000004000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1fd7193b2aaf32d227ed0c890ecdc9cd055c86051b7de37591e9d4fd10e7e58535")
	// Independent Python hmac/hashlib SHA-256 fixture using exact retail key.
	got := EncodeClientHello(0x7573, nonce)
	if !bytes.Equal(got, want) {
		t.Fatalf("hello %x", got)
	}
	hello, err := ParseClientHello(want)
	if err != nil || hello.Region != 0x7573 || hello.Version != 4 || hello.Nonce != nonce {
		t.Fatalf("parse %+v %v", hello, err)
	}
	for i := range want {
		bad := append([]byte(nil), want...)
		bad[i] ^= 1
		if _, err := ParseClientHello(bad); err == nil {
			t.Fatalf("accepted tamper byte %d", i)
		}
	}
}
func TestServerHandshakeTranscriptAndIdempotence(t *testing.T) {
	var nonce [32]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	h, hello, err := BeginServerHandshake(EncodeClientHello(0x7573, nonce), 0x1234, bytes.NewReader(exponent(2)))
	if err != nil {
		t.Fatal(err)
	}
	serverPublic, err := verifyHandshake(hello, 2, 0x1234, PublicKeySize, nonce[:])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hello, h.ServerHello()) {
		t.Fatal("hello retransmission changed")
	}
	clientPrivate, _ := NewDHPrivate(exponent(3))
	clientPublic := clientPrivate.Public()
	context := append(append([]byte(nil), nonce[:]...), serverPublic...)
	keyPacket := signHandshake(3, 0x1234, clientPublic[:], context)
	bad := append([]byte(nil), keyPacket...)
	bad[len(bad)-1] ^= 1
	if _, _, err := h.Complete(bad); !errors.Is(err, ErrAuthentication) {
		t.Fatal(err)
	}
	finish, keys, err := h.Complete(keyPacket)
	if err != nil {
		t.Fatal(err)
	}
	context = append(context, clientPublic[:]...)
	if _, err := verifyHandshake(finish, 8, 0x1234, 0, context); err != nil {
		t.Fatalf("finish transcript: %v", err)
	}
	if hex.EncodeToString(keys.ClientToProxy.Cipher[:]) != "c49bc148a22c253d805917da601f04fc" || hex.EncodeToString(keys.ProxyToClient.Authentication[:]) != "443cac01247ff38e4530d09611785bde15b3eb3a" {
		t.Fatalf("keys for independent shared-secret4096 fixture: %+v", keys)
	}
	again, againKeys, err := h.Complete(keyPacket)
	if err != nil || !bytes.Equal(finish, again) || keys != againKeys {
		t.Fatal("key retry changed exchange")
	}
	again[0] ^= 1
	third, _, _ := h.Complete(keyPacket)
	if !bytes.Equal(third, finish) {
		t.Fatal("returned slice aliases cached finish")
	}
	if _, _, err := h.Complete(bad); !errors.Is(err, ErrState) {
		t.Fatal("changed completed exchange accepted")
	}
}
func TestHandshakeMACRetainsRawPublicPadding(t *testing.T) {
	var nonce [32]byte
	h, hello, err := BeginServerHandshake(EncodeClientHello(0x7573, nonce), 7, bytes.NewReader(exponent(2)))
	if err != nil {
		t.Fatal(err)
	}
	serverPublic, _ := verifyHandshake(hello, 2, 7, PublicKeySize, nonce[:])
	minimal := make([]byte, 140)
	copy(minimal, []byte{5, 1, 1, 0, 1, 0x10})
	binary.LittleEndian.PutUint32(minimal[6:10], 1)
	minimal[10] = 64
	for i := 11; i < len(minimal); i++ {
		minimal[i] = 0xcc
	}
	context := append(append([]byte(nil), nonce[:]...), serverPublic...)
	finish, _, err := h.Complete(signHandshake(3, 7, minimal, context))
	if err != nil {
		t.Fatal(err)
	}
	context = append(context, minimal...)
	if _, err := verifyHandshake(finish, 8, 7, 0, context); err != nil {
		t.Fatal("transcript lost native padding")
	}
}
func TestHandshakeRejectsInvalidStateOrGroup(t *testing.T) {
	hello := EncodeClientHello(0x7573, [32]byte{})
	if _, _, err := BeginServerHandshake(hello, 0xffff, bytes.NewReader(exponent(2))); err == nil {
		t.Fatal("reserved session accepted")
	}
	h, server, err := BeginServerHandshake(hello, 1, bytes.NewReader(exponent(2)))
	if err != nil {
		t.Fatal(err)
	}
	serverPublic, _ := verifyHandshake(server, 2, 1, 140, make([]byte, 32))
	context := append(make([]byte, 32), serverPublic...)
	invalid := make([]byte, 140)
	if _, _, err := h.Complete(signHandshake(3, 1, invalid, context)); !errors.Is(err, ErrPublicKey) {
		t.Fatalf("bad group: %v", err)
	}
	valid, _ := NewDHPrivate(exponent(3))
	pub := valid.Public()
	if _, _, err := h.Complete(signHandshake(3, 2, pub[:], context)); err == nil {
		t.Fatal("wrong session accepted")
	}
	payload := make([]byte, 40)
	binary.BigEndian.PutUint32(payload[4:8], 3)
	if _, err := ParseClientHello(signHandshake(1, 0xffff, payload, nil)); err == nil {
		t.Fatal("old protocol accepted")
	}
}
