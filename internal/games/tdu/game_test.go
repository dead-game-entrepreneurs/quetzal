package tdu

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/game"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/proxycrypto"
	"github.com/princebyyourlogic/quetzal/internal/state"
)

type Addresses struct{ Auth, Secure, Master, Proxy netip.AddrPort }

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func localConfig(t *testing.T) Config {
	t.Helper()
	c := DefaultConfig()
	c.Bind = netip.MustParseAddr("127.0.0.1")
	c.PublicIP = netip.MustParseAddr("192.0.2.10")
	c.StateDir = t.TempDir()
	c.AuthPort, c.SecurePort, c.MasterPort, c.ProxyPort = 0, 0, 0, 0
	return c
}

// This helper performs only the application's dependency/lifecycle contract.
// It exercises the adapter's actual shared transports without importing app.
func startAdapter(t *testing.T, c Config) Addresses {
	t.Helper()
	lock, key, err := state.Open(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	def, err := Definition(quietLog(), c, key)
	if err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if def.Key != "tdu" || def.Game != "tdu" || len(def.UDPPorts) != 4 || def.NewServices == nil || def.NewUDP != nil || len(def.ReservedUDP) != 0 {
		lock.Close()
		t.Fatal("invalid adapter composition")
	}
	var sockets []*net.UDPConn
	for _, port := range def.UDPPorts {
		conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(c.Bind, uint16(port))))
		if err != nil {
			for _, conn := range sockets {
				conn.Close()
			}
			lock.Close()
			t.Fatal(err)
		}
		sockets = append(sockets, conn)
	}
	services, err := def.NewServices(sockets, nil)
	if err != nil {
		for _, conn := range sockets {
			conn.Close()
		}
		lock.Close()
		t.Fatal(err)
	}
	if len(services) != 3 {
		t.Fatal("TDU must compose two PRUDP services and one native service")
	}
	addresses := Addresses{sockets[0].LocalAddr().(*net.UDPAddr).AddrPort(), sockets[1].LocalAddr().(*net.UDPAddr).AddrPort(), sockets[2].LocalAddr().(*net.UDPAddr).AddrPort(), sockets[3].LocalAddr().(*net.UDPAddr).AddrPort()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, len(services))
	for _, service := range services {
		go func(s game.UDPService) { done <- s.Serve(ctx) }(service)
	}
	t.Cleanup(func() {
		cancel()
		for _, service := range services {
			service.Close()
		}
		for range services {
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
					t.Error(err)
				}
			case <-time.After(2 * time.Second):
				t.Error("adapter service did not stop")
			}
		}
		for _, conn := range sockets {
			conn.Close()
		}
		lock.Close()
	})
	return addresses
}
func TestAdapterMasterUsesAppBoundSockets(t *testing.T) {
	cfg := localConfig(t)
	addresses := startAdapter(t, cfg)
	conn, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(addresses.Master))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	request := (proxycrypto.MasterRequest{Region: proxycrypto.RegionUS, Revision: proxycrypto.RetailRevision}).MarshalBinary()
	if _, err = conn.Write(request); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 32)
	n, err := conn.Read(b)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := proxycrypto.ParseMasterReply(b[:n])
	if err != nil || reply.Proxy != cfg.PublicIP || reply.DirectoryIP != cfg.PublicIP {
		t.Fatal(reply, err)
	}
}
func TestAdapterRejectsInvalidFactories(t *testing.T) {
	cfg := localConfig(t)
	key, err := state.PrincipalKey(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	// Definition needs a persisted key but does not acquire the tenant lock itself.
	key[0] |= 1
	def, err := Definition(quietLog(), cfg, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = def.NewServices(nil, nil); err == nil {
		t.Fatal("accepted missing sockets")
	}
	if _, err = def.NewServices(make([]*net.UDPConn, 4), nil); err == nil {
		t.Fatal("accepted nil sockets")
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = def.NewServices([]*net.UDPConn{conn, conn, conn, conn}, nil); err == nil {
		t.Fatal("accepted duplicate sockets")
	}
}
