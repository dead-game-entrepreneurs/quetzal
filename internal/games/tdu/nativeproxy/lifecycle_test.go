package nativeproxy

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestBatchRepliesAreSeparateEncryptedDatagrams(t *testing.T) {
	s, _, _ := startServer(t, func(o *Options) {
		o.BatchServices = map[uint16]BatchDataHandler{9002: func(_ context.Context, info SessionInfo, _ netip.AddrPort, b []byte) ([][]byte, error) {
			if info.Generation == 0 || !info.TransportActivated {
				t.Error("missing transport ownership")
			}
			return [][]byte{append([]byte("action:"), b...), []byte("transaction-complete")}, nil
		}}
	})
	p := handshake(t, s)
	p.activate(t)
	send(t, p.conn, p.server, p.application(t, netip.AddrPortFrom(s.opts.PublicIP, 9002), []byte("request")))
	for _, want := range [][]byte{[]byte("action:request"), []byte("transaction-complete")} {
		plain, _, err := p.receiver.Open(receive(t, p.conn))
		if err != nil || len(plain) < 10 || !bytes.Equal(plain[10:], want) {
			t.Fatalf("batch packet %x %v", plain, err)
		}
	}
	if s.Stats().ApplicationRequests != 1 {
		t.Fatal("batch counted as multiple requests")
	}
}

func TestOversizedBatchSendsNoPartialReply(t *testing.T) {
	s, _, _ := startServer(t, func(o *Options) {
		o.BatchServices = map[uint16]BatchDataHandler{9002: func(context.Context, SessionInfo, netip.AddrPort, []byte) ([][]byte, error) {
			return [][]byte{{1}, {2}, {3}, {4}, {5}}, nil
		}}
	})
	p := handshake(t, s)
	p.activate(t)
	send(t, p.conn, p.server, p.application(t, netip.AddrPortFrom(s.opts.PublicIP, 9002), []byte{1}))
	noReply(t, p.conn)
	if s.Stats().RejectedPackets == 0 {
		t.Fatal("unbounded batch accepted")
	}
}

func TestGenerationsSurviveWireIDReuseAndOwnersAreDroppedOnce(t *testing.T) {
	removed := make(chan SessionInfo, 8)
	seen := make(chan SessionInfo, 8)
	var server *Server
	s, cancel, done := startServer(t, func(o *Options) {
		o.OnDisconnect = func(info SessionInfo) {
			if server != nil {
				_ = server.Stats()
			}
			removed <- info
		}
		o.Services = map[uint16]DataHandler{9002: func(_ context.Context, info SessionInfo, _ netip.AddrPort, _ []byte) ([]byte, error) {
			seen <- info
			return nil, nil
		}}
	})
	server = s
	first := handshake(t, s)
	first.activate(t)
	call := func(p *peer) SessionInfo {
		send(t, p.conn, p.server, p.application(t, netip.AddrPortFrom(s.opts.PublicIP, 9002), []byte{1}))
		select {
		case info := <-seen:
			return info
		case <-time.After(time.Second):
			t.Fatal("missing service ownership")
			return SessionInfo{}
		}
	}
	owner1 := call(first)
	send(t, first.conn, first.server, first.packet(t, 64, nil))
	select {
	case info := <-removed:
		if info.Generation != owner1.Generation || info.Remote != owner1.Remote {
			t.Fatal("wrong owner removed")
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect callback blocked or absent")
	}
	s.mu.Lock()
	s.nextSession = owner1.ID
	s.mu.Unlock()
	second := handshake(t, s)
	second.activate(t)
	owner2 := call(second)
	if owner2.ID != owner1.ID || owner2.Generation == 0 || owner2.Generation == owner1.Generation {
		t.Fatalf("wire identity reuse %+v %+v", owner1, owner2)
	}
	cancel()
	s.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
	select {
	case info := <-removed:
		if info.Generation != owner2.Generation {
			t.Fatal("wrong shutdown owner")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not remove owner")
	}
	select {
	case info := <-removed:
		t.Fatalf("duplicate cleanup %+v", info)
	default:
	}
}

func TestExpiredTransportRemovesOwnedState(t *testing.T) {
	removed := make(chan SessionInfo, 2)
	s, _, _ := startServer(t, func(o *Options) {
		o.HandshakeTTL = 25 * time.Millisecond
		o.OnDisconnect = func(info SessionInfo) { removed <- info }
	})
	p := handshake(t, s) // Established keys use SessionTTL, so test a separate pending hello.
	_ = p
	// A completed handshake must use the established lifetime, not HandshakeTTL.
	select {
	case <-removed:
		t.Fatal("established session expired on handshake timeout")
	case <-time.After(40 * time.Millisecond):
	}
	s.mu.Lock()
	for _, c := range s.connections {
		c.lastSeen = time.Now().Add(-s.opts.SessionTTL - time.Second)
	}
	s.mu.Unlock()
	select {
	case info := <-removed:
		if info.Generation == 0 {
			t.Fatal("missing expired generation")
		}
	case <-time.After(time.Second):
		t.Fatal("expired session not cleaned")
	}
}

func TestCancellationBeforeServeReleasesBothSockets(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		s, err := New(Options{MasterBind: netip.MustParseAddrPort("127.0.0.1:0"), ProxyBind: netip.MustParseAddrPort("127.0.0.1:0"), PublicIP: netip.MustParseAddr("127.0.0.1")})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		addresses := []netip.AddrPort{s.MasterAddr(), s.ProxyAddr()}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if closeFirst {
			s.Close()
		}
		if err := s.Serve(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("closeFirst=%v: canceled startup returned %v", closeFirst, err)
		}
		for _, address := range addresses {
			conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(address))
			if err != nil {
				t.Fatal("canceled startup retained its socket", err)
			}
			conn.Close()
		}
	}
}
