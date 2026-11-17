package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/app"
	"github.com/princebyyourlogic/quetzal/internal/game"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu"
	"github.com/princebyyourlogic/quetzal/internal/games/ttl25"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "check the configured HTTP listener and exit")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := app.LoadConfig()
	if err != nil {
		log.Error("configuration", "err", err)
		os.Exit(2)
	}
	log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if *healthcheck {
		client := http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://" + cfg.HTTPAddress() + "/healthz")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runServer(ctx, cfg, log); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("exiting", "err", err)
		os.Exit(1)
	}
	log.Info("stopped")
}

// Tenant state is isolated even though transport/auth code is shared. The first
// adapter retains its existing location so enabling a new game does not rename
// or hide working accounts, community data or its principal key.
func tenantStateDir(root, key string) string {
	if key == "25tolife" {
		return root
	}
	return filepath.Join(root, "tenants", key)
}

func runServer(ctx context.Context, cfg app.Config, log *slog.Logger) error {
	defs, closeTenants, err := buildTenants(cfg, log)
	if err != nil {
		return err
	}
	defer closeTenants()
	return app.RunTenants(ctx, cfg, defs, log)
}

func buildTenants(cfg app.Config, log *slog.Logger) (defs []game.Definition, cleanup func(), err error) {
	var locks []*os.File
	cleanup = func() {
		for _, def := range defs {
			for _, service := range def.Auxiliary {
				if service != nil {
					_ = service.Close()
				}
			}
		}
		for i := len(locks) - 1; i >= 0; i-- {
			_ = locks[i].Close()
		}
	}
	failed := true
	defer func() {
		if failed {
			cleanup()
		}
	}()
	keys := cfg.Games
	if len(keys) == 0 {
		keys = []string{"25tolife"}
	}
	// Reject unknown adapters before creating any state.
	seen := map[string]bool{}
	for _, key := range keys {
		if key != "25tolife" && key != "tdu" {
			return nil, cleanup, fmt.Errorf("unknown game %q; available: 25tolife,tdu", key)
		}
		if seen[key] {
			return nil, cleanup, fmt.Errorf("duplicate game %q", key)
		}
		seen[key] = true
	}
	for _, key := range keys {
		dir := tenantStateDir(cfg.StateDir, key)
		lock, principal, openErr := app.OpenState(dir)
		if openErr != nil {
			return defs, cleanup, fmt.Errorf("tenant %s state: %w", key, openErr)
		}
		locks = append(locks, lock)
		tenantLog := log.With("tenant", key, "game", key)
		var def game.Definition
		switch key {
		case "25tolife":
			relay, loadErr := ttl25.LoadRelayConfig(cfg.Bind, cfg.PublicIP)
			if loadErr != nil {
				return defs, cleanup, fmt.Errorf("tenant %s relay: %w", key, loadErr)
			}
			def, err = ttl25.Definition(tenantLog, cfg.PublicIP, dir, principal, relay)
		case "tdu":
			config, loadErr := tdu.LoadConfig(cfg.Bind, cfg.PublicIP, dir)
			if loadErr != nil {
				return defs, cleanup, fmt.Errorf("tenant %s config: %w", key, loadErr)
			}
			def, err = tdu.Definition(tenantLog, config, principal)
		}
		if err != nil {
			return defs, cleanup, fmt.Errorf("tenant %s: %w", key, err)
		}
		defs = append(defs, def)
	}
	if err = app.ValidateTenants(cfg, defs); err != nil {
		return defs, cleanup, err
	}
	failed = false
	return defs, cleanup, nil
}
