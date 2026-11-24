package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/app"
)

type readinessLog struct {
	slog.Handler
	ready chan string
}

func (h readinessLog) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "ready" {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "http" {
				h.ready <- a.Value.String()
			}
			return true
		})
	}
	return h.Handler.Handle(ctx, r)
}

func TestAllAdaptersServeTogether(t *testing.T) {
	cfg := testTenantsConfig(t)
	cfg.Games = append(cfg.Games, "commandos", "wcc", "snowblind")
	cfg.HTTPPort = 0
	defs, cleanup, err := buildTenants(cfg, testLog())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan string, 1)
	go func() { done <- app.RunTenants(ctx, cfg, defs, slog.New(readinessLog{testLog().Handler(), ready})) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("combined server did not stop")
		}
	})
	var address string
	select {
	case address = <-ready:
	case err := <-done:
		done <- err
		t.Fatalf("combined startup failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("combined readiness timed out")
	}
	client := http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	get := func(path string, target any) {
		t.Helper()
		response, err := client.Get("http://" + address + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s: HTTP %d", path, response.StatusCode)
		}
		if err = json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
	var health struct {
		Status  string
		Tenants []struct {
			Tenant, Game string
			UDP          []string
		}
	}
	get("/healthz", &health)
	if health.Status != "multi_tenant" || len(health.Tenants) != 5 {
		t.Fatalf("health %+v", health)
	}
	for i, want := range []int{5, 4, 2, 2, 2} {
		if len(health.Tenants[i].UDP) != want || health.Tenants[i].Game != cfg.Games[i] {
			t.Fatalf("tenant health %+v", health.Tenants[i])
		}
	}
	for _, key := range []string{"tdu", "commandos", "snowblind"} {
		var result struct {
			Game            string
			RetailValidated bool `json:"retail_validated"`
		}
		get("/games/"+key+"/connectivity", &result)
		if result.Game != key || result.RetailValidated {
			t.Fatalf("wrong diagnostic tenant/status %+v", result)
		}
	}
}
