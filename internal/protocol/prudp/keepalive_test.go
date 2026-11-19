package prudp

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestKeepAliveDoesNotConsumeReliableSequenceOrAcknowledgeData(t *testing.T) {
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	h := &testHandler{messages: make(chan []byte, 1), disconnected: make(chan Peer, 1)}
	s, err := New(Options{Bind: netip.MustParseAddrPort("127.0.0.1:0"), Handler: h,
		ChecksumSeed: 0xff, PlainPayload: true, KeepAliveInterval: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Unix(1000, 0)
	peer := Peer{ID: 1, Addr: receiver.LocalAddr().(*net.UDPAddr).AddrPort(), LocalPort: 30121,
		SourceStream: 0x3f, DestinationStream: 0x31, ClientSession: 44}
	key := peerKey{peer.Addr, peer.SourceStream, peer.DestinationStream}
	c := &connection{peer: peer, localCookie: 0x1111, remoteCookie: 0x2222, session: 33,
		expected: 2, next: 1, last: now, lastKeepAlive: now,
		pending: map[uint16]*transmission{}, received: map[uint16]Packet{}}
	s.peers[key] = c
	read := func() Packet {
		t.Helper()
		if err := receiver.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		wire := make([]byte, 1000)
		n, _, err := receiver.ReadFromUDPAddrPort(wire)
		if err != nil {
			t.Fatal(err)
		}
		p, used, err := ParseFrame(wire[:n], 0xff)
		if err != nil || used != n {
			t.Fatal("invalid emitted frame", err)
		}
		return p
	}
	ack := func(p Packet) Packet {
		return Packet{Source: peer.SourceStream, Destination: peer.DestinationStream,
			Session: peer.ClientSession, Signature: c.localCookie, Type: p.Type, Flags: FlagAck,
			Sequence: p.Sequence, Fragment: p.Fragment}
	}
	s.expire(now.Add(5 * time.Second))
	ping := read()
	if ping.Type != PING || ping.Flags != FlagNeedsAck || len(ping.Payload) != 0 ||
		ping.Source != 0x31 || ping.Destination != 0x3f || ping.Signature != c.remoteCookie || ping.Session != c.session {
		t.Fatal("keepalive did not retain the established endpoint and retail PING shape")
	}
	bad := ack(ping)
	bad.Signature++
	s.receive(context.Background(), peer.Addr, bad, now.Add(5050*time.Millisecond))
	if c.keepAliveAcknowledged != 0 {
		t.Fatal("wrong-cookie reply acknowledged the keepalive")
	}
	s.receive(context.Background(), peer.Addr, ack(ping), now.Add(5100*time.Millisecond))
	if c.keepAliveAcknowledged != 1 || c.keepAlivePending || c.lastKeepAliveAck.IsZero() {
		t.Fatal("valid return-path proof was not recorded")
	}
	// The first application request/reply still uses its original sequence.
	request := Packet{Source: peer.SourceStream, Destination: peer.DestinationStream,
		Session: peer.ClientSession, Signature: c.localCookie, Type: DATA,
		Flags: FlagReliable | FlagNeedsAck, Sequence: 2, Payload: encodePlainPayload([]byte("next RPC"))}
	s.receive(context.Background(), peer.Addr, request, now.Add(5200*time.Millisecond))
	if p := read(); p.Type != DATA || p.Flags != FlagAck || p.Sequence != 2 {
		t.Fatal("keepalive changed application receive ordering")
	}
	select {
	case payload := <-h.messages:
		if string(payload) != "next RPC" {
			t.Fatal("keepalive changed application data")
		}
	default:
		t.Fatal("application request was not delivered after keepalive")
	}
	if err := s.respond(c, [][]byte{[]byte("reply")}, now.Add(5200*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	reply := read()
	if reply.Type != DATA || reply.Sequence != 1 || len(c.pending) != 1 {
		t.Fatal("keepalive consumed a reliable sequence")
	}
	// A duplicate ping ACK with the same numeric sequence cannot release DATA.
	s.receive(context.Background(), peer.Addr, ack(ping), now.Add(5300*time.Millisecond))
	if len(c.pending) != 1 || c.keepAliveAcknowledged != 1 {
		t.Fatal("duplicate keepalive ACK changed reliable application state")
	}
	s.receive(context.Background(), peer.Addr, ack(reply), now.Add(5400*time.Millisecond))
	if len(c.pending) != 0 {
		t.Fatal("normal DATA acknowledgement no longer works")
	}
	s.expire(now.Add(10 * time.Second))
	nextPing := read()
	if nextPing.Type != PING || nextPing.Sequence != ping.Sequence+1 || c.next != 2 || len(c.pending) != 0 {
		t.Fatal("periodic keepalive changed reliable state")
	}
	if err := s.respond(c, [][]byte{[]byte("second reply")}, now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	secondReply := read()
	if secondReply.Sequence != nextPing.Sequence {
		t.Fatal("test did not exercise overlapping DATA and PING sequences")
	}
	s.receive(context.Background(), peer.Addr, ack(nextPing), now.Add(10100*time.Millisecond))
	if len(c.pending) != 1 || c.keepAliveAcknowledged != 2 {
		t.Fatal("matching live PING acknowledgement retired reliable DATA")
	}
	s.receive(context.Background(), peer.Addr, ack(secondReply), now.Add(10200*time.Millisecond))
	if len(c.pending) != 0 {
		t.Fatal("second DATA reply could not be acknowledged")
	}
	// A console that stops replying is still removed on the bounded idle budget.
	s.expire(now.Add(3 * time.Minute))
	if s.peers[key] != nil {
		t.Fatal("outgoing keepalive kept an unreachable connection alive")
	}
}
