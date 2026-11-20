// Package ttl25 implements the SLUS-210.16 Quazal backend and title services.
package ttl25

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/game"
	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func Definition(log *slog.Logger, publicIP netip.Addr, stateDir string, key quazalcrypto.Key, connectivity ...RelayConfig) (game.Definition, error) {
	store, err := quazal.OpenStore(filepath.Join(stateDir, "accounts.json"))
	if err != nil {
		return game.Definition{}, err
	}
	ranking, err := openRanking(filepath.Join(stateDir, "ranking.json"))
	if err != nil {
		return game.Definition{}, err
	}
	options := quazal.Options{Role: quazal.Auth, PublicIP: publicIP, SecurePort: 30121, ServerPID: 2, PrincipalKey: key, Store: store, Log: log, ValidateAccountData: ValidateAccountData, SingleRegistration: true}
	sessions := newSessions()
	sessions.log = log
	var auxiliary []game.UDPService
	var reserved []game.PortRange
	if len(connectivity) > 1 {
		return game.Definition{}, fmt.Errorf("only one relay configuration is supported")
	}
	if len(connectivity) == 1 {
		if err := connectivity[0].validate(); err != nil {
			return game.Definition{}, err
		}
		if connectivity[0].Mode != "direct" {
			relay, err := newNativeRelay(connectivity[0], log)
			if err != nil {
				return game.Definition{}, err
			}
			sessions.relay = relay
			reserved = append(reserved, game.PortRange{First: int(connectivity[0].FirstPort), Last: int(connectivity[0].LastPort)})
			auxiliary = append(auxiliary, relay)
		}
	}
	community, err := openCommunity(filepath.Join(stateDir, "community.json"), store, sessions)
	if err != nil {
		return game.Definition{}, err
	}
	community.motd, err = loadMessageOfDay(os.Getenv("QUETZAL_TTL_MOTD_FILE"), log)
	if err != nil {
		return game.Definition{}, fmt.Errorf("MOTD: %w", err)
	}
	sessions.name = community.accountName
	// The transport drains instructions on idle ticks even in direct mode;
	// use that lifecycle hook to expire abandoned migration vacancies too.
	options.GameProbeInstructions = func() []quazal.ProbeInstruction {
		if err := community.settleExpiredRatings(); err != nil {
			log.Error("rating settlement failed", "err", err)
		}
		return sessions.migrationInstructions()
	}
	options.GameProbe = sessions.probeTargets
	options.GameConnectionURLs = sessions.connectionURLs
	options.GameStatus = community.status
	options.GameRegistered = func(peer prudp.Peer, pid uint32, url string) {
		if err := community.ensureStatistics(pid); err != nil {
			log.Error("initial statistics persistence failed", "pid", pid, "err", err)
		}
		community.status(peer, pid, "Online")
		sessions.registered(peer, pid, url)
	}
	options.GameDisconnect = func(peer prudp.Peer, pid uint32) { community.disconnect(peer, pid); sessions.disconnect(peer, pid) }
	options.GameRPC = func(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
		if request.Protocol == 26 && request.Method == 1 {
			return community.competition(pid, request.Args)
		}
		if request.Protocol == 26 && request.Method == 2 {
			return sessions.registerCompetitionRound(peer, pid, request.Args)
		}
		if request.Protocol == 50 {
			return sessions.extension(peer, pid, request)
		}
		if request.Protocol == 101 && request.Method == 2 {
			return sessions.searchFor(peer, pid, request)
		}
		if request.Protocol == 21 && request.Method == 1 {
			r := rankingReader{b: request.Args}
			typeName := r.str()
			if typeName == "TTLSession" || typeName == "TTLRound" {
				return sessions.register(peer, pid, request.Args)
			}
		}
		if request.Protocol == 21 && request.Method == 2 {
			return sessions.unregister(peer, pid, request.Args)
		}
		if request.Protocol == 21 && request.Method == 31 {
			return community.gatheringReport(peer, pid, request.Args)
		}
		if request.Protocol == 21 {
			switch request.Method {
			case 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 20, 21, 22, 23, 24, 26, 27, 28, 29, 30:
				return sessions.operation(peer, pid, request)
			}
		}
		if request.Protocol == 15 && request.Method == 1 {
			return community.competition(pid, request.Args)
		}
		if request.Protocol == 101 {
			return community.handle(peer, pid, request)
		}
		return ranking.handle(pid, request)
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
	return game.Definition{Key: "25tolife", Game: "25tolife", HTTPPaths: []string{"/TTL/", "/connectivity"}, ReservedUDP: reserved, UDPPorts: []int{30120, 30121, 10100, 10105, 10110}, HTTP: relayDiagnostics(PatchHandler(log), sessions.relay), Auxiliary: auxiliary, Mode: "accounts_experimental",
		NewUDP: func(conn *net.UDPConn, observe func(string, netip.AddrPort, netip.AddrPort, []byte) error) (game.UDPService, error) {
			var handler prudp.Handler = auth
			var keepAlive time.Duration
			if conn.LocalAddr().(*net.UDPAddr).Port == 30121 {
				handler = secure
				keepAlive = 5 * time.Second
			}
			return prudp.New(prudp.Options{Bind: conn.LocalAddr().(*net.UDPAddr).AddrPort(), Conn: conn, ChecksumSeed: 0xff,
				PlainPayload: true, MaxDatagramSize: 1000, MaxRetries: 20, RetryInterval: 750 * time.Millisecond,
				KeepAliveInterval: keepAlive,
				Observe:           observe, Handler: handler, Log: log})
		}}, nil
}

// PatchHandler exercises the stock no-patch path found in PATCHLIB.REL.
// Retail compatibility still requires a game test.
func PatchHandler(log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		event := "http_unhandled"
		if r.URL.Path == "/TTL/final-version.txt" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			event = "patch_check"
		}
		log.Info(event, "game", "25tolife", "remote", r.RemoteAddr, "host", r.Host,
			"method", r.Method, "path", r.URL.Path, "status", http.StatusNotFound)
		w.Header().Set("Content-Length", "0")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotFound)
	})
}
