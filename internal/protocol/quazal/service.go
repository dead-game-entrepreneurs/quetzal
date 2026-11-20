// Package quazal provides shared Rendez-Vous authentication and secure services.
// Title-specific RPCs and endpoint policies are supplied through Options.
package quazal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

type Role uint8

const (
	Auth Role = iota + 1
	Secure
)

type Options struct {
	Role                Role
	PublicIP            netip.Addr
	SecurePort          uint16
	ServerPID           uint32
	PrincipalKey        quazalcrypto.Key
	Store               *Store
	Log                 *slog.Logger
	Random              io.Reader
	Now                 func() time.Time
	TicketTTL           time.Duration
	ValidateAccountData func(public, private AnyData) error
	// RegisteredURLScheme selects the client's returned StationURL scheme.
	// Empty defaults to prudp; prudps is available for titles that require it.
	RegisteredURLScheme string
	// SingleRegistration retires an older named-account registration only
	// after a fresh ticket-proven connection successfully registers. Titles
	// that allow simultaneous registrations leave this disabled.
	SingleRegistration bool
	// GameRPC requires registration and ticket proof. Guests are denied unless
	// AllowGuestGameRPC explicitly allows that operation.
	GameRPC func(peer prudp.Peer, pid uint32, request Request) ([]byte, error)
	// AllowGuestGameRPC applies only to title RPCs handled by GameRPC, after
	// registration and ticket proof. Nil preserves named-account-only access.
	AllowGuestGameRPC     func(request Request) bool
	GameDisconnect        func(peer prudp.Peer, pid uint32)
	GameStatus            func(peer prudp.Peer, pid uint32, status string)
	GameRegistered        func(peer prudp.Peer, pid uint32, url string)
	GameProbe             func(peer prudp.Peer, pid uint32, urls []string) ([]prudp.Peer, error)
	GameConnectionURLs    func(peer prudp.Peer, pid, connectionID, targetPID uint32) ([]string, error)
	GameProbeInstructions func() []ProbeInstruction
	// GameServerCalls optionally supplies other title-authorized server-to-
	// client calls (for example notification events). They are drained with
	// probe instructions, only to live, ticket-proven, registered named peers.
	// Replies are not correlated and are ignored like other unsolicited replies.
	GameServerCalls func() []ServerCall
}

type peerID struct {
	port uint16
	id   uint64
}

func peerKey(peer prudp.Peer) peerID { return peerID{peer.LocalPort, peer.ID} }

type callID struct {
	protocol byte
	call     uint32
}
type cachedCall struct {
	digest [32]byte
	reply  []byte
}
type peerState struct {
	peer                     prudp.Peer
	pid                      uint32
	guest, proof, registered bool
	connectionID             uint32
	status                   string
	handshake                [32]byte
	connectReply             []byte
	cache                    map[callID]cachedCall
	order                    []callID
	stationURL               string
	probes                   map[uint32]time.Time
}
type issuedTicket struct {
	pid, destination uint32
	guest            bool
	digest           [32]byte
	expires          time.Time
	nonces           map[uint32]peerID
}

// Service implements the authentication or secure-RDV role without binding sockets.
// Both roles must share one Store and the same backend service-principal key.
type Service struct {
	mu        sync.Mutex
	options   Options
	peers     map[peerID]*peerState
	guestKey  quazalcrypto.Key
	outbound  []prudp.OutboundMessage
	nextProbe uint32
}

var _ prudp.Handler = (*Service)(nil)

func New(options Options) (*Service, error) {
	if options.Role != Auth && options.Role != Secure {
		return nil, errors.New("quazal: invalid role")
	}
	options.PublicIP = options.PublicIP.Unmap()
	if !options.PublicIP.Is4() || options.PublicIP.IsUnspecified() {
		return nil, errors.New("quazal: usable public IPv4 address required")
	}
	if options.RegisteredURLScheme == "" {
		options.RegisteredURLScheme = "prudp"
	}
	if options.RegisteredURLScheme != "prudp" && options.RegisteredURLScheme != "prudps" {
		return nil, errors.New("quazal: registered StationURL scheme must be prudp or prudps")
	}
	if options.Role == Auth && options.SecurePort == 0 {
		return nil, errors.New("quazal: auth role requires the advertised secure port")
	}
	if options.Store == nil || options.PrincipalKey == (quazalcrypto.Key{}) {
		return nil, errors.New("quazal: shared account store and nonzero principal key required")
	}
	if err := options.Store.reservePID(options.ServerPID); err != nil {
		return nil, err
	}
	if options.Log == nil {
		options.Log = slog.Default()
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.TicketTTL == 0 {
		options.TicketTTL = 15 * time.Minute
	}
	if options.TicketTTL < 0 || options.TicketTTL > 24*time.Hour {
		return nil, errors.New("quazal: ticket lifetime must be positive and at most 24 hours")
	}
	return &Service{options: options, peers: make(map[peerID]*peerState), guestKey: quazalcrypto.PasswordKey([]byte(guestPassword), GuestPID)}, nil
}

func (s *Service) Connect(_ context.Context, peer prudp.Peer, payload []byte) (prudp.ConnectResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := peerKey(peer)
	digest := sha256.Sum256(payload)
	if previous := s.peers[key]; previous != nil {
		if previous.handshake != digest {
			return prudp.ConnectResult{}, errors.New("quazal: conflicting repeated CONNECT")
		}
		return prudp.ConnectResult{Payload: append([]byte(nil), previous.connectReply...)}, nil
	}
	if len(s.peers) >= 4096 {
		return prudp.ConnectResult{}, errors.New("quazal: peer capacity reached")
	}
	state := &peerState{peer: peer, handshake: digest, cache: make(map[callID]cachedCall)}
	if s.options.Role == Auth {
		if len(payload) != 0 {
			return prudp.ConnectResult{}, errors.New("quazal: auth CONNECT must have an empty security payload")
		}
	} else {
		ticket, challenge, err := quazalcrypto.OpenConnect(s.options.PrincipalKey, payload)
		if err != nil {
			return prudp.ConnectResult{}, err
		}
		request, err := quazalcrypto.ParseConnectRequest(payload)
		if err != nil {
			return prudp.ConnectResult{}, err
		}
		store := s.options.Store
		store.mu.Lock()
		now := s.options.Now()
		s.purgeTicketsLocked(now)
		issued := store.tickets[ticket.SessionKey]
		if issued == nil || issued.pid != ticket.PrincipalID || issued.destination != s.options.ServerPID || issued.digest != sha256.Sum256(request.Ticket) {
			store.mu.Unlock()
			return prudp.ConnectResult{}, errors.New("quazal: ticket was not issued by this runtime")
		}
		if old, seen := issued.nonces[challenge.Nonce]; seen && old != key {
			store.mu.Unlock()
			return prudp.ConnectResult{}, errors.New("quazal: replayed CONNECT challenge")
		}
		if len(issued.nonces) >= 64 {
			store.mu.Unlock()
			return prudp.ConnectResult{}, errors.New("quazal: ticket connection limit reached")
		}
		issued.nonces[challenge.Nonce] = key
		state.pid = ticket.PrincipalID
		state.guest = issued.guest
		state.proof = true
		store.mu.Unlock()
		state.connectReply = quazalcrypto.ConnectReply(challenge.Nonce)
	}
	s.peers[key] = state
	s.options.Log.Info("quazal connection established", "role", s.options.Role, "peer", peer.ID,
		"remote", peer.Addr, "local_port", peer.LocalPort, "pid", state.pid, "guest", state.guest, "proof", state.proof)
	return prudp.ConnectResult{Payload: append([]byte(nil), state.connectReply...)}, nil
}

func (s *Service) Disconnect(peer prudp.Peer) {
	s.mu.Lock()
	s.disconnectLocked(peer, "transport disconnected")
	s.mu.Unlock()
}

func (s *Service) disconnectLocked(peer prudp.Peer, reason string) {
	if state := s.peers[peerKey(peer)]; state != nil {
		s.options.Log.Info("quazal connection closed", "role", s.options.Role, "peer", peer.ID,
			"remote", peer.Addr, "local_port", peer.LocalPort, "pid", state.pid, "cid", state.connectionID,
			"registered", state.registered, "reason", reason)
		if state.proof && !state.guest && s.options.GameDisconnect != nil {
			s.options.GameDisconnect(peer, state.pid)
		}
	}
	delete(s.peers, peerKey(peer))
	out := s.outbound[:0]
	for _, message := range s.outbound {
		if peerKey(message.Peer) != peerKey(peer) {
			out = append(out, message)
		}
	}
	s.outbound = out
}

func (s *Service) Handle(_ context.Context, peer prudp.Peer, wire []byte) ([][]byte, error) {
	if len(wire) >= 5 && wire[4]&0x80 == 0 {
		return s.probeResponse(peer, wire)
	}
	request, err := ParseRequest(wire)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.peers[peerKey(peer)]
	if state == nil {
		return nil, errors.New("quazal: RPC before CONNECT")
	}
	id := callID{request.Protocol, request.CallID}
	digest := sha256.Sum256(wire)
	if old, exists := state.cache[id]; exists {
		if old.digest != digest {
			return [][]byte{ErrorReply(request, GenericFailure)}, nil
		}
		return [][]byte{append([]byte(nil), old.reply...)}, nil
	}
	payload, err := s.dispatch(peer, state, request)
	requestLog := s.options.Log.With(
		"role", s.options.Role,
		"peer", peer.ID,
		"remote", peer.Addr,
		"local_port", peer.LocalPort,
		"pid", state.pid,
		"cid", state.connectionID,
		"protocol", request.Protocol,
		"method", request.Method,
		"call_id", request.CallID,
	)
	var reply []byte
	if err != nil {
		requestLog.Warn("quazal RPC rejected", "reason", err.Error())
		reply = ErrorReply(request, GenericFailure)
	} else {
		reply, err = SuccessReply(request, payload)
		if err != nil {
			return nil, err
		}
		requestLog.Info("quazal RPC completed")
	}
	if len(state.order) >= 128 {
		delete(state.cache, state.order[0])
		state.order = state.order[1:]
	}
	state.cache[id] = cachedCall{digest: digest, reply: append([]byte(nil), reply...)}
	state.order = append(state.order, id)
	return [][]byte{reply}, nil
}

func (s *Service) dispatch(peer prudp.Peer, state *peerState, request Request) ([]byte, error) {
	switch {
	case s.options.Role == Auth && request.Protocol == 10 && request.Method == 1:
		return s.login(state, request.Args)
	case s.options.Role == Auth && request.Protocol == 10 && request.Method == 3:
		return s.issueTicket(state, request.Args)
	case s.options.Role == Secure && request.Protocol == 11 && request.Method == 1:
		return s.register(peer, state, request.Args)
	case s.options.Role == Secure && request.Protocol == 11 && request.Method == 3:
		return s.connectionURLs(peer, state, request.Args)
	case s.options.Role == Secure && request.Protocol == 25 && request.Method == 1:
		return s.signup(state, request.Args)
	case s.options.Role == Secure && request.Protocol == 25 && request.Method == 12:
		return s.updateContact(state, request.Args)
	case s.options.Role == Secure && request.Protocol == 25 && request.Method == 13:
		return s.updateAccountData(state, request.Args)
	case s.options.Role == Secure && request.Protocol == 25 && request.Method == 17:
		return s.setStatus(peer, state, request.Args)
	case s.options.Role == Secure && request.Protocol == 3 && request.Method == 1:
		return s.requestProbes(peer, state, request.Args)
	default:
		if s.options.Role == Secure && s.options.GameRPC != nil {
			if !state.proof || !state.registered {
				return nil, errors.New("game RPC requires registered account proof")
			}
			if state.guest && (s.options.AllowGuestGameRPC == nil || !s.options.AllowGuestGameRPC(request)) {
				return nil, errors.New("game RPC requires registered account proof")
			}
			return s.options.GameRPC(peer, state.pid, request)
		}
		return nil, errors.New("unsupported protocol or method")
	}
}

func (s *Service) secureURL() string {
	return fmt.Sprintf("prudps:/address=%s;port=%d;stream=3;sid=1;PID=%d;CID=1;type=2", s.options.PublicIP, s.options.SecurePort, s.options.ServerPID)
}

func (s *Service) login(state *peerState, args []byte) ([]byte, error) {
	r := reader{data: args}
	name := r.str(15)
	if !r.done() {
		return nil, ErrRMC
	}
	canonical, err := s.options.Store.canonicalName(name)
	if err != nil {
		return nil, ErrAccountName
	}
	pid := GuestPID
	guest := canonical == guestName
	key := s.guestKey
	if !guest {
		account, ok := s.options.Store.Lookup(name)
		if !ok {
			return nil, errors.New("account unavailable")
		}
		pid = account.PID
		key = quazalcrypto.ExtendInitial(account.InitialKey, pid)
	}
	validation := make([]byte, 16)
	if _, err := io.ReadFull(s.options.Random, validation); err != nil {
		return nil, fmt.Errorf("validation randomness unavailable: %w", err)
	}
	w := writer{}
	w.u32(Success)
	w.u32(pid)
	w.buffer(quazalcrypto.Seal(key, validation))
	w.str(s.secureURL())
	w.u32(0)
	w.str("")
	w.str("")
	if w.err != nil {
		return nil, w.err
	}
	state.pid = pid
	state.guest = guest
	return w.data, nil
}

func (s *Service) purgeTicketsLocked(now time.Time) {
	for key, issued := range s.options.Store.tickets {
		if !now.Before(issued.expires) {
			delete(s.options.Store.tickets, key)
		}
	}
}

func (s *Service) issueTicket(state *peerState, args []byte) ([]byte, error) {
	r := reader{data: args}
	source, destination := r.u32(), r.u32()
	if !r.done() || state.pid == 0 || source != state.pid || destination != s.options.ServerPID {
		return nil, errors.New("ticket request is not bound to the selected account and service")
	}
	clientKey := s.guestKey
	if !state.guest {
		store := s.options.Store
		store.mu.Lock()
		found := false
		for _, account := range store.accounts {
			if account.PID == state.pid {
				clientKey = quazalcrypto.ExtendInitial(account.InitialKey, account.PID)
				found = true
				break
			}
		}
		store.mu.Unlock()
		if !found {
			return nil, errors.New("account unavailable")
		}
	}
	store := s.options.Store
	store.mu.Lock()
	defer store.mu.Unlock()
	s.purgeTicketsLocked(s.options.Now())
	if len(store.tickets) >= 65536 {
		return nil, errors.New("ticket capacity reached")
	}
	var sessionKey quazalcrypto.Key
	unique := false
	for tries := 0; tries < 4; tries++ {
		if _, err := io.ReadFull(s.options.Random, sessionKey[:]); err != nil {
			return nil, errors.New("session randomness unavailable")
		}
		if sessionKey != (quazalcrypto.Key{}) && store.tickets[sessionKey] == nil {
			unique = true
			break
		}
	}
	if !unique {
		return nil, errors.New("could not allocate a unique session key")
	}
	sealed, err := quazalcrypto.IssueServiceTicket(clientKey, s.options.PrincipalKey, source, destination, sessionKey)
	if err != nil {
		return nil, err
	}
	envelope, err := quazalcrypto.OpenServiceTicket(clientKey, sealed)
	if err != nil {
		return nil, err
	}
	store.tickets[sessionKey] = &issuedTicket{pid: source, destination: destination, guest: state.guest, digest: sha256.Sum256(envelope.Ticket), expires: s.options.Now().Add(s.options.TicketTTL), nonces: make(map[uint32]peerID)}
	w := writer{}
	w.u32(Success)
	w.buffer(sealed)
	return w.data, w.err
}

func (s *Service) register(peer prudp.Peer, state *peerState, args []byte) ([]byte, error) {
	if !state.proof {
		return nil, errors.New("registration requires secure ticket proof")
	}
	r := reader{data: args}
	count := r.u32()
	if count > 32 {
		return nil, ErrRMC
	}
	for n := uint32(0); n < count; n++ {
		url := r.str(512)
		if !strings.HasPrefix(url, "prudp:/") && !strings.HasPrefix(url, "prudps:/") {
			return nil, errors.New("invalid StationURL")
		}
	}
	if !r.done() {
		return nil, ErrRMC
	}
	if state.connectionID == 0 {
		// Connection IDs need only be unique among this service's live peers.
		used := make(map[uint32]bool, len(s.peers))
		for _, other := range s.peers {
			used[other.connectionID] = true
		}
		for id := uint32(1); id != 0; id++ {
			if !used[id] {
				state.connectionID = id
				break
			}
		}
		if state.connectionID == 0 {
			return nil, errors.New("connection ID space exhausted")
		}
	}
	if s.options.SingleRegistration && !state.guest {
		var previous []prudp.Peer
		for key, other := range s.peers {
			if key != peerKey(peer) && other.proof && other.registered && !other.guest && other.pid == state.pid {
				previous = append(previous, other.peer)
			}
		}
		if len(previous) > 128-len(s.outbound) {
			return nil, errors.New("registration retirement queue capacity reached")
		}
		for _, retired := range previous {
			s.disconnectLocked(retired, "replaced by fresh account registration")
			s.outbound = append(s.outbound, prudp.OutboundMessage{Peer: retired, Retire: true})
		}
	}
	state.registered = true
	// This is the client's observed public StationURL. The scheme is title
	// policy: prudps selects the ticket-manager connection factory on clients.
	url := fmt.Sprintf("%s:/address=%s;port=%d;stream=3;sid=%d;PID=%d;CID=%d;type=2", s.options.RegisteredURLScheme, peer.Addr.Addr(), peer.Addr.Port(), peer.SourceStream&15, state.pid, state.connectionID)
	state.stationURL = url
	if !state.guest && s.options.GameRegistered != nil {
		s.options.GameRegistered(peer, state.pid, url)
	}
	w := writer{}
	w.u32(Success)
	w.u32(state.connectionID)
	w.str(url)
	return w.data, w.err
}

func (s *Service) signup(state *peerState, args []byte) ([]byte, error) {
	if !state.proof || !state.guest || !state.registered {
		return nil, errors.New("signup requires a registered guest connection")
	}
	r := reader{data: args}
	name := r.str(15)
	text := r.str(32)
	parameter := r.u32()
	contact := r.str(255)
	if !r.done() || parameter != 0x20 || len(text) != 32 {
		return nil, errors.New("unsupported signup shape")
	}
	raw, err := hex.DecodeString(text)
	if err != nil || len(raw) != 16 {
		return nil, errors.New("invalid initial hash")
	}
	var initial quazalcrypto.Key
	copy(initial[:], raw)
	if _, err := s.options.Store.CreateWithContact(name, initial, contact); err != nil {
		return nil, err
	}
	w := writer{}
	w.u32(Success)
	return w.data, w.err
}
