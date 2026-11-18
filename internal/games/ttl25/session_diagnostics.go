package ttl25

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"strconv"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

var silentSessionLog = slog.New(slog.NewTextHandler(io.Discard, nil))

func diagnosticHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// Numeric game settings are useful diagnostics. Never emit free-text queries,
// room names, level strings, account names, profile fields or StationURLs.
func diagnosticFilters(fields map[string]string) map[string]string {
	result := make(map[string]string, len(fields))
	for _, names := range [][]string{sessionNumbers, sessionBools} {
		for _, name := range names {
			if value, ok := fields[name]; ok {
				if _, err := strconv.ParseUint(value, 10, 32); err == nil {
					result[name] = value
				}
			}
		}
	}
	if value, ok := fields["levelName"]; ok {
		result["levelName_hash"] = diagnosticHash(value)
	}
	return result
}

func (s *sessionRegistry) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return silentSessionLog
}

func (s *sessionRegistry) searchLogger(peer prudp.Peer, pid uint32, request quazal.Request, query string, offset, limit uint32) *slog.Logger {
	return s.logger().With("pid", pid, "peer", peer.ID, "local_port", peer.LocalPort,
		"protocol", request.Protocol, "method", request.Method, "call_id", request.CallID,
		"query_hash", diagnosticHash(query), "query_bytes", len(query), "offset", offset, "limit", limit)
}

// Caller holds s.mu; snapshots contain only identifiers, settings and live flags.
func (s *sessionRegistry) roomDiagnostic(id uint32, room sessionRecord) map[string]any {
	_, registered := s.peers[room.owner]
	return map[string]any{"id": id, "type": room.data.Type, "owner_pid": room.owner.pid,
		"owner_peer": room.owner.peer, "host_registered": registered, "url_published": room.url != "",
		"state": room.state, "backend_members": len(uniqueMembers(room)), "fields": diagnosticFilters(room.fields)}
}

func (s *sessionRegistry) roomEvent(message string, caller sessionOwner, id uint32, room sessionRecord, attrs ...any) {
	base := []any{"pid", caller.pid, "peer", caller.peer, "local_port", caller.port, "room", s.roomDiagnostic(id, room)}
	s.logger().Info(message, append(base, attrs...)...)
}

func (s *sessionRegistry) joinRejected(caller sessionOwner, id uint32, reason string) {
	s.logger().Warn("matchmaking participation rejected", "pid", caller.pid, "peer", caller.peer,
		"local_port", caller.port, "gid", id, "reason", reason)
}
