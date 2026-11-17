// Adapted from sibling tdu-server; title differences are documented in docs/games/25tolife/analysis.md.
package quazalcrypto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// These bytes were independently generated with Python struct, ARC4 and HMAC.
const fixtureOpaqueTicket = "0f8322dddd2139d1139bc3f0a412d6899e6b851ba9e0788ca0d741c1a1d1cee8da87b986"
const fixtureServiceEnvelope = "202122232425262728292a2b2c2d2e2f02000000240000000f8322dddd2139d1139bc3f0a412d6899e6b851ba9e0788ca0d741c1a1d1cee8da87b986"
const fixtureServiceResponse = "c9bd62da63c73feb2ef2bded22f00460d171815fd6b742ee801d87042416dad348a188ef941134911f3cb8c01bad81e62742124f94e03c213d1bbe25de0348acf9f56581134b142351430778"
const fixtureConnect = "240000000f8322dddd2139d1139bc3f0a412d6899e6b851ba9e0788ca0d741c1a1d1cee8da87b9861c000000e8ad512bf1c99fe898ae96f7f1f83d988c3240b988b11f1ca4154076"

func TestServiceTicketAndConnectVectors(t *testing.T) {
	clientKey, destinationKey, sessionKey := fixtureKey(0), fixtureKey(16), fixtureKey(32)
	response, err := IssueServiceTicket(clientKey, destinationKey, 0x11223344, 2, sessionKey)
	if err != nil || !bytes.Equal(response, unhex(t, fixtureServiceResponse)) {
		t.Fatalf("issue: %x %v", response, err)
	}
	envelope, err := OpenServiceTicket(clientKey, response)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.SessionKey != sessionKey || envelope.ServerPID != 2 || !bytes.Equal(envelope.Ticket, unhex(t, fixtureOpaqueTicket)) {
		t.Fatalf("envelope: %+v", envelope)
	}
	plaintext, err := envelope.MarshalBinary()
	if err != nil || !bytes.Equal(plaintext, unhex(t, fixtureServiceEnvelope)) {
		t.Fatal("wrong envelope field order")
	}
	if _, err = OpenServiceTicket(destinationKey, response); !errors.Is(err, ErrAuthentication) {
		t.Fatal("accepted outer envelope under destination key")
	}
	challenge := Challenge{PrincipalID: 0x11223344, ConnectionID: 0xaabbccdd, Nonce: 0xffffffff}
	wire, err := BuildConnect(envelope.Ticket, envelope.SessionKey, challenge)
	if err != nil || !bytes.Equal(wire, unhex(t, fixtureConnect)) {
		t.Fatalf("connect: %x %v", wire, err)
	}
	ticket, got, err := OpenConnect(destinationKey, wire)
	if err != nil || ticket.PrincipalID != challenge.PrincipalID || ticket.SessionKey != sessionKey || got != challenge {
		t.Fatalf("open connect: %+v %+v %v", ticket, got, err)
	}
	if _, _, err = OpenConnect(clientKey, wire); !errors.Is(err, ErrAuthentication) {
		t.Fatal("accepted ticket under requesting client's key")
	}
	if got := ConnectReply(challenge.Nonce); !bytes.Equal(got, unhex(t, "0400000000000000")) {
		t.Fatalf("nonce wrap: %x", got)
	}
	if err := VerifyConnectReply(ConnectReply(0xffffffff), 0xffffffff); err != nil {
		t.Fatal(err)
	}
}

func TestConnectRejectsWrongPrincipalAndEitherTamperedLayer(t *testing.T) {
	destinationKey, sessionKey := fixtureKey(16), fixtureKey(32)
	ticket := unhex(t, fixtureOpaqueTicket)
	wire, err := BuildConnect(ticket, sessionKey, Challenge{PrincipalID: 0x11223345, Nonce: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenConnect(destinationKey, wire); !errors.Is(err, ErrPrincipal) {
		t.Fatalf("PID mismatch: %v", err)
	}
	wire = unhex(t, fixtureConnect)
	for _, offset := range []int{4, 4 + len(ticket) + 4} {
		changed := bytes.Clone(wire)
		changed[offset] ^= 1
		if _, _, err := OpenConnect(destinationKey, changed); !errors.Is(err, ErrAuthentication) {
			t.Fatalf("altered layer: %v", err)
		}
	}
}

func TestTicketLengthsAndParserOwnership(t *testing.T) {
	data := unhex(t, fixtureServiceEnvelope)
	for n := 0; n < len(data); n++ {
		if _, err := ParseServiceTicketEnvelope(data[:n]); !errors.Is(err, ErrLength) {
			t.Fatalf("truncated envelope %d", n)
		}
	}
	for _, n := range []uint32{0, 1, 35, 37, 0xffffffff} {
		changed := bytes.Clone(data)
		binary.LittleEndian.PutUint32(changed[20:], n)
		if _, err := ParseServiceTicketEnvelope(changed); !errors.Is(err, ErrLength) {
			t.Fatalf("declared length %d", n)
		}
	}
	if _, err := ParseServiceTicketEnvelope(append(bytes.Clone(data), 0)); !errors.Is(err, ErrLength) {
		t.Fatal("accepted envelope trailing byte")
	}
	envelope, err := ParseServiceTicketEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}
	data[24] ^= 1
	if !bytes.Equal(envelope.Ticket, unhex(t, fixtureOpaqueTicket)) {
		t.Fatal("ticket aliases input")
	}
	plain, err := (DestinationTicket{PrincipalID: 42, SessionKey: fixtureKey(32)}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 20 || binary.LittleEndian.Uint32(plain) != 42 {
		t.Fatal("destination ticket layout")
	}
	for n := 0; n < len(plain); n++ {
		if _, err := ParseDestinationTicket(plain[:n]); !errors.Is(err, ErrLength) {
			t.Fatalf("short destination ticket %d", n)
		}
	}
	if _, err := ParseDestinationTicket(append(plain, 0)); !errors.Is(err, ErrLength) {
		t.Fatal("accepted destination ticket trailer")
	}
}

func TestConnectLengthsAndReplies(t *testing.T) {
	wire := unhex(t, fixtureConnect)
	for n := 0; n < len(wire); n++ {
		if _, err := ParseConnectRequest(wire[:n]); !errors.Is(err, ErrLength) {
			t.Fatalf("truncated CONNECT %d", n)
		}
	}
	for _, offset := range []int{0, 40} {
		changed := bytes.Clone(wire)
		binary.LittleEndian.PutUint32(changed[offset:], 0xffffffff)
		if _, err := ParseConnectRequest(changed); !errors.Is(err, ErrLength) {
			t.Fatal("accepted oversized Buffer")
		}
	}
	if _, err := ParseConnectRequest(append(bytes.Clone(wire), 0)); !errors.Is(err, ErrLength) {
		t.Fatal("accepted CONNECT trailer")
	}
	request, err := ParseConnectRequest(wire)
	if err != nil {
		t.Fatal(err)
	}
	wire[4] ^= 1
	if !bytes.Equal(request.Ticket, unhex(t, fixtureOpaqueTicket)) {
		t.Fatal("CONNECT parser aliases input")
	}
	challenge, _ := (Challenge{PrincipalID: 1, ConnectionID: 2, Nonce: 3}).MarshalBinary()
	for n := 0; n < len(challenge); n++ {
		if _, err := ParseChallenge(challenge[:n]); !errors.Is(err, ErrLength) {
			t.Fatal("accepted short challenge")
		}
	}
	if _, err := ParseChallenge(append(challenge, 0)); !errors.Is(err, ErrLength) {
		t.Fatal("accepted challenge trailer")
	}
	reply := ConnectReply(42)
	for n := 0; n < len(reply); n++ {
		if err := VerifyConnectReply(reply[:n], 42); !errors.Is(err, ErrLength) {
			t.Fatal("accepted short reply")
		}
	}
	if err := VerifyConnectReply(reply, 43); !errors.Is(err, ErrChallenge) {
		t.Fatal("accepted incorrect challenge response")
	}
	reply[0] = 5
	if err := VerifyConnectReply(reply, 42); !errors.Is(err, ErrLength) {
		t.Fatal("accepted reply Buffer length")
	}
}

func FuzzConnectAndTicketBounds(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if envelope, err := ParseServiceTicketEnvelope(data); err == nil {
			encoded, err := envelope.MarshalBinary()
			if err != nil || !bytes.Equal(encoded, data) {
				t.Fatal("envelope was not canonical")
			}
		}
		if request, err := ParseConnectRequest(data); err == nil {
			encoded, err := request.MarshalBinary()
			if err != nil || !bytes.Equal(encoded, data) {
				t.Fatal("CONNECT was not canonical")
			}
		}
		_, _, _ = OpenConnect(Key{}, data)
	})
}
