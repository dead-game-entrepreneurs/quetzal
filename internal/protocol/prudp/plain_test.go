package prudp

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"
)

func TestPlainWrapperMatchesGuestRequestShape(t *testing.T) {
	// Synthetic call ID; same observed protocol, method and guest String schema.
	wire, _ := hex.DecodeString("00110000008a01000000010000000600677565737400")
	plain, err := decodePlainPayload(wire, 65536)
	if err != nil || len(plain) != 21 || plain[4] != 0x8a {
		t.Fatal("plain guest wrapper", err)
	}
	if !bytes.Equal(encodePlainPayload(plain), wire) {
		t.Fatal("plain encoding added an encryption layer")
	}
	if _, err := decodePlainPayload(wire, len(plain)-1); err == nil {
		t.Fatal("payload bound ignored")
	}
	if _, err := decodePlainPayload([]byte{2, 0}, 65536); err == nil {
		t.Fatal("unknown selector accepted")
	}
}

func TestPlainTransport(t *testing.T) {
	s, h := startTestServer(t, func(o *Options) { o.PlainPayload = true })
	c := newClient(t, s)
	c.syn(t)
	request := c.requestConnect()
	request.Payload = encodePlainPayload([]byte("ticket"))
	c.send(t, request)
	connected, _ := c.read(t)
	payload, err := decodePlainPayload(connected.Payload, 1000)
	if err != nil || string(payload) != "accepted:ticket" {
		t.Fatal("CONNECT plain response", err)
	}
	c.serverSession = connected.Session
	p := c.data(2, 0, nil)
	p.Payload = encodePlainPayload([]byte("plain RPC"))
	c.send(t, p)
	c.read(t)
	select {
	case got := <-h.messages:
		if string(got) != "plain RPC" {
			t.Fatal("plain DATA changed")
		}
	case <-time.After(time.Second):
		t.Fatal("plain DATA not dispatched")
	}
}
