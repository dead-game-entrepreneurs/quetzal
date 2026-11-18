package prudp

import (
	"bytes"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestRetailReceiveBufferReassemblesLargeReplyAndStaysConnected(t *testing.T) {
	// The hosted nine-player 101/7 reply was 3964 bytes. The old 1000-byte
	// application chunks became 1015-byte datagrams, exceeding this buffer.
	expected := bytes.Repeat([]byte{0x42}, 3964)
	s, h := startTestServer(t, func(o *Options) {
		o.PlainPayload, o.MaxDatagramSize = true, 1000
		o.Handler.(*testHandler).respond = func(message []byte) [][]byte {
			if string(message) == "statistics" {
				return [][]byte{expected}
			}
			return [][]byte{[]byte("still online")}
		}
	})
	c := newClient(t, s)
	c.syn(t)
	connect := c.requestConnect()
	connect.Payload = encodePlainPayload([]byte("ticket"))
	c.send(t, connect)
	c.read(t)
	request := c.data(2, 0, nil)
	request.Payload = encodePlainPayload([]byte("statistics"))
	c.send(t, request)
	c.read(t) // request ACK
	var assembled []byte
	for sequence := uint16(1); ; sequence++ {
		checkDeadline := c.conn.SetReadDeadline(time.Now().Add(time.Second))
		if checkDeadline != nil {
			t.Fatal(checkDeadline)
		}
		buf := make([]byte, 1000) // actual retail receive capacity
		n, err := c.conn.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		p, used, err := ParseFrame(buf[:n], 0xff)
		if err != nil || used != n {
			t.Fatalf("reply was truncated by retail receive buffer: %v", err)
		}
		if p.Type != DATA || p.Sequence != sequence || p.Flags&FlagAck != 0 {
			t.Fatal("unexpected reply")
		}
		part, err := decodePlainPayload(p.Payload, 1000)
		if err != nil {
			t.Fatal(err)
		}
		assembled = append(assembled, part...)
		c.ack(t, p)
		if p.Fragment == 0 {
			break
		}
		if p.Fragment != byte(sequence) {
			t.Fatal("fragment order")
		}
	}
	if !bytes.Equal(assembled, expected) {
		t.Fatal("large reply lost content")
	}
	// Let the test transport's retry budget elapse. Every fragment must have
	// been acknowledged, rather than merely preventing a UI decode failure.
	select {
	case <-h.disconnected:
		t.Fatal("acknowledged reply exhausted retries")
	case <-time.After(250 * time.Millisecond):
	}
	request = c.data(3, 0, nil)
	request.Payload = encodePlainPayload([]byte("next request"))
	c.send(t, request)
	c.read(t)
	p, _ := c.read(t)
	c.ack(t, p)
	message, err := decodePlainPayload(p.Payload, 1000)
	if err != nil || string(message) != "still online" {
		t.Fatal("connection did not survive statistics", err)
	}
}

func TestDatagramBudgetIncludesEachPayloadMode(t *testing.T) {
	for _, mode := range []string{"plain", "encrypted", "opaque"} {
		t.Run(mode, func(t *testing.T) {
			receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
			if err != nil {
				t.Fatal(err)
			}
			defer receiver.Close()
			s, err := New(Options{Bind: netip.MustParseAddrPort("127.0.0.1:0"), Handler: &testHandler{}, MaxDatagramSize: 1000, PlainPayload: mode == "plain", OpaquePayload: mode == "opaque"})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			c := &connection{peer: Peer{Addr: receiver.LocalAddr().(*net.UDPAddr).AddrPort()}, pending: map[uint16]*transmission{}, next: 1}
			expected := bytes.Repeat([]byte{0x39}, 2300)
			if err := s.respond(c, [][]byte{expected}, time.Now()); err != nil {
				t.Fatal(err)
			}
			var assembled []byte
			for range len(c.pending) {
				receiver.SetReadDeadline(time.Now().Add(time.Second))
				wire := make([]byte, 1000)
				n, _, err := receiver.ReadFromUDP(wire)
				if err != nil {
					t.Fatal(err)
				}
				p, used, err := ParseFrame(wire[:n], s.opts.ChecksumSeed)
				if err != nil || used != n {
					t.Fatal("datagram exceeded budget", err)
				}
				body, err := s.decodePayload(p.Payload)
				if err != nil {
					t.Fatal(err)
				}
				assembled = append(assembled, body...)
			}
			if !bytes.Equal(assembled, expected) {
				t.Fatal("payload changed")
			}
		})
	}
}

func TestInvalidDatagramLimitsAreRejected(t *testing.T) {
	for _, size := range []int{-1, 1, 63, 65508} {
		s, err := New(Options{Bind: netip.MustParseAddrPort("127.0.0.1:0"), Handler: &testHandler{}, MaxDatagramSize: size})
		if err == nil {
			s.Close()
			t.Fatalf("accepted datagram budget %d", size)
		}
	}
}
