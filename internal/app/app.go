// Package app owns Quetzal configuration, listeners and service lifetimes.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/capture"
	"github.com/princebyyourlogic/quetzal/internal/game"
)

type Config struct {
	Bind       string
	HTTPPort   int
	CaptureDir string
	PublicIP   netip.Addr
	StateDir   string
	LogLevel   slog.Level
	Games      []string
}

func LoadConfig() (Config, error) {
	c := Config{Bind: "127.0.0.1", HTTPPort: 8082, StateDir: "data/state", Games: []string{"25tolife"}}
	if s := os.Getenv("QUETZAL_BIND"); s != "" {
		c.Bind = s
	}
	if s := os.Getenv("QUETZAL_HTTP_PORT"); s != "" {
		p, err := strconv.Atoi(s)
		if err != nil || p < 1 || p > 65535 {
			return c, fmt.Errorf("invalid QUETZAL_HTTP_PORT %q", s)
		}
		c.HTTPPort = p
	}
	if s := os.Getenv("QUETZAL_CAPTURE_DIR"); s != "" {
		c.CaptureDir = s
	}
	if s := os.Getenv("QUETZAL_LOG_LEVEL"); s != "" {
		if err := c.LogLevel.UnmarshalText([]byte(s)); err != nil {
			return c, fmt.Errorf("invalid QUETZAL_LOG_LEVEL: %w", err)
		}
	}
	a, err := netip.ParseAddr(c.Bind)
	if err != nil || !a.Is4() {
		return c, fmt.Errorf("QUETZAL_BIND must be an IPv4 address")
	}
	c.PublicIP = a
	if value := os.Getenv("QUETZAL_PUBLIC_IP"); value != "" {
		c.PublicIP, err = netip.ParseAddr(value)
		if err != nil || !c.PublicIP.Is4() {
			return c, fmt.Errorf("QUETZAL_PUBLIC_IP must be an IPv4 address")
		}
	}
	if c.PublicIP.IsUnspecified() {
		return c, fmt.Errorf("QUETZAL_PUBLIC_IP is required with a wildcard bind")
	}
	if value := os.Getenv("QUETZAL_STATE_DIR"); value != "" {
		c.StateDir = value
	}
	if value := os.Getenv("QUETZAL_GAMES"); value != "" {
		c.Games = nil
		seen := make(map[string]bool)
		for _, item := range strings.Split(value, ",") {
			key := strings.TrimSpace(item)
			if !validKey(key) || seen[key] {
				return c, fmt.Errorf("invalid or duplicate game in QUETZAL_GAMES: %q", key)
			}
			seen[key] = true
			c.Games = append(c.Games, key)
		}
	}
	return c, nil
}

func (c Config) HTTPAddress() string { return net.JoinHostPort(c.Bind, strconv.Itoa(c.HTTPPort)) }

// Run preserves the single-tenant API for existing adapters and callers.
func Run(ctx context.Context, c Config, def game.Definition, log *slog.Logger) error {
	return RunTenants(ctx, c, []game.Definition{def}, log)
}

// RunTenants binds every endpoint before starting any tenant. A failure shuts
// down the whole instance; callers retain state locks until this function joins
// every handler. Shared packages never select an adapter based on untrusted RPC.
func RunTenants(ctx context.Context, c Config, defs []game.Definition, log *slog.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	if log == nil {
		log = slog.Default()
	}
	var sockets []*net.UDPConn
	var services []game.UDPService
	var wg sync.WaitGroup
	var httpHandlers sync.WaitGroup
	var httpAdmission sync.Mutex
	httpStopping := false
	var httpServer *http.Server
	var httpListener net.Listener
	var recorders []*capture.Recorder
	for _, def := range defs {
		services = append(services, def.Auxiliary...)
	}
	defer func() {
		cancel()
		httpAdmission.Lock()
		httpStopping = true
		httpAdmission.Unlock()
		for _, service := range services {
			if service != nil {
				_ = service.Close()
			}
		}
		if httpServer != nil {
			_ = httpServer.Close()
		}
		if httpListener != nil {
			_ = httpListener.Close()
		}
		for _, conn := range sockets {
			_ = conn.Close()
		}
		wg.Wait()
		httpHandlers.Wait()
		for _, recorder := range recorders {
			_ = recorder.Close()
		}
	}()
	if err := ValidateTenants(c, defs); err != nil {
		return err
	}
	type tenant struct {
		def     game.Definition
		conns   []*net.UDPConn
		observe game.Observe
	}
	tenants := make([]tenant, len(defs))
	for i, def := range defs {
		tenants[i].def = def
		bind := def.Bind
		if bind == "" {
			bind = c.Bind
		}
		for _, port := range def.UDPPorts {
			address := &net.UDPAddr{IP: net.ParseIP(bind), Port: port}
			conn, err := net.ListenUDP("udp4", address)
			if err != nil {
				return fmt.Errorf("tenant %s listen %s: %w", def.Key, address, err)
			}
			sockets = append(sockets, conn)
			tenants[i].conns = append(tenants[i].conns, conn)
		}
	}
	// Ephemeral binds must also respect lazy reservations. Constructors have
	// not run yet, so any conflict still closes every socket before readiness.
	boundDefs := append([]game.Definition(nil), defs...)
	for i, t := range tenants {
		boundDefs[i].UDPPorts = nil
		for _, conn := range t.conns {
			boundDefs[i].UDPPorts = append(boundDefs[i].UDPPorts, conn.LocalAddr().(*net.UDPAddr).Port)
		}
	}
	if err := ValidateTenants(c, boundDefs); err != nil {
		return err
	}
	var err error
	httpListener, err = net.Listen("tcp4", c.HTTPAddress())
	if err != nil {
		return fmt.Errorf("HTTP listener: %w", err)
	}
	for i := range tenants {
		t := &tenants[i]
		var recorder *capture.Recorder
		if c.CaptureDir != "" {
			dir := c.CaptureDir
			if len(tenants) > 1 {
				dir = filepath.Join(dir, t.def.Key)
			}
			recorder, err = capture.Open(dir, 16*1024*1024)
			if err != nil {
				return fmt.Errorf("tenant %s capture: %w", t.def.Key, err)
			}
			recorders = append(recorders, recorder)
		}
		key := t.def.Key
		t.observe = func(direction string, local, remote netip.AddrPort, wire []byte) error {
			if recorder != nil {
				if err := recorder.RecordDirection(direction, key, local.String(), remote.String(), wire); err != nil {
					return fmt.Errorf("tenant %s capture: %w", key, err)
				}
			}
			log.Debug("udp_packet", "direction", direction, "tenant", key, "game", key, "local", local.String(), "remote", remote.String(), "bytes", len(wire))
			return nil
		}
		if t.def.NewServices != nil {
			group, err := t.def.NewServices(t.conns, t.observe)
			services = append(services, group...)
			if err != nil {
				return fmt.Errorf("tenant %s services: %w", key, err)
			}
			for _, s := range group {
				if s == nil {
					return fmt.Errorf("tenant %s returned a nil service", key)
				}
			}
		} else if t.def.NewUDP != nil {
			for _, conn := range t.conns {
				s, err := t.def.NewUDP(conn, t.observe)
				if s != nil {
					services = append(services, s)
				}
				if err != nil {
					return fmt.Errorf("tenant %s service: %w", key, err)
				}
				if s == nil {
					return fmt.Errorf("tenant %s returned a nil service", key)
				}
			}
		} else {
			for _, conn := range t.conns {
				services = append(services, &captureService{conn: conn, observe: t.observe})
			}
		}
	}
	// Sized for every producer, including failures that arrive during cleanup.
	errCh := make(chan error, len(services)+1)
	mux := http.NewServeMux()
	type healthTenant struct {
		Key    string   `json:"tenant"`
		Game   string   `json:"game"`
		Status string   `json:"status"`
		UDP    []string `json:"udp"`
	}
	health := struct {
		Status  string         `json:"status"`
		Game    string         `json:"game,omitempty"`
		Tenants []healthTenant `json:"tenants"`
	}{Status: "multi_tenant"}
	for _, t := range tenants {
		mode := t.def.Mode
		if mode == "" {
			mode = "capture_only"
		}
		adapter := t.def.Game
		if adapter == "" {
			adapter = t.def.Key
		}
		item := healthTenant{Key: t.def.Key, Game: adapter, Status: mode, UDP: []string{}}
		for _, conn := range t.conns {
			item.UDP = append(item.UDP, conn.LocalAddr().String())
		}
		health.Tenants = append(health.Tenants, item)
		handler := t.def.HTTP
		if handler == nil {
			handler = http.NotFoundHandler()
		}
		prefix := "/games/" + t.def.Key + "/"
		mux.Handle(prefix, http.StripPrefix(strings.TrimSuffix(prefix, "/"), handler))
		for _, path := range t.def.HTTPPaths {
			mux.Handle(path, handler)
		}
		if len(tenants) == 1 {
			health.Status = mode
			health.Game = t.def.Key
			mux.Handle("/", handler)
		}
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(health)
	})
	trackedHTTP := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpAdmission.Lock()
		if httpStopping {
			httpAdmission.Unlock()
			http.Error(w, "server stopping", http.StatusServiceUnavailable)
			return
		}
		httpHandlers.Add(1)
		httpAdmission.Unlock()
		defer httpHandlers.Done()
		mux.ServeHTTP(w, r)
	})
	httpServer = &http.Server{Handler: trackedHTTP, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	wg.Add(1)
	go func() { defer wg.Done(); errCh <- httpServer.Serve(httpListener) }()
	for _, service := range services {
		wg.Add(1)
		go func(s game.UDPService) { defer wg.Done(); errCh <- s.Serve(ctx) }(service)
	}
	log.Info("ready", "tenants", health.Tenants, "http", httpListener.Addr().String(), "bind", c.Bind, "capture_dir", c.CaptureDir)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

type captureService struct {
	conn    *net.UDPConn
	observe game.Observe
}

func (s *captureService) Close() error { return s.conn.Close() }
func (s *captureService) Serve(ctx context.Context) error {
	wire := make([]byte, 65535)
	for {
		n, remote, err := s.conn.ReadFromUDPAddrPort(wire)
		if err != nil {
			return err
		}
		if err := s.observe("rx", s.conn.LocalAddr().(*net.UDPAddr).AddrPort(), remote, wire[:n]); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
