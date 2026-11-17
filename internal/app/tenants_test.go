package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/game"
)

type readyHandler struct{ ready chan string }

func (h readyHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h readyHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Message == "ready" {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "http" {
				h.ready <- a.Value.String()
			}
			return true
		})
	}
	return nil
}
func (h readyHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h readyHandler) WithGroup(string) slog.Handler      { return h }

type runningTenants struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
	http   string
}

func launchTenants(t *testing.T, cfg Config, defs []game.Definition) *runningTenants {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h := &runningTenants{cancel: cancel, done: make(chan struct{})}
	ready := make(chan string, 1)
	go func() { h.err = RunTenants(ctx, cfg, defs, slog.New(readyHandler{ready})); close(h.done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
			if h.err != nil && !errors.Is(h.err, context.Canceled) {
				t.Errorf("tenant shutdown: %v", h.err)
			}
		case <-time.After(3 * time.Second):
			t.Error("tenant shutdown timed out")
		}
	})
	select {
	case address := <-ready:
		h.http = "http://" + address
	case <-h.done:
		t.Fatalf("startup: %v", h.err)
	case <-time.After(3 * time.Second):
		t.Fatal("readiness timeout")
	}
	return h
}

type echoService struct {
	conn    *net.UDPConn
	observe game.Observe
	key     string
}

func (s *echoService) Close() error { return s.conn.Close() }
func (s *echoService) Serve(ctx context.Context) error {
	b := make([]byte, 1024)
	for {
		n, remote, err := s.conn.ReadFromUDPAddrPort(b)
		if err != nil {
			return err
		}
		local := s.conn.LocalAddr().(*net.UDPAddr).AddrPort()
		if err := s.observe("rx", local, remote, b[:n]); err != nil {
			return err
		}
		out := []byte(s.key + ":" + string(b[:n]))
		if err := s.observe("tx", local, remote, out); err != nil {
			return err
		}
		if _, err := s.conn.WriteToUDPAddrPort(out, remote); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
func TestTenantsRouteUDPHTTPAndPrivateCapturesIndependently(t *testing.T) {
	addresses := map[string]netip.AddrPort{}
	var defs []game.Definition
	for _, key := range []string{"alpha", "beta"} {
		defs = append(defs, game.Definition{Key: key, UDPPorts: []int{0}, HTTPPaths: []string{"/" + key + "-public/"}, HTTP: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, key) }),
			NewServices: func(conns []*net.UDPConn, observe game.Observe) ([]game.UDPService, error) {
				addresses[key] = conns[0].LocalAddr().(*net.UDPAddr).AddrPort()
				return []game.UDPService{&echoService{conns[0], observe, key}}, nil
			}})
	}
	captureDir := t.TempDir()
	h := launchTenants(t, Config{Bind: "127.0.0.1", CaptureDir: captureDir}, defs)
	for _, key := range []string{"alpha", "beta"} {
		conn, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(addresses[key]))
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(time.Second))
		if _, err := conn.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 128)
		n, err := conn.Read(b)
		conn.Close()
		if err != nil || string(b[:n]) != key+":hello" {
			t.Fatalf("wrong tenant reply %s: %q %v", key, b[:n], err)
		}
		for _, path := range []string{"/games/" + key + "/example", "/" + key + "-public/example"} {
			response, err := http.Get(h.http + path)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if string(body) != key {
				t.Fatalf("HTTP %s: %q", path, body)
			}
		}
	}
	response, err := http.Get(h.http + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	var health struct {
		Status  string
		Tenants []struct {
			Tenant, Game string
			UDP          []string
		}
	}
	err = json.NewDecoder(response.Body).Decode(&health)
	response.Body.Close()
	if err != nil || health.Status != "multi_tenant" || len(health.Tenants) != 2 {
		t.Fatalf("health %+v %v", health, err)
	}
	for _, tenant := range health.Tenants {
		if len(tenant.UDP) != 1 || tenant.UDP[0] != addresses[tenant.Tenant].String() {
			t.Fatalf("tenant health %+v", tenant)
		}
	}
	h.cancel()
	<-h.done
	for _, key := range []string{"alpha", "beta"} {
		data, err := os.ReadFile(filepath.Join(captureDir, key, "packets.ndjson"))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) != 2 {
			t.Fatalf("capture count %s: %d", key, len(lines))
		}
		for _, line := range lines {
			var record struct{ Game string }
			if err := json.Unmarshal([]byte(line), &record); err != nil || record.Game != key {
				t.Fatalf("cross-tenant capture %s: %s", key, line)
			}
		}
		rebound, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(addresses[key]))
		if err != nil {
			t.Fatalf("tenant socket leaked: %v", err)
		}
		rebound.Close()
	}
}

type startupService struct {
	closed chan struct{}
	once   sync.Once
}

func (s *startupService) Serve(context.Context) error {
	return errors.New("should not run before every tenant is initialized")
}
func (s *startupService) Close() error { s.once.Do(func() { close(s.closed) }); return nil }
func TestTenantInitializationFailureClosesAllBoundSocketsAndServices(t *testing.T) {
	service := &startupService{closed: make(chan struct{})}
	var addresses []netip.AddrPort
	defs := []game.Definition{
		{Key: "first", UDPPorts: []int{0}, NewServices: func(c []*net.UDPConn, _ game.Observe) ([]game.UDPService, error) {
			addresses = append(addresses, c[0].LocalAddr().(*net.UDPAddr).AddrPort())
			return []game.UDPService{service}, nil
		}},
		{Key: "second", UDPPorts: []int{0}, NewServices: func(c []*net.UDPConn, _ game.Observe) ([]game.UDPService, error) {
			addresses = append(addresses, c[0].LocalAddr().(*net.UDPAddr).AddrPort())
			return nil, errors.New("synthetic constructor failure")
		}},
	}
	err := RunTenants(context.Background(), Config{Bind: "127.0.0.1"}, defs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "constructor failure") {
		t.Fatalf("startup failure %v", err)
	}
	select {
	case <-service.closed:
	default:
		t.Fatal("earlier tenant service not closed")
	}
	for _, address := range addresses {
		conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(address))
		if err != nil {
			t.Fatal("leaked socket", err)
		}
		conn.Close()
	}
}
func TestAssignedEphemeralPortCannotOverlapLazyReservation(t *testing.T) {
	constructed := false
	defs := []game.Definition{
		{Key: "relay", ReservedUDP: []game.PortRange{{First: 1024, Last: 65535}}},
		{Key: "ephemeral", UDPPorts: []int{0}, NewServices: func([]*net.UDPConn, game.Observe) ([]game.UDPService, error) { constructed = true; return nil, nil }},
	}
	err := RunTenants(context.Background(), Config{Bind: "127.0.0.1"}, defs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "conflicts") || constructed {
		t.Fatalf("ephemeral preflight: %v, constructed=%v", err, constructed)
	}
}
func TestShutdownWaitsForActiveHTTPGameHandler(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	h := launchTenants(t, Config{Bind: "127.0.0.1"}, []game.Definition{{Key: "test", HTTP: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release; io.WriteString(w, "done") })}})
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		response, err := http.Get(h.http + "/blocking")
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler not started")
	}
	h.cancel()
	select {
	case <-h.done:
		t.Fatal("returned before HTTP game handler completed")
	case <-time.After(20 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	select {
	case <-h.done:
	case <-time.After(time.Second):
		t.Fatal("did not join handler")
	}
	<-clientDone
}
func TestTenantPreflightPortAndHTTPBoundaries(t *testing.T) {
	cfg := Config{Bind: "127.0.0.1"}
	good := []game.Definition{{Key: "ttl", UDPPorts: []int{30120, 30121}, ReservedUDP: []game.PortRange{{First: 30400, Last: 30527}}, HTTPPaths: []string{"/TTL/"}}, {Key: "tdu", UDPPorts: []int{30200, 30201, 8124, 8122}}}
	if err := ValidateTenants(cfg, good); err != nil {
		t.Fatal(err)
	}
	for _, defs := range [][]game.Definition{
		nil, {{Key: "../escape"}}, {{Key: "same"}, {Key: "same"}},
		{{Key: "a", UDPPorts: []int{8122}}, {Key: "b", UDPPorts: []int{8122}}},
		{{Key: "a", ReservedUDP: []game.PortRange{{First: 30200, Last: 30327}}}, {Key: "b", UDPPorts: []int{30201}}},
		{{Key: "a", UDPPorts: []int{30200}}, {Key: "b", ReservedUDP: []game.PortRange{{First: 30200, Last: 30327}}}},
		{{Key: "a", Bind: "0.0.0.0", UDPPorts: []int{8122}}, {Key: "b", Bind: "127.0.0.2", UDPPorts: []int{8122}}},
		{{Key: "a", UDPPorts: []int{-1}}}, {{Key: "a", HTTPPaths: []string{"/healthz"}}}, {{Key: "a", HTTPPaths: []string{"/games/other/"}}},
		{{Key: "a", HTTPPaths: []string{"/bad%zz"}}}, {{Key: "a", HTTPPaths: []string{"/TTL/"}}, {Key: "b", HTTPPaths: []string{"/TTL/sub/"}}},
	} {
		if err := ValidateTenants(cfg, defs); err == nil {
			t.Fatalf("accepted conflict %+v", defs)
		}
	}
	distinct := []game.Definition{{Key: "a", Bind: "127.0.0.1", UDPPorts: []int{8122}}, {Key: "b", Bind: "127.0.0.2", UDPPorts: []int{8122}}}
	if err := ValidateTenants(cfg, distinct); err != nil {
		t.Fatalf("different bind addresses: %v", err)
	}
}
func TestEnabledGameConfiguration(t *testing.T) {
	for _, key := range []string{"QUETZAL_BIND", "QUETZAL_PUBLIC_IP", "QUETZAL_HTTP_PORT", "QUETZAL_LOG_LEVEL", "QUETZAL_STATE_DIR", "QUETZAL_GAMES"} {
		t.Setenv(key, "")
	}
	cfg, err := LoadConfig()
	if err != nil || len(cfg.Games) != 1 || cfg.Games[0] != "25tolife" {
		t.Fatalf("default games %+v %v", cfg.Games, err)
	}
	t.Setenv("QUETZAL_GAMES", "25tolife, tdu")
	cfg, err = LoadConfig()
	if err != nil || strings.Join(cfg.Games, ",") != "25tolife,tdu" {
		t.Fatalf("game selection %+v %v", cfg.Games, err)
	}
	for _, value := range []string{",tdu", "tdu,", "tdu,tdu", "../tdu", "TDU"} {
		t.Setenv("QUETZAL_GAMES", value)
		if _, err := LoadConfig(); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}
