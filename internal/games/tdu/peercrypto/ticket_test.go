package peercrypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"testing"
)

func ticketFixture() (Ticket, [16]byte) {
	var random, iv [16]byte
	for i := range random {
		random[i], iv[i] = byte(i), byte(i)
	}
	return Ticket{Random: random, RecipientConnectionID: 0x12345678,
		Descriptor: NewClientDescriptor(0xc0000201, 8889, 3, 0x11223344, 0xc0a8010a, 8889, 3)}, iv
}

const ticketFixtureWire = "904152dc5098f2157cb88dd205e55aefb6a4b7613115642f66bb3ed2993b61192731e97b2737e33cae232e4ee694e1fbc6e8a73b42c947e6735e4b664033e4f0ab6b10e38b66b996990d8f1f6ca94a201b4c4e844d6954ebb5ff162c878f8e62dd01f7331df1b6bb88623ad2e8b7f6e1ff89000102030405060708090a0b0c0d0e0f"

func TestTicketIndependentOpenSSLFixture(t *testing.T) {
	// Expected ciphertext was generated independently with OpenSSL AES-256-CTR
	// and Python's HMAC-SHA256 over the documented 82-byte field sequence.
	ticket, iv := ticketFixture()
	wire, err := SealTicket(DefaultTicketKey(), iv, ticket)
	if err != nil {
		t.Fatal(err)
	}
	want := mustHex(t, ticketFixtureWire)
	if !bytes.Equal(wire[:], want) {
		t.Fatalf("wire=%x want=%x", wire, want)
	}
	decoded, err := OpenTicket(DefaultTicketKey(), want)
	if err != nil || decoded != ticket {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	if decoded.Descriptor.ConnectionID() != 0x11223344 || decoded.Descriptor.LocalPort() != 8889 || decoded.Descriptor.NATType() != 3 {
		t.Fatal("raw little-endian extension fields decoded incorrectly")
	}
}

func TestTicketTamperWrongKeyAndSize(t *testing.T) {
	wire := mustHex(t, ticketFixtureWire)
	for i := range wire {
		broken := bytes.Clone(wire)
		broken[i] ^= 1
		if _, err := OpenTicket(DefaultTicketKey(), broken); !errors.Is(err, ErrTicketAuthentication) {
			t.Fatalf("tamper at %d err=%v", i, err)
		}
	}
	wrongKey := DefaultTicketKey()
	wrongKey[0] ^= 1
	if _, err := OpenTicket(wrongKey, wire); !errors.Is(err, ErrTicketAuthentication) {
		t.Fatal(err)
	}
	for _, n := range []int{0, 4, TicketSize - 1, TicketSize + 1} {
		if _, err := OpenTicket(DefaultTicketKey(), make([]byte, n)); !errors.Is(err, ErrTicketLength) {
			t.Fatalf("length=%d err=%v", n, err)
		}
	}
}

func TestTicketBinding(t *testing.T) {
	ticket, _ := ticketFixture()
	for _, address := range []uint32{ticket.Descriptor.Address, ticket.Descriptor.LocalAddress()} {
		if err := ticket.ValidateBinding(ticket.RecipientConnectionID, address); err != nil {
			t.Fatal(err)
		}
	}
	if err := ticket.ValidateBinding(ticket.RecipientConnectionID+1, ticket.Descriptor.Address); !errors.Is(err, ErrRecipient) {
		t.Fatal(err)
	}
	if err := ticket.ValidateBinding(ticket.RecipientConnectionID, 0x7f000001); !errors.Is(err, ErrSource) {
		t.Fatal(err)
	}
	ticket.Descriptor.Extension[0] = 1
	if err := ticket.ValidateBinding(ticket.RecipientConnectionID, ticket.Descriptor.Address); !errors.Is(err, ErrDescriptor) {
		t.Fatal(err)
	}
	if _, err := SealTicket(DefaultTicketKey(), [16]byte{}, ticket); !errors.Is(err, ErrDescriptor) {
		t.Fatal(err)
	}
}

func TestTicketAuthenticatedMalformedFields(t *testing.T) {
	key := DefaultTicketKey()
	wire := mustHex(t, ticketFixtureWire)
	block, _ := aes.NewCipher(key[:])
	plain := make([]byte, 114)
	cipher.NewCTR(block, wire[114:]).XORKeyStream(plain, wire[:114])
	for _, test := range []struct {
		name   string
		offset int
		want   error
	}{
		{"wrong EncKey tag", 0, ErrTicketFormat},
		{"wrong random length", 3, ErrTicketFormat},
		{"wrong nested length", 25, ErrTicketFormat},
		{"wrong client tag", 26, ErrDescriptor},
		{"wrong extension length", 37, ErrDescriptor},
		{"unsupported extension type", 38, ErrDescriptor},
	} {
		t.Run(test.name, func(t *testing.T) {
			broken := bytes.Clone(plain)
			if test.offset == 38 {
				broken[test.offset] = 1
			} else {
				broken[test.offset] ^= 1
			}
			mac := hmac.New(sha256.New, key[:])
			mac.Write(broken[:82])
			copy(broken[82:], mac.Sum(nil))
			var malformed [TicketSize]byte
			cipher.NewCTR(block, wire[114:]).XORKeyStream(malformed[:114], broken)
			copy(malformed[114:], wire[114:])
			if _, err := OpenTicket(key, malformed[:]); !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
		})
	}
}
