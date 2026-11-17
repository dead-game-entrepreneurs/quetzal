package nativeproxy

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/proxycrypto"
)

func TestPreboundSocketsAndCompleteCapture(t *testing.T) {
	master, proxy := socket(t), socket(t)
	masterAddr := master.LocalAddr().(*net.UDPAddr).AddrPort()
	proxyAddr := proxy.LocalAddr().(*net.UDPAddr).AddrPort()
	type observed struct {
		direction     string
		local, remote netip.AddrPort
		wire          []byte
	}
	var mu sync.Mutex
	var captures []observed
	s, err := New(Options{MasterConn: master, ProxyConn: proxy, PublicIP: netip.MustParseAddr("127.0.0.1"), CleanupInterval: 10 * time.Millisecond,
		// Supplied sockets make these bind choices irrelevant.
		MasterBind: netip.MustParseAddrPort("[::1]:8124"), ProxyBind: netip.AddrPort{},
		Observe: func(direction string, local, remote netip.AddrPort, wire []byte) error {
			mu.Lock()
			defer mu.Unlock()
			captures = append(captures, observed{direction, local, remote, bytes.Clone(wire)})
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.MasterAddr() != masterAddr || s.ProxyAddr() != proxyAddr {
		t.Fatal("rebound supplied sockets")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(func() { cancel(); s.Close() })
	client := socket(t)
	request := (proxycrypto.MasterRequest{Region: proxycrypto.RegionUS, Revision: proxycrypto.RetailRevision}).MarshalBinary()
	send(t, client, s.MasterAddr(), request)
	receive(t, client)
	p := handshake(t, s)
	p.activate(t)
	// Oversized packets are invalid protocol, but must be captured without the
	// old MaxDatagram+1 read-buffer truncation. Malformed proxy traffic too.
	oversized := bytes.Repeat([]byte{0x67}, 4096)
	send(t, client, s.MasterAddr(), oversized)
	noReply(t, client)
	malformed := []byte{1, 2, 3}
	send(t, client, s.ProxyAddr(), malformed)
	noReply(t, client)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server failed to stop")
	}
	mu.Lock()
	defer mu.Unlock()
	counts := make(map[string]int)
	oversizeSeen, malformedSeen := false, false
	for _, capture := range captures {
		if capture.direction != "rx" && capture.direction != "tx" {
			t.Fatal("bad direction")
		}
		if capture.local != masterAddr && capture.local != proxyAddr {
			t.Fatalf("wrong local endpoint %v", capture.local)
		}
		if capture.remote != client.LocalAddr().(*net.UDPAddr).AddrPort() && capture.remote != p.conn.LocalAddr().(*net.UDPAddr).AddrPort() {
			t.Fatalf("wrong remote endpoint %v", capture.remote)
		}
		counts[capture.direction+capture.local.String()]++
		if capture.direction == "rx" && capture.local == masterAddr && bytes.Equal(capture.wire, oversized) {
			oversizeSeen = true
		}
		if capture.direction == "rx" && capture.local == proxyAddr && bytes.Equal(capture.wire, malformed) {
			malformedSeen = true
		}
	}
	for _, local := range []netip.AddrPort{masterAddr, proxyAddr} {
		for _, direction := range []string{"rx", "tx"} {
			if counts[direction+local.String()] == 0 {
				t.Fatalf("missing %s capture for %v", direction, local)
			}
		}
	}
	if !oversizeSeen || !malformedSeen {
		t.Fatal("capture omitted or truncated invalid packet")
	}
	// Successful construction transfers service lifetime: Close releases both.
	for _, addr := range []netip.AddrPort{masterAddr, proxyAddr} {
		conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(addr))
		if err != nil {
			t.Fatalf("prebound socket not closed: %v", err)
		}
		conn.Close()
	}
}

func TestPreboundConstructionFailureRetainsCallerSockets(t *testing.T) {
	master, proxy := socket(t), socket(t)
	opts := Options{MasterConn: master, ProxyConn: proxy, PublicIP: netip.MustParseAddr("127.0.0.1"), MaxSessions: -1}
	if s, err := New(opts); err == nil {
		s.Close()
		t.Fatal("accepted invalid options")
	}
	assertOpen := func(conn *net.UDPConn) {
		t.Helper()
		client := socket(t)
		send(t, client, conn.LocalAddr().(*net.UDPAddr).AddrPort(), []byte("still owned by app"))
		if b := receive(t, conn); !bytes.Equal(b, []byte("still owned by app")) {
			t.Fatal("caller socket unusable")
		}
	}
	assertOpen(master)
	assertOpen(proxy)
	opts.MaxSessions = 0
	opts.ProxyConn = nil
	opts.ProxyBind = proxy.LocalAddr().(*net.UDPAddr).AddrPort()
	if s, err := New(opts); err == nil {
		s.Close()
		t.Fatal("accepted occupied proxy bind")
	}
	assertOpen(master)
	opts.ProxyConn = master
	if s, err := New(opts); err == nil {
		s.Close()
		t.Fatal("accepted same socket for both roles")
	}
	assertOpen(master)
	connected, err := net.DialUDP("udp4", nil, master.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer connected.Close()
	opts.ProxyConn = connected
	if s, err := New(opts); err == nil {
		s.Close()
		t.Fatal("accepted connected socket")
	}
	assertOpen(master)
}

func TestObserveFailureStopsBothNativeEndpoints(t *testing.T) {
	for _, role := range []string{"master", "proxy"} {
		for _, direction := range []string{"rx", "tx"} {
			t.Run(role+"/"+direction, func(t *testing.T) {
				captureFailure := errors.New("capture storage failed")
				var callbacks int
				s, _, done := startServer(t, func(o *Options) {
					o.Observe = func(observedDirection string, local, remote netip.AddrPort, wire []byte) error {
						callbacks++
						if observedDirection == direction {
							return captureFailure
						}
						return nil
					}
				})
				client := socket(t)
				if role == "master" {
					send(t, client, s.MasterAddr(), (proxycrypto.MasterRequest{Region: proxycrypto.RegionUS, Revision: proxycrypto.RetailRevision}).MarshalBinary())
				} else {
					send(t, client, s.ProxyAddr(), proxycrypto.EncodeClientHello(proxycrypto.RegionUS, [32]byte{}))
				}
				select {
				case err := <-done:
					if !errors.Is(err, captureFailure) {
						t.Fatalf("lost observer error: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("capture failure did not stop server")
				}
				noReply(t, client)
				if callbacks == 0 {
					t.Fatal("observer not invoked")
				}
				if direction == "rx" && s.Stats().Sessions != 0 {
					t.Fatal("processed uncaptured input")
				}
				for _, addr := range []netip.AddrPort{s.MasterAddr(), s.ProxyAddr()} {
					conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(addr))
					if err != nil {
						t.Fatalf("capture failure leaked socket: %v", err)
					}
					conn.Close()
				}
			})
		}
	}
}
