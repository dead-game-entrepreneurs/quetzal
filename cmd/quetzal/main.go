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
	"syscall"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/app"
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
	stateLock, key, err := app.OpenState(cfg.StateDir)
	if err != nil {
		log.Error("state", "err", err)
		os.Exit(1)
	}
	defer stateLock.Close()
	relayConfig, err := ttl25.LoadRelayConfig(cfg.Bind, cfg.PublicIP)
	if err != nil {
		log.Error("relay configuration", "err", err)
		os.Exit(2)
	}
	definition, err := ttl25.Definition(log, cfg.PublicIP, cfg.StateDir, key, relayConfig)
	if err != nil {
		log.Error("game initialization", "err", err)
		os.Exit(1)
	}
	if err := app.Run(ctx, cfg, definition, log); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("exiting", "err", err)
		os.Exit(1)
	}
	log.Info("stopped")
}
