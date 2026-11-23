package quazal

import (
	"bytes"
	"errors"
	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
)

const MaxAccountData = 16 * 1024

// AnyData retains the SDK type name and serialized concrete body. The adapter
// owns any inner Data length and fields; storage preserves the exact bytes.
type AnyData struct {
	Type    string `json:"type"`
	Payload []byte `json:"payload"`
}

func cloneData(data *AnyData) *AnyData {
	if data == nil {
		return nil
	}
	return &AnyData{Type: data.Type, Payload: bytes.Clone(data.Payload)}
}

func validData(data *AnyData) bool {
	return data != nil && len(data.Type) > 0 && len(data.Type) <= 128 && !bytes.ContainsRune([]byte(data.Type), 0) && len(data.Payload) <= MaxAccountData
}

func (s *Service) setStatus(peer prudp.Peer, state *peerState, args []byte) ([]byte, error) {
	if !state.proof || !state.registered {
		return nil, errors.New("status requires registered secure proof")
	}
	r := reader{data: args}
	status := r.str(128)
	if !r.done() {
		return nil, ErrRMC
	}
	state.status = status
	if !state.guest && s.options.GameStatus != nil {
		s.options.GameStatus(peer, state.pid, status)
	}
	// The 0x8011 reply decoder reads no application fields.
	return nil, nil
}

func (s *Service) updateAccountData(state *peerState, args []byte) ([]byte, error) {
	if !state.proof || !state.registered || state.guest {
		return nil, errors.New("profile update requires registered account proof")
	}
	if s.options.ValidateAccountData == nil {
		return nil, errors.New("account data schema is unavailable")
	}
	r := reader{data: args}
	public := AnyData{Type: r.str(128), Payload: r.buffer(MaxAccountData)}
	private := AnyData{Type: r.str(128), Payload: r.buffer(MaxAccountData)}
	if !r.done() || !validData(&public) || !validData(&private) {
		return nil, ErrRMC
	}
	if err := s.options.ValidateAccountData(public, private); err != nil {
		return nil, err
	}
	if err := s.options.Store.UpdateData(state.pid, public, private); err != nil {
		return nil, err
	}
	w := writer{}
	w.u32(Success)
	return w.data, w.err
}

func (s *Service) updateContact(state *peerState, args []byte) ([]byte, error) {
	if !state.proof || !state.registered || state.guest {
		return nil, errors.New("contact update requires registered account proof")
	}
	r := reader{data: args}
	contact := r.str(255)
	if !r.done() {
		return nil, ErrRMC
	}
	if err := s.options.Store.UpdateContact(state.pid, contact); err != nil {
		return nil, err
	}
	// AccountManagement reply 0x800c decodes one qResult, like 0x800d.
	w := writer{}
	w.u32(Success)
	return w.data, w.err
}
