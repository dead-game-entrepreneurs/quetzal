// Package social implements the recovered read-only TDU friend roster query.
// Mutations require separate recovery and are never acknowledged by this module.
package social

import (
	"encoding/binary"
	"errors"
	"strings"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

var (
	ErrRequest     = errors.New("malformed TDU friend roster request")
	ErrUnsupported = errors.New("unsupported TDU social operation")
	ErrAccount     = errors.New("TDU friend roster requires a named tenant account")
	ErrRoster      = errors.New("invalid TDU friend roster source")
)

const MaxFriends = 128

type Accounts interface {
	LookupPID(uint32) (quazal.Account, bool)
}

// Query preserves both native request selectors. Their full filtering meanings
// are not established; the source must apply its recovered or explicit policy.
type Query struct {
	Filter byte
	Flag   bool
}

// Friend contains the relationship source's row values. The SDK field names
// beyond PID are descriptive; no unproved numeric relationship/status enum is
// assigned here. The account store supplies the canonical display name.
type Friend struct {
	PID          uint32
	Relationship byte
	Details      uint32
	Status       string
}

// Source returns actual relationships selected for this authenticated principal.
// A tenant without friendship mutations can explicitly supply its empty-state
// policy. There is no default successful roster or successful write fallback.
type Source func(callerPID uint32, query Query) ([]Friend, error)

type Service struct {
	accounts Accounts
	source   Source
}

func New(accounts Accounts, source Source) (*Service, error) {
	if accounts == nil || source == nil {
		return nil, ErrRoster
	}
	return &Service{accounts: accounts, source: source}, nil
}

// Handle must be called behind the shared registered, ticket-proven GameRPC gate.
func (s *Service) Handle(pid uint32, q quazal.Request) ([]byte, error) {
	if q.Protocol != 20 || q.Method != 10 {
		return nil, ErrUnsupported
	}
	if len(q.Args) != 2 || q.Args[1] > 1 {
		return nil, ErrRequest
	}
	if pid == 0 || pid == quazal.GuestPID {
		return nil, ErrAccount
	}
	caller, ok := s.accounts.LookupPID(pid)
	if !ok || caller.PID != pid {
		return nil, ErrAccount
	}
	rows, err := s.source(pid, Query{Filter: q.Args[0], Flag: q.Args[1] != 0})
	if err != nil {
		return nil, err
	}
	if len(rows) > MaxFriends {
		return nil, ErrRoster
	}
	out := binary.LittleEndian.AppendUint32(nil, uint32(len(rows)))
	seen := make(map[uint32]bool, len(rows))
	for _, row := range rows {
		account, ok := s.accounts.LookupPID(row.PID)
		if !ok || account.PID != row.PID || row.PID == 0 || row.PID == pid || row.PID == quazal.GuestPID || seen[row.PID] || len(account.Name) == 0 || !validText(account.Name, 15) || !validText(row.Status, 255) {
			return nil, ErrRoster
		}
		seen[row.PID] = true
		out = binary.LittleEndian.AppendUint32(out, row.PID)
		out = appendText(out, account.Name)
		out = append(out, row.Relationship)
		out = binary.LittleEndian.AppendUint32(out, row.Details)
		out = appendText(out, row.Status)
	}
	return out, nil
}

func validText(s string, max int) bool { return len(s) <= max && !strings.ContainsRune(s, 0) }
func appendText(b []byte, s string) []byte {
	b = binary.LittleEndian.AppendUint16(b, uint16(len(s)+1))
	b = append(b, s...)
	return append(b, 0)
}
