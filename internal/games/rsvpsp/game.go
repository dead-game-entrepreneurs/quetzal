// Package rsvpsp implements the Tom Clancy's Rainbow Six Vegas (US PSP,
// ULUS-10206) Quazal backend: guest login, signup with the Spark profile
// records and named login through the shared Rendez-Vous services, hosted
// matches through the Spark title protocol, and empty answers to the other
// Spark lists (stats, friends, invitations). Other title calls are logged and
// refused.
package rsvpsp

import (
	"encoding/hex"
	"log/slog"
	"net"
	"net/netip"
	"path/filepath"
	"slices"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/game"
	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

const (
	// AuthPort is stored by the backend settings constructor 0x0892f4bc next
	// to rsvegaspsp.quazal.net (the EU host uses 30390).
	AuthPort = 30380
	// SecurePort is advertised by the server in the login reply; the client
	// does not fix it.
	SecurePort = 30381
	// ChecksumSeed is the byte sum of the US access key "R6V3g4s": stream
	// setup 0x08bc7e44 passes the key copied to 0x08f6ad44 to 0x08c1f820,
	// which sums its bytes. (The EU key "oc9NeH4p" would give 0xaa.)
	ChecksumSeed = 0x1f

	// Reliable retransmission budget (about 15 seconds), as for WCC.
	retryLimit    = 20
	retryInterval = 750 * time.Millisecond
	// Request bytes logged for an unsupported title call.
	logArgs = 256
)

// Definition returns the retail-port tenant.
func Definition(log *slog.Logger, publicIP netip.Addr, stateDir string, key quazalcrypto.Key) (game.Definition, error) {
	return definition(log, publicIP, AuthPort, SecurePort, stateDir, key)
}

func definition(log *slog.Logger, publicIP netip.Addr, authPort, securePort uint16, stateDir string, key quazalcrypto.Key) (game.Definition, error) {
	store, err := quazal.OpenStore(filepath.Join(stateDir, "accounts.json"))
	if err != nil {
		return game.Definition{}, err
	}
	options := quazal.Options{Role: quazal.Auth, PublicIP: publicIP, SecurePort: securePort, ServerPID: 2,
		PrincipalKey: key, Store: store, Log: log, SingleRegistration: true,
		ValidateAccountData: ValidateAccountData}
	hosted := newGames(log, func(pid uint32) string {
		if account, ok := store.LookupPID(pid); ok {
			return account.Name
		}
		return ""
	})
	friends, err := openSocial(filepath.Join(stateDir, "friends.json"), hosted, func(name string) (uint32, bool) {
		account, ok := store.Lookup(name)
		return account.PID, ok
	})
	if err != nil {
		return game.Definition{}, err
	}
	stats, err := openCareers(filepath.Join(stateDir, "stats.json"), hosted, friends.friendsOf)
	if err != nil {
		return game.Definition{}, err
	}
	friends.rating = stats.rating
	hosted.reported = func(results []matchResult, seconds uint32) {
		if err := stats.commit(results, seconds); err != nil {
			log.Error("rsvpsp stats not saved", "err", err)
			return
		}
		for _, r := range results {
			log.Info("rsvpsp stats counted", "pid", r.pid, "values", r.values, "seconds", seconds)
		}
	}
	options.GameDisconnect = hosted.disconnect
	options.GameRegistered = hosted.registered
	options.GameServerCalls = hosted.serverCalls
	options.GameRPC = func(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
		switch {
		case request.Protocol == SparkProtocol && request.Method == 10:
			return friends.list(pid, request.Args)
		case request.Protocol == SparkProtocol && request.Method == 11:
			return friends.info(pid, request.Args)
		case request.Protocol == SparkProtocol && slices.Contains([]uint32{6, 7, 8, 17}, request.Method):
			return stats.spark(pid, request)
		case request.Protocol == FriendsProtocol:
			if reply, ok, err := friends.friends(pid, request); ok {
				return reply, err
			}
		case request.Protocol == SparkProtocol:
			if reply, ok, err := hosted.spark(peer, pid, request); ok {
				return reply, err
			}
		case request.Protocol == 21 && request.Method == 18:
			return hosted.findByDescription(request.Args)
		case request.Protocol == 21 && request.Method == 39:
			return hosted.updateURLs(pid, request.Args)
		case request.Protocol == 3 && request.Method == 3:
			return hosted.probeExt(pid, request.Args)
		}
		// Anything else is refused. Arguments are logged so field tests show
		// which calls the menus make next.
		args := request.Args
		if len(args) > logArgs {
			args = args[:logArgs]
		}
		log.Warn("rsvpsp unsupported rpc", "protocol", request.Protocol, "method", request.Method, "pid", pid,
			"bytes", len(request.Args), "args", hex.EncodeToString(args))
		return nil, errUnsupported
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
	return game.Definition{Key: "rsvpsp", Game: "rsvpsp", UDPPorts: []int{int(authPort), int(securePort)}, Mode: "accounts_experimental",
		NewUDP: func(conn *net.UDPConn, observe func(string, netip.AddrPort, netip.AddrPort, []byte) error) (game.UDPService, error) {
			var handler prudp.Handler = auth
			var user prudp.UserDatagramHandler
			if conn.LocalAddr().(*net.UDPAddr).Port == int(securePort) {
				handler = secure
				// The public-URL probe before hosting arrives on the secure port.
				user = publicURL
			}
			// Core constructor 0x08c19918 installs only the LZO compressor
			// (flag 0x08e0118c is 1 in .data and never written) and no
			// transport RC4, so payloads use the plain raw/LZO wrapper.
			return prudp.New(prudp.Options{Bind: conn.LocalAddr().(*net.UDPAddr).AddrPort(), Conn: conn, ChecksumSeed: ChecksumSeed,
				PlainPayload: true, MaxDatagramSize: 1000, MaxRetries: retryLimit, RetryInterval: retryInterval,
				UserHandler: user, UserChecksumSeed: 0,
				Observe: observe, Handler: handler, Log: log})
		}}, nil
}
