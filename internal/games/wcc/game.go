// Package wcc implements the World Championship Cards (US PS2, SLUS-213.40)
// Quazal backend: accounts and login through the shared Rendez-Vous services,
// plus the title's matchmaking and health checks.
package wcc

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/game"
	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

const (
	// AuthPort is written by the backend settings constructor 0x0028ee38
	// next to wcclive.quazal.net.
	AuthPort = 30280
	// SecurePort is advertised by the server in the login reply; the client
	// does not fix it.
	SecurePort = 30281
	// ChecksumSeed is the byte sum of access key "uh85sn" (0x006e3818),
	// installed through 0x003d13e0 and summed by 0x003837a0.
	ChecksumSeed = 0x2b

	// Reliable retransmission budget (about 15 seconds), matching 25 to Life.
	retryLimit    = 20
	retryInterval = 750 * time.Millisecond
)

// Health (18) and Monitoring (19) are bound together by 0x003f2680; the
// title pings 18/1 from 0x0028f3e0. Both PingDaemon replies are one bool.
func healthPing(request quazal.Request) bool {
	return (request.Protocol == 18 || request.Protocol == 19) && request.Method == 1 && len(request.Args) == 0
}

// Definition returns the retail-port tenant.
func Definition(log *slog.Logger, publicIP netip.Addr, stateDir string, key quazalcrypto.Key) (game.Definition, error) {
	return definition(log, publicIP, AuthPort, SecurePort, stateDir, key)
}

func definition(log *slog.Logger, publicIP netip.Addr, authPort, securePort uint16, stateDir string, key quazalcrypto.Key) (game.Definition, error) {
	// The title's name entry allows spaces between characters ("Spaces are
	// not allowed at the beginning or at the end of the player name. However,
	// spaces are allowed inbetween characters." in DATA/SHELL.WAD).
	store, err := quazal.OpenStoreWithNames(filepath.Join(stateDir, "accounts.json"), quazal.NamePolicy{InnerSpaces: true})
	if err != nil {
		return game.Definition{}, err
	}
	name := func(pid uint32) string {
		if account, ok := store.LookupPID(pid); ok {
			return account.Name
		}
		return ""
	}
	gatherings := newRegistry(log, name)
	options := quazal.Options{Role: quazal.Auth, PublicIP: publicIP, SecurePort: securePort, ServerPID: 2,
		PrincipalKey: key, Store: store, Log: log, SingleRegistration: true}
	options.GameRegistered = gatherings.registered
	options.GameDisconnect = gatherings.disconnect
	options.GameProbe = gatherings.probeTargets
	options.GameConnectionURLs = gatherings.connectionURLs
	options.GameProbeInstructions = gatherings.probeInstructions
	options.GameServerCalls = gatherings.serverCalls
	options.AllowGuestGameRPC = healthPing
	options.GameRPC = func(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
		var reply []byte
		var err error
		switch {
		case healthPing(request):
			return putBool(nil, true), nil
		case request.Protocol == 21:
			reply, err = gatherings.matchmaking(peer, pid, request)
		case request.Protocol == 50:
			reply, err = gatherings.extension(peer, pid, request)
		default:
			err = errUnsupported
		}
		if errors.Is(err, errUnsupported) {
			// Field tests rely on this to find operations still to implement.
			log.Warn("wcc unsupported rpc", "protocol", request.Protocol, "method", request.Method, "pid", pid, "bytes", len(request.Args))
		}
		return reply, err
	}
	auth, err := quazal.New(options)
	if err != nil {
		return game.Definition{}, err
	}
	options.Role = quazal.Secure
	secure, err := quazal.New(options)
	if err != nil {
		return game.Definition{}, err
	}
	diagnostics := http.NewServeMux()
	diagnostics.HandleFunc("GET /connectivity", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(gatherings.snapshot())
	})
	return game.Definition{Key: "wcc", Game: "wcc", UDPPorts: []int{int(authPort), int(securePort)}, HTTP: diagnostics, Mode: "accounts_experimental",
		NewUDP: func(conn *net.UDPConn, observe func(string, netip.AddrPort, netip.AddrPort, []byte) error) (game.UDPService, error) {
			var handler prudp.Handler = auth
			if conn.LocalAddr().(*net.UDPAddr).Port == int(securePort) {
				handler = secure
			}
			// Title init never calls the RC4 enabler 0x00355388, so payloads use
			// the plain raw/LZO wrapper (compression flag 0x00656c40 defaults on).
			// Same retail-sized retry grace as 25 to Life: a WCC session dropped
			// a player with "reliable retries exhausted" under the defaults.
			return prudp.New(prudp.Options{Bind: conn.LocalAddr().(*net.UDPAddr).AddrPort(), Conn: conn, ChecksumSeed: ChecksumSeed,
				PlainPayload: true, MaxDatagramSize: 1000, MaxRetries: retryLimit, RetryInterval: retryInterval,
				Observe: observe, Handler: handler, Log: log})
		}}, nil
}
