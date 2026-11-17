package quazal

import (
	"errors"
	"strconv"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
)

// ProbeInstruction is a title-authorized native traversal request to a live,
// ticket-proven peer. It is drained by the transport, including on idle ticks.
type ProbeInstruction struct {
	Peer prudp.Peer
	URL  string
}

// The retail protocol-3 dispatcher at 0x5430e8 accepts a list of
// StationURLs for method 1 and one StationURL for method 2. Both replies are
// empty. Method 1 tells the target's live connection to probe the requester.
func (s *Service) requestProbes(peer prudp.Peer, state *peerState, args []byte) ([]byte, error) {
	if !state.proof || !state.registered || state.guest || state.stationURL == "" || s.options.GameProbe == nil {
		return nil, errors.New("probe request requires registered account proof")
	}
	r := reader{data: args}
	n := r.u32()
	if n > 32 {
		return nil, ErrRMC
	}
	urls := make([]string, 0, n)
	for range n {
		urls = append(urls, r.str(512))
	}
	if !r.done() {
		return nil, ErrRMC
	}
	targets, err := s.options.GameProbe(peer, state.pid, urls)
	if err != nil {
		return nil, err
	}
	if len(targets) > 32 || len(targets) > 128-len(s.outbound) {
		return nil, errors.New("probe queue capacity reached")
	}
	now := s.options.Now()
	seen := map[peerID]bool{}
	for _, target := range targets {
		key := peerKey(target)
		other := s.peers[key]
		if seen[key] || key == peerKey(peer) || other == nil || !other.proof || !other.registered || other.guest {
			return nil, errors.New("probe target is unavailable")
		}
		seen[key] = true
		for id, expiry := range other.probes {
			if !now.Before(expiry) {
				delete(other.probes, id)
			}
		}
		if len(other.probes) >= 32 {
			return nil, errors.New("probe target capacity reached")
		}
	}
	for _, target := range targets {
		other := s.peers[peerKey(target)]
		if other.probes == nil {
			other.probes = map[uint32]time.Time{}
		}
		// Server calls and client requests have separate RMC directions.
		s.nextProbe++
		id := s.nextProbe | 0x80000000
		w := writer{}
		w.str(state.stationURL + ";RVCID=" + strconv.FormatUint(uint64(state.connectionID), 10))
		wire, err := EncodeRequest(Request{Protocol: 3, Method: 2, CallID: id, Args: w.data})
		if err != nil {
			return nil, err
		}
		other.probes[id] = now.Add(10 * time.Second)
		s.outbound = append(s.outbound, prudp.OutboundMessage{Peer: target, Payload: wire})
	}
	s.options.Log.Info("quazal probe initiation queued", "pid", state.pid, "targets", len(targets))
	return nil, nil
}

func (s *Service) DrainOutbound() []prudp.OutboundMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.options.Role == Secure && s.options.GameProbeInstructions != nil {
		for _, instruction := range s.options.GameProbeInstructions() {
			other := s.peers[peerKey(instruction.Peer)]
			if other == nil || !other.proof || !other.registered || other.guest || len(instruction.URL) > 512 || len(s.outbound) >= 128 {
				continue
			}
			now := s.options.Now()
			for id, expiry := range other.probes {
				if !now.Before(expiry) {
					delete(other.probes, id)
				}
			}
			if len(other.probes) >= 32 {
				continue
			}
			if other.probes == nil {
				other.probes = map[uint32]time.Time{}
			}
			s.nextProbe++
			id := s.nextProbe | 0x80000000
			w := writer{}
			w.str(instruction.URL)
			wire, err := EncodeRequest(Request{Protocol: 3, Method: 2, CallID: id, Args: w.data})
			if err != nil {
				continue
			}
			other.probes[id] = now.Add(10 * time.Second)
			s.outbound = append(s.outbound, prudp.OutboundMessage{Peer: instruction.Peer, Payload: wire})
		}
	}
	out := s.outbound
	s.outbound = nil
	return out
}

func (s *Service) probeResponse(peer prudp.Peer, wire []byte) ([][]byte, error) {
	r, err := ParseResponse(wire)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.peers[peerKey(peer)]
	if s.options.Role != Secure || state == nil || !state.proof || !state.registered || state.guest {
		return nil, errors.New("probe response requires registered account proof")
	}
	expiry, pending := state.probes[r.CallID]
	if r.Protocol != 3 || !pending {
		return nil, nil // ignore unsolicited/duplicate replies; never answer replies
	}
	if r.Success && (r.Method != 2 || len(r.Payload) != 0) {
		return nil, ErrRMC
	}
	delete(state.probes, r.CallID)
	s.options.Log.Info("quazal probe initiation acknowledged", "pid", state.pid, "success", r.Success, "expired", !s.options.Now().Before(expiry))
	return nil, nil
}

// SecureConnection 11/3: CID and PID selectors, then bool + L<StationURL>.
// Verified against request writer 0x5512a0 and dispatcher 0x551498.
func (s *Service) connectionURLs(peer prudp.Peer, state *peerState, args []byte) ([]byte, error) {
	if !state.proof || !state.registered || state.guest || s.options.GameConnectionURLs == nil {
		return nil, errors.New("connection lookup requires registered account proof")
	}
	r := reader{data: args}
	cid, targetPID := r.u32(), r.u32()
	if !r.done() {
		return nil, ErrRMC
	}
	urls, err := s.options.GameConnectionURLs(peer, state.pid, cid, targetPID)
	if err != nil {
		return nil, err
	}
	if len(urls) > 32 {
		return nil, ErrRMC
	}
	w := writer{}
	if len(urls) > 0 {
		w.raw([]byte{1})
	} else {
		w.raw([]byte{0})
	}
	w.u32(uint32(len(urls)))
	for _, url := range urls {
		if len(url) > 512 {
			return nil, ErrRMC
		}
		w.str(url)
	}
	return w.data, w.err
}
