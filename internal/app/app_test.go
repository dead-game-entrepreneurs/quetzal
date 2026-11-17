package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/game"
)

func TestConfigPrivateDefaultsAndExplicitDiagnostics(t *testing.T) {
	for _, key := range []string{"QUETZAL_BIND", "QUETZAL_PUBLIC_IP", "QUETZAL_HTTP_PORT", "QUETZAL_CAPTURE_DIR", "QUETZAL_STATE_DIR", "QUETZAL_LOG_LEVEL"} {
		t.Setenv(key, "")
	}
	c, err := LoadConfig()
	if err != nil || c.CaptureDir != "" || c.StateDir != "data/state" || c.LogLevel != slog.LevelInfo {
		t.Fatalf("private defaults: %+v, %v", c, err)
	}
	t.Setenv("QUETZAL_CAPTURE_DIR", "data/captures")
	t.Setenv("QUETZAL_LOG_LEVEL", "debug")
	c, err = LoadConfig()
	if err != nil || c.CaptureDir != "data/captures" || c.LogLevel != slog.LevelDebug {
		t.Fatalf("explicit diagnostics: %+v, %v", c, err)
	}
	t.Setenv("QUETZAL_LOG_LEVEL", "invalid")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("invalid log level accepted")
	}
}

type observedService struct{}

func (s *observedService) Serve(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
func (s *observedService) Close() error                    { return nil }

func TestRunWithoutRawCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	observed := make(chan error, 1)
	def := game.Definition{Key: "test", UDPPorts: []int{0}, HTTP: http.NotFoundHandler(),
		NewUDP: func(_ *net.UDPConn, observe func(string, netip.AddrPort, netip.AddrPort, []byte) error) (game.UDPService, error) {
			address := netip.MustParseAddrPort("127.0.0.1:1234")
			observed <- observe("rx", address, address, []byte("synthetic packet"))
			return &observedService{}, nil
		}}
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, Config{Bind: "127.0.0.1"}, def, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	select {
	case err := <-observed:
		if err != nil {
			t.Fatalf("packet observation without capture: %v", err)
		}
	case err := <-result:
		t.Fatalf("server stopped before observation: %v", err)
	case <-ctx.Done():
		t.Fatal("server did not observe packet")
	}
	cancel()
	if err := <-result; err != context.Canceled {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestBindFailureReleasesEarlierListeners(t *testing.T) {
	first, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	firstAddr := first.LocalAddr().(*net.UDPAddr)
	first.Close()
	busy, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	def := game.Definition{Key: "test", UDPPorts: []int{firstAddr.Port, busy.LocalAddr().(*net.UDPAddr).Port}}
	if err := Run(context.Background(), Config{Bind: "127.0.0.1", HTTPPort: 0, CaptureDir: t.TempDir()}, def, log); err == nil {
		t.Fatal("expected a bind conflict")
	}
	again, err := net.ListenUDP("udp4", firstAddr)
	if err != nil {
		t.Fatalf("listener leaked: %v", err)
	}
	again.Close()
}
