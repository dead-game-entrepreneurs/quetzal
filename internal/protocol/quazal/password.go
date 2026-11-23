package quazal

import (
	"encoding/hex"
	"errors"

	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

// ChangePassword persists an initial hash and revokes all previously issued
// tickets and live proofs for this PID. No raw password reaches storage.
func (s *Store) ChangePassword(pid uint32, initial quazalcrypto.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := make(map[string]Account, len(s.accounts))
	found := false
	for name, account := range s.accounts {
		if account.PID == pid {
			account.InitialKey = initial
			found = true
		}
		updated[name] = account
	}
	if !found {
		return errors.New("quazal: account unavailable")
	}
	if err := s.persist(updated, s.nextPID); err != nil {
		return err
	}
	s.accounts = updated
	s.credentialVersions[pid]++
	for key, issued := range s.tickets {
		if issued.pid == pid {
			delete(s.tickets, key)
		}
	}
	return nil
}

func (s *Service) currentCredentials(state *peerState) bool {
	s.options.Store.mu.Lock()
	defer s.options.Store.mu.Unlock()
	return state.credentialVersion == s.options.Store.credentialVersions[state.pid]
}

func (s *Service) changePassword(state *peerState, args []byte) ([]byte, error) {
	if !state.proof || !state.registered || state.guest {
		return nil, errors.New("password change requires a registered named account")
	}
	r := reader{data: args}
	text := r.str(32)
	if !r.done() || len(text) != 32 {
		return nil, ErrRMC
	}
	raw, err := hex.DecodeString(text)
	if err != nil {
		return nil, ErrRMC
	}
	var initial quazalcrypto.Key
	copy(initial[:], raw)
	if err := s.options.Store.ChangePassword(state.pid, initial); err != nil {
		return nil, err
	}
	for _, other := range s.peers {
		if other.pid == state.pid && other.proof && other.registered && !other.guest {
			if s.options.GameDisconnect != nil {
				s.options.GameDisconnect(other.peer, other.pid)
			}
			other.registered = false
		}
	}
	return []byte{1}, nil
}
