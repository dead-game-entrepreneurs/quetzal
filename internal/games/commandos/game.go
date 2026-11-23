// Package commandos implements the US PS2 Commandos Strike Force backend.
package commandos

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"

	"github.com/princebyyourlogic/quetzal/internal/game"
	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal/peerrelay"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

const signupPrincipal = "**CuentaCrearCSFv1**"

func Definition(log *slog.Logger, c Config, key quazalcrypto.Key) (game.Definition, error) {
	if err := c.Validate(); err != nil {
		return game.Definition{}, err
	}
	if key == (quazalcrypto.Key{}) {
		return game.Definition{}, errors.New("Commandos principal key must not be zero")
	}
	store, err := quazal.OpenStore(filepath.Join(c.StateDir, "accounts.json"))
	if err != nil {
		return game.Definition{}, err
	}
	lobbies := newSessions()
	var auxiliary []game.UDPService
	var reserved []game.PortRange
	if c.RelayMode != "direct" {
		relay, err := peerrelay.New(peerrelay.Options{Config: c.relayConfig(), Log: log})
		if err != nil {
			return game.Definition{}, err
		}
		lobbies.relay = relay
		lobbies.relayMode = c.RelayMode
		auxiliary = append(auxiliary, relay)
		reserved = append(reserved, game.PortRange{First: int(c.RelayFirstPort), Last: int(c.RelayLastPort)})
	}
	diagnostics := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/connectivity" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"game": "commandos", "status": "backend_experimental", "sessions": lobbies.count(), "payload_mode": c.PayloadMode, "retail_validated": false, "peer_connectivity": c.RelayMode, "relay": lobbies.relay.Snapshot()})
	})
	return game.Definition{Key: "commandos", Game: "commandos", Bind: c.Bind.String(), UDPPorts: []int{int(c.AuthPort), int(c.SecurePort)}, HTTP: diagnostics, Mode: "backend_experimental", Auxiliary: auxiliary, ReservedUDP: reserved,
		NewServices: func(sockets []*net.UDPConn, observe game.Observe) ([]game.UDPService, error) {
			if len(sockets) != 2 {
				return nil, errors.New("Commandos requires auth and secure sockets")
			}
			for i, conn := range sockets {
				if conn == nil {
					return nil, errors.New("Commandos socket is nil")
				}
				expected := []uint16{c.AuthPort, c.SecurePort}[i]
				addr, ok := conn.LocalAddr().(*net.UDPAddr)
				if !ok || (expected != 0 && addr.Port != int(expected)) {
					return nil, errors.New("Commandos sockets are not in configured order")
				}
			}
			options := quazal.Options{PublicIP: c.PublicIP, ServerPID: 2, PrincipalKey: key, Store: store, Log: log,
				Signup: &quazal.SignupPolicy{Name: signupPrincipal, Password: signupPrincipal, Parameter: 0}, EnablePasswordChange: true,
				GameRPC: lobbies.handle, GameRegistered: lobbies.registered, GameDisconnect: lobbies.disconnect, GameProbe: lobbies.probe}
			if lobbies.relay != nil {
				options.GameProbeInstructions = lobbies.relay.DrainInstructions
			}
			transport := prudp.Options{ChecksumSeed: 0xa6, Log: log, Observe: observe, RawPayload: c.PayloadMode == "raw", PlainPayload: c.PayloadMode == "plain"}
			endpoints, err := quazal.NewEndpoints(sockets, options, [2]prudp.Options{transport, transport})
			if err != nil {
				return nil, err
			}
			return []game.UDPService{endpoints[0], endpoints[1]}, nil
		}}, nil
}
