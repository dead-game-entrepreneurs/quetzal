// Package app owns Quetzal configuration, listeners and service lifetimes.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
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
}

func LoadConfig() (Config, error) {
	c := Config{Bind: "127.0.0.1", HTTPPort: 8082, StateDir: "data/state"}
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
	return c, nil
}

func (c Config) HTTPAddress() string { return net.JoinHostPort(c.Bind, strconv.Itoa(c.HTTPPort)) }

func Run(ctx context.Context, c Config, def game.Definition, log *slog.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	var sockets []*net.UDPConn
	services := append([]game.UDPService(nil), def.Auxiliary...)
	var udpServices []game.UDPService
	var wg sync.WaitGroup
	var httpServer *http.Server
	var httpListener net.Listener
	var recorder *capture.Recorder
	defer func() {
		cancel()
		for _, service := range services {
			service.Close()
		}
		if httpServer != nil {
			httpServer.Close()
		}
		if httpListener != nil {
			httpListener.Close()
		}
		for _, conn := range sockets {
			conn.Close()
		}
		wg.Wait()
		if recorder != nil {
			recorder.Close()
		}
	}()
	for _, port := range def.UDPPorts {
		address := &net.UDPAddr{IP: net.ParseIP(c.Bind), Port: port}
		conn, err := net.ListenUDP("udp4", address)
		if err != nil {
			return fmt.Errorf("listen %s: %w", address, err)
		}
		sockets = append(sockets, conn)
	}
	var err error
	httpListener, err = net.Listen("tcp4", c.HTTPAddress())
	if err != nil {
		return fmt.Errorf("HTTP listener: %w", err)
	}
	if c.CaptureDir != "" {
		recorder, err = capture.Open(c.CaptureDir, 16*1024*1024)
		if err != nil {
			return fmt.Errorf("capture: %w", err)
		}
	}
	if def.NewUDP != nil {
		observe := func(direction string, local, remote netip.AddrPort, wire []byte) error {
			if recorder != nil {
				if err := recorder.RecordDirection(direction, def.Key, local.String(), remote.String(), wire); err != nil {
					return fmt.Errorf("capture: %w", err)
				}
			}
			log.Debug("udp_packet", "direction", direction, "game", def.Key, "local", local.String(), "remote", remote.String(), "bytes", len(wire))
			return nil
		}
		for _, conn := range sockets {
			service, err := def.NewUDP(conn, observe)
			if err != nil {
				return err
			}
			services = append(services, service)
			udpServices = append(udpServices, service)
		}
	}
	errCh := make(chan error, len(sockets)+len(def.Auxiliary)+1)
	mux := http.NewServeMux()
	mode := def.Mode
	if mode == "" {
		mode = "capture_only"
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"status\":%q,\"game\":%q}\n", mode, def.Key)
	})
	mux.Handle("/", def.HTTP)
	httpServer = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	wg.Add(1)
	go func() { defer wg.Done(); errCh <- httpServer.Serve(httpListener) }()
	for _, service := range def.Auxiliary {
		wg.Add(1)
		go func() { defer wg.Done(); errCh <- service.Serve(ctx) }()
	}
	for i, conn := range sockets {
		wg.Add(1)
		if len(udpServices) > 0 {
			service := udpServices[i]
			go func() { defer wg.Done(); errCh <- service.Serve(ctx) }()
			continue
		}
		go func(conn *net.UDPConn) {
			defer wg.Done()
			payload := make([]byte, 65535)
			for {
				n, remote, err := conn.ReadFromUDPAddrPort(payload)
				if err != nil {
					errCh <- err
					return
				}
				if recorder != nil {
					if err := recorder.Record(def.Key, conn.LocalAddr().String(), remote.String(), payload[:n]); err != nil {
						errCh <- fmt.Errorf("capture: %w", err)
						return
					}
				}
				log.Debug("udp_received", "game", def.Key, "local", conn.LocalAddr().String(),
					"remote", remote.String(), "bytes", n)
			}
		}(conn)
	}
	log.Info("ready", "game", def.Key, "http", httpListener.Addr().String(), "udp_ports", def.UDPPorts,
		"bind", c.Bind, "mode", mode, "capture_dir", c.CaptureDir)
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
