package tdu

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"

	"github.com/princebyyourlogic/quetzal/internal/game"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/connectivity"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/nativeproxy"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/presence"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/sessionmaster"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/titledata"
	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

// Definition creates tenant state and public resource handlers without binding
// sockets or starting goroutines. The app owns the tenant lock and supplies the
// four bound sockets in auth, secure, native master, native proxy order.
func Definition(log *slog.Logger, c Config, key quazalcrypto.Key) (game.Definition, error) {
	if err := c.Validate(); err != nil {
		return game.Definition{}, err
	}
	if key == (quazalcrypto.Key{}) {
		return game.Definition{}, errors.New("TDU principal key must not be zero")
	}
	if log == nil {
		log = slog.Default()
	}
	resources, err := nativeResources(c)
	if err != nil {
		return game.Definition{}, err
	}
	var sessions *sessionmaster.Service
	var players *presence.Service
	handlers := make(map[uint16]nativeproxy.DataHandler)
	batchHandlers := make(map[uint16]nativeproxy.BatchDataHandler)
	registrations := make(map[uint16]uint16)
	if c.DirectoryPort != 0 {
		catalog, e := presence.LoadActivityCatalog(c.ActivityCatalogPath)
		if e != nil {
			return game.Definition{}, fmt.Errorf("TDU activity catalog: %w", e)
		}
		sessions, err = sessionmaster.New(sessionmaster.Options{Port: c.SessionPort, ActivityCatalog: catalog})
		if err != nil {
			return game.Definition{}, err
		}
		for port, handler := range sessions.BatchHandlers() {
			batchHandlers[port] = handler
		}
		for _, id := range sessionmaster.SupportedActions() {
			registrations[id] = c.SessionPort
		}
		players, err = presence.New(presence.Options{DensityPort: c.DensityPort, UserMapPort: c.UserMapPort})
		if err != nil {
			return game.Definition{}, err
		}
		for port, handler := range players.Handlers() {
			handlers[port] = handler
		}
		for id, port := range players.Registrations() {
			registrations[id] = port
		}
		native, err := ednet.New(ednet.Options{PublicIP: c.PublicIP, DirectoryPort: c.DirectoryPort, ResourcePort: c.ResourcePort, Resources: resources, Registrations: registrations})
		if err != nil {
			return game.Definition{}, fmt.Errorf("TDU native services: %w", err)
		}
		for port, handler := range native.Handlers() {
			handlers[port] = handler
		}
	}
	traceNativeServices(log, handlers, batchHandlers)
	links, err := connectivity.New(connectivity.Options{Membership: func(caller, target uint32) (sessionmaster.Membership, bool) {
		if sessions == nil {
			return sessionmaster.Membership{}, false
		}
		return sessions.Membership(caller, target)
	}, Discovery: func(caller, target uint32) (sessionmaster.Membership, bool) {
		if sessions == nil || players == nil || caller == 0 {
			return sessionmaster.Membership{}, false
		}
		var owner sessionmaster.Owner
		for _, record := range players.Snapshot() {
			if record.Update.UserID != caller {
				continue
			}
			// Multiple live generations claiming this PID cannot identify a
			// native caller. Quazal proof is checked independently by links.
			if owner.Generation != 0 {
				return sessionmaster.Membership{}, false
			}
			owner = sessionmaster.Owner{Remote: record.Owner.Remote, Generation: record.Owner.Generation}
		}
		if owner.Generation == 0 {
			return sessionmaster.Membership{}, false
		}
		return sessions.BrowseIntroduction(owner, target)
	}})
	if err != nil {
		return game.Definition{}, err
	}
	store, err := quazal.OpenStore(filepath.Join(c.StateDir, "accounts.json"))
	if err != nil {
		return game.Definition{}, fmt.Errorf("TDU accounts: %w", err)
	}
	titleStore, err := titledata.Open(filepath.Join(c.StateDir, "title-data.json"))
	if err != nil {
		return game.Definition{}, fmt.Errorf("TDU title data: %w", err)
	}
	titleService, err := titledata.New(titleStore, store)
	if err != nil {
		return game.Definition{}, err
	}
	diagnostics := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connectivity" {
			http.NotFound(w, r)
			return
		}
		sessionCount, playerCount := 0, 0
		if sessions != nil {
			sessionCount = len(sessions.Snapshot())
		}
		if players != nil {
			playerCount = len(players.Snapshot())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"game": "tdu", "status": "backend_experimental", "sessions": sessionCount, "players": playerCount, "peer_connectivity": "native_direct", "retail_validated": false})
	})
	return game.Definition{Key: "tdu", Game: "tdu", Bind: c.Bind.String(), UDPPorts: []int{int(c.AuthPort), int(c.SecurePort), int(c.MasterPort), int(c.ProxyPort)}, HTTP: diagnostics, Mode: "backend_experimental",
		NewServices: func(sockets []*net.UDPConn, observe game.Observe) ([]game.UDPService, error) {
			if len(sockets) != 4 {
				return nil, errors.New("TDU requires four app-bound UDP sockets")
			}
			seen := map[*net.UDPConn]bool{}
			for index, conn := range sockets {
				if conn == nil || seen[conn] || conn.RemoteAddr() != nil {
					return nil, errors.New("TDU requires four distinct UDP sockets")
				}
				seen[conn] = true
				address, ok := conn.LocalAddr().(*net.UDPAddr)
				if !ok || !address.AddrPort().Addr().Is4() {
					return nil, errors.New("TDU sockets must use IPv4")
				}
				configured := []uint16{c.AuthPort, c.SecurePort, c.MasterPort, c.ProxyPort}[index]
				if configured != 0 && address.Port != int(configured) {
					return nil, errors.New("TDU sockets are not in configured port order")
				}
			}
			options := quazal.Options{Role: quazal.Auth, PublicIP: c.PublicIP, SecurePort: sockets[1].LocalAddr().(*net.UDPAddr).AddrPort().Port(), ServerPID: 2, PrincipalKey: key, Store: store, Log: log, RegisteredURLScheme: "prudps",
				AllowGuestGameRPC: func(request quazal.Request) bool { return request.Protocol == 104 && request.Method == 1 },
				GameRegistered:    links.Registered, GameDisconnect: links.Disconnect,
				GameConnectionURLs: links.ConnectionURLs, GameProbe: links.Probe, GameProbeInstructions: links.ProbeInstructions,
				GameRPC: func(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
					if request.Protocol == 11 && request.Method == 2 {
						return links.HandleRPC(peer, pid, request)
					}
					if request.Protocol == 102 || request.Protocol == 103 {
						return titleService.Handle(pid, request)
					}
					if request.Protocol != 104 || request.Method != 1 {
						return nil, fmt.Errorf("unsupported TDU protocol %d method %d", request.Protocol, request.Method)
					}
					if len(request.Args) != 0 {
						return nil, errors.New("TDU bootstrap accepts no arguments")
					}
					ip := c.PublicIP.As4()
					var out [4]byte
					binary.LittleEndian.PutUint32(out[:], binary.BigEndian.Uint32(ip[:]))
					return out[:], nil
				},
			}
			authHandler, err := quazal.New(options)
			if err != nil {
				return nil, err
			}
			options.Role = quazal.Secure
			secureHandler, err := quazal.New(options)
			if err != nil {
				return nil, err
			}
			auth, err := prudp.New(prudp.Options{Bind: sockets[0].LocalAddr().(*net.UDPAddr).AddrPort(), Conn: sockets[0], ChecksumSeed: 0xbe, Handler: authHandler, Log: log, Observe: observe})
			if err != nil {
				return nil, err
			}
			secure, err := prudp.New(prudp.Options{Bind: sockets[1].LocalAddr().(*net.UDPAddr).AddrPort(), Conn: sockets[1], ChecksumSeed: 0xbe, Handler: secureHandler, Log: log, Observe: observe})
			if err != nil {
				return nil, err
			}
			native, err := nativeproxy.New(nativeproxy.Options{MasterConn: sockets[2], ProxyConn: sockets[3], PublicIP: c.PublicIP, Log: log, Observe: observe, Services: handlers, BatchServices: batchHandlers,
				OnDisconnect: func(info nativeproxy.SessionInfo) {
					if sessions != nil {
						sessions.OnDisconnect(info)
					}
					if players != nil {
						players.OnDisconnect(info)
					}
				}})
			if err != nil {
				return nil, err
			}
			return []game.UDPService{auth, secure, native}, nil
		}}, nil
}
