// Package snowblind implements the US PS2 Project: Snowblind Rendez-Vous services.
package snowblind

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

var rankingSpec = quazal.RankingSpec{Description: "BO3Stats", Name: "BO3Stats"}

func Definition(log *slog.Logger, c Config, key quazalcrypto.Key) (game.Definition, error) {
	if err := c.Validate(); err != nil {
		return game.Definition{}, err
	}
	if key == (quazalcrypto.Key{}) {
		return game.Definition{}, errors.New("Snowblind principal key must not be zero")
	}
	store, err := quazal.OpenStore(filepath.Join(c.StateDir, "accounts.json"))
	if err != nil {
		return game.Definition{}, err
	}
	ranking, err := quazal.OpenRanking(filepath.Join(c.StateDir, "ranking.json"), rankingSpec)
	if err != nil {
		return game.Definition{}, err
	}
	clans, err := openClans(filepath.Join(c.StateDir, "clans.json"), store)
	if err != nil {
		return game.Definition{}, err
	}
	friends, err := openFriends(filepath.Join(c.StateDir, "friends.json"), store)
	if err != nil {
		return game.Definition{}, err
	}
	stats, err := openStats(filepath.Join(c.StateDir, "stats.json"), func(pid uint32) bool { _, ok := store.LookupPID(pid); return ok })
	if err != nil {
		return game.Definition{}, err
	}
	lobbies := newSessions(log, func(pid uint32) string {
		account, ok := store.LookupPID(pid)
		if !ok {
			return ""
		}
		return account.Name
	})
	clans.online = lobbies.online
	lobbies.stats = stats
	friends.online = lobbies.online
	friends.clan = clans.abbreviation
	var auxiliary []game.UDPService
	var reserved []game.PortRange
	if c.RelayMode != "direct" {
		relay, err := peerrelay.New(peerrelay.Options{Config: c.relayConfig(), Log: log})
		if err != nil {
			return game.Definition{}, err
		}
		lobbies.relay, lobbies.relayMode = relay, c.RelayMode
		auxiliary = append(auxiliary, relay)
		reserved = append(reserved, game.PortRange{First: int(c.RelayFirstPort), Last: int(c.RelayLastPort)})
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/connectivity" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"game": "snowblind", "status": "backend_experimental",
				"payload_mode": c.PayloadMode, "retail_validated": false,
				"sessions": lobbies.count(), "peer_connectivity": c.RelayMode,
				"relay": lobbies.relay.Snapshot(),
			})
			return
		}
		patchHandler(log).ServeHTTP(w, r)
	})
	return game.Definition{
		Key: "snowblind", Game: "snowblind", Bind: c.Bind.String(),
		UDPPorts:  []int{int(c.AuthPort), int(c.SecurePort)},
		HTTPPaths: []string{"/psb1a/"}, HTTP: handler, Mode: "backend_experimental",
		Auxiliary: auxiliary, ReservedUDP: reserved,
		NewServices: func(sockets []*net.UDPConn, observe game.Observe) ([]game.UDPService, error) {
			title := &startup{ranking: ranking, sessions: lobbies, accounts: store, clans: clans, friends: friends, stats: stats}
			options := quazal.Options{
				PublicIP: c.PublicIP, ServerPID: 2, PrincipalKey: key, Store: store, Log: log,
				ValidateAccountData: quazal.ValidateEidosAccountData, GameRPC: title.handle,
				GameRegistered: lobbies.registered, GameDisconnect: lobbies.disconnect,
				GameProbe: lobbies.probe, GameConnectionURLs: lobbies.connectionURLs,
			}
			if lobbies.relay != nil {
				options.GameProbeInstructions = lobbies.relay.DrainInstructions
			}
			transport := prudp.Options{
				ImplicitPayloadSize: true, ChecksumSeed: 0x02, Log: log, Observe: observe,
				PlainPayload: c.PayloadMode == "plain", RawPayload: c.PayloadMode == "raw",
			}
			endpoints, err := quazal.NewEndpoints(sockets, options, [2]prudp.Options{transport, transport})
			if err != nil {
				return nil, err
			}
			return []game.UDPService{endpoints[0], endpoints[1]}, nil
		},
	}, nil
}
