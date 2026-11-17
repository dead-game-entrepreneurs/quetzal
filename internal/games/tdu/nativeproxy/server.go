// Package nativeproxy serves the verified US PS2 native master and proxy
// transport. A completed shared-key handshake is not an authenticated account.
package nativeproxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/proxycrypto"
)

var (
	ErrUnsupported = errors.New("nativeproxy: unsupported application request")
	ErrServing     = errors.New("nativeproxy: Serve already called")
)

// SessionInfo identifies a transport only. It contains no account principal.
// Public service handlers must authenticate any account-scoped operation through
// a separately verified protocol; source IP and the embedded hello MAC do not.
type SessionInfo struct {
	Remote netip.AddrPort
	ID     uint16
	// Generation never recycles during this server lifetime, unlike the wire ID.
	Generation         uint64
	Region             uint32
	TransportActivated bool
}

// DataHandler handles one in-process, explicitly registered public service.
// It receives the server-owned destination from the native envelope. A nil
// response means no reply. Responses must fit one native proxy datagram.
type DataHandler func(context.Context, SessionInfo, netip.AddrPort, []byte) ([]byte, error)

// BatchDataHandler can emit the distinct action reply and reliable completion
// required by some native SDK calls. Entries are separate encrypted datagrams.
type BatchDataHandler func(context.Context, SessionInfo, netip.AddrPort, []byte) ([][]byte, error)

const MaxReplyBatch = 4

type Options struct {
	// MasterConn and ProxyConn optionally supply app-bound UDP4 sockets. Each
	// corresponding Bind is ignored. New closes only sockets it created if
	// construction fails; after success Server.Close closes both sockets.
	MasterConn *net.UDPConn
	ProxyConn  *net.UDPConn
	// Observe records complete UDP datagrams on both endpoints, before receive
	// validation and before transmit. Callbacks are serialized and must not
	// retain wire without copying it. Any error stops Serve and prevents that
	// packet from being processed or transmitted.
	Observe          func(direction string, local, remote netip.AddrPort, wire []byte) error
	MasterBind       netip.AddrPort
	ProxyBind        netip.AddrPort
	PublicIP         netip.Addr
	Log              *slog.Logger
	MaxSessions      int
	HandshakeTTL     time.Duration
	SessionTTL       time.Duration
	CleanupInterval  time.Duration
	MinHelloInterval time.Duration
	// DirectoryIP is the native service-directory IPv4 returned by the master.
	// An invalid address defaults to PublicIP. It is separate from the proxy
	// address even when both are served by this process.
	DirectoryIP netip.Addr
	Random      io.Reader
	// Services maps logical destination ports to local code. No packet is
	// forwarded to a network destination, including unregistered local ports.
	Services      map[uint16]DataHandler
	BatchServices map[uint16]BatchDataHandler
	// OnDisconnect runs once per removed transport, outside the server mutex.
	// Shutdown callbacks run after both read loops and application handlers join.
	OnDisconnect func(SessionInfo)
}

type connection struct {
	id          uint16
	generation  uint64
	hello       []byte
	handshake   *proxycrypto.ServerHandshake
	sender      *proxycrypto.Sender
	receiver    *proxycrypto.Receiver
	region      uint32
	activated   bool
	created     time.Time
	lastSeen    time.Time
	unsupported map[uint16]bool
}

type Stats struct {
	Sessions            int
	Pending             int
	KeyExchanged        int
	Activated           int
	RejectedPackets     uint64
	UnsupportedRequests uint64
	ApplicationRequests uint64
}

type Server struct {
	opts           Options
	master         *net.UDPConn
	proxy          *net.UDPConn
	mu             sync.Mutex
	connections    map[netip.AddrPort]*connection
	nextSession    uint16
	nextGeneration uint64
	disconnected   []SessionInfo
	serving        atomic.Bool
	closed         atomic.Bool
	closeOnce      sync.Once
	closeErr       error
	observeMu      sync.Mutex
	observeErr     error
	rejected       atomic.Uint64
	unsupported    atomic.Uint64
	applications   atomic.Uint64
}

// New validates options and uses supplied sockets or binds missing IPv4 sockets.
// It starts no goroutines. Supplied sockets must be distinct, unconnected UDP4
// listeners; their lifetime transfers to Server only after New succeeds.
func New(opts Options) (*Server, error) {
	if !opts.PublicIP.Is4() || opts.PublicIP.IsUnspecified() || opts.PublicIP.IsMulticast() {
		return nil, errors.New("nativeproxy: IPv4 binds and a usable public IPv4 are required")
	}
	if err := validateEndpoint(opts.MasterConn, opts.MasterBind); err != nil {
		return nil, fmt.Errorf("native master: %w", err)
	}
	if err := validateEndpoint(opts.ProxyConn, opts.ProxyBind); err != nil {
		return nil, fmt.Errorf("native proxy: %w", err)
	}
	if opts.MasterConn != nil && opts.MasterConn == opts.ProxyConn {
		return nil, errors.New("nativeproxy: master and proxy require distinct sockets")
	}
	if opts.MaxSessions == 0 {
		opts.MaxSessions = 128
	}
	if opts.MaxSessions < 1 || opts.MaxSessions > 4096 {
		return nil, errors.New("nativeproxy: MaxSessions must be 1..4096")
	}
	if opts.HandshakeTTL == 0 {
		opts.HandshakeTTL = 15 * time.Second
	}
	if opts.SessionTTL == 0 {
		opts.SessionTTL = 2 * time.Minute
	}
	if opts.CleanupInterval == 0 {
		opts.CleanupInterval = 250 * time.Millisecond
	}
	if opts.MinHelloInterval == 0 {
		opts.MinHelloInterval = time.Second
	}
	if opts.HandshakeTTL < time.Millisecond || opts.SessionTTL < time.Millisecond || opts.CleanupInterval < time.Millisecond || opts.MinHelloInterval < 0 {
		return nil, errors.New("nativeproxy: invalid expiry or hello timing")
	}
	if opts.CleanupInterval > time.Second {
		opts.CleanupInterval = time.Second
	}
	if !opts.DirectoryIP.IsValid() {
		opts.DirectoryIP = opts.PublicIP
	}
	if !opts.DirectoryIP.Is4() || opts.DirectoryIP.IsUnspecified() || opts.DirectoryIP.IsMulticast() || opts.DirectoryIP == netip.MustParseAddr("255.255.255.255") {
		return nil, errors.New("nativeproxy: a usable directory IPv4 address is required")
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	services := make(map[uint16]DataHandler, len(opts.Services))
	for port, handler := range opts.Services {
		if port == 0 || handler == nil {
			return nil, errors.New("nativeproxy: services require nonzero ports and handlers")
		}
		services[port] = handler
	}
	batch := make(map[uint16]BatchDataHandler, len(opts.BatchServices))
	for port, handler := range opts.BatchServices {
		if port == 0 || handler == nil || services[port] != nil {
			return nil, errors.New("nativeproxy: batch services require exclusive nonzero ports and handlers")
		}
		batch[port] = handler
	}
	opts.BatchServices = batch
	opts.Services = services
	master := opts.MasterConn
	if master == nil {
		var err error
		master, err = net.ListenUDP("udp4", net.UDPAddrFromAddrPort(opts.MasterBind))
		if err != nil {
			return nil, fmt.Errorf("native master bind: %w", err)
		}
	}
	proxy := opts.ProxyConn
	if proxy == nil {
		var err error
		proxy, err = net.ListenUDP("udp4", net.UDPAddrFromAddrPort(opts.ProxyBind))
		if err != nil {
			if opts.MasterConn == nil {
				master.Close()
			}
			return nil, fmt.Errorf("native proxy bind: %w", err)
		}
	}
	return &Server{opts: opts, master: master, proxy: proxy, connections: make(map[netip.AddrPort]*connection), nextSession: 1}, nil
}

func validateEndpoint(conn *net.UDPConn, bind netip.AddrPort) error {
	if conn == nil {
		if !bind.IsValid() || !bind.Addr().Is4() {
			return errors.New("an IPv4 bind or prebound UDP4 socket is required")
		}
		return nil
	}
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || !local.AddrPort().Addr().Is4() || conn.RemoteAddr() != nil {
		return errors.New("prebound socket must be an unconnected UDP4 listener")
	}
	return nil
}

func (s *Server) MasterAddr() netip.AddrPort { return s.master.LocalAddr().(*net.UDPAddr).AddrPort() }
func (s *Server) ProxyAddr() netip.AddrPort  { return s.proxy.LocalAddr().(*net.UDPAddr).AddrPort() }

// Serve runs both listeners until cancellation, Close or a socket read failure.
// Cancellation returns ctx.Err; an explicit Close returns nil. Serve runs once.
func (s *Server) Serve(ctx context.Context) error {
	if !s.serving.CompareAndSwap(false, true) {
		return ErrServing
	}
	// Cancellation may win before the application's Serve goroutine starts.
	// Release constructor-owned sockets and preserve the cancellation result.
	if err := ctx.Err(); err != nil {
		_ = s.Close()
		return err
	}
	if s.closed.Load() {
		if err := ctx.Err(); err != nil {
			return err
		}
		return net.ErrClosed
	}
	defer s.dropAll()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- s.readLoop(runCtx, s.master, true) }()
	go func() { results <- s.readLoop(runCtx, s.proxy, false) }()
	s.opts.Log.Info("native transport listening", "master", s.MasterAddr(), "proxy", s.ProxyAddr(), "public_ip", s.opts.PublicIP)
	var first error
	select {
	case <-ctx.Done():
		first = ctx.Err()
	case first = <-results:
		cancel()
		s.Close()
		<-results
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(first, net.ErrClosed) {
			return nil
		}
		return first
	}
	cancel()
	s.Close()
	<-results
	<-results
	return first
}

func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		a, b := s.master.Close(), s.proxy.Close()
		if errors.Is(a, net.ErrClosed) {
			a = nil
		}
		if errors.Is(b, net.ErrClosed) {
			b = nil
		}
		s.closeErr = errors.Join(a, b)
	})
	return s.closeErr
}

func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Stats{Sessions: len(s.connections), RejectedPackets: s.rejected.Load(), UnsupportedRequests: s.unsupported.Load(), ApplicationRequests: s.applications.Load()}
	for _, c := range s.connections {
		if c.sender == nil {
			out.Pending++
		} else {
			out.KeyExchanged++
		}
		if c.activated {
			out.Activated++
		}
	}
	return out
}

func (s *Server) expire(now time.Time) {
	for endpoint, c := range s.connections {
		ttl := s.opts.HandshakeTTL
		if c.sender != nil {
			ttl = s.opts.SessionTTL
		}
		if now.Sub(c.lastSeen) >= ttl {
			s.dropLocked(endpoint, c)
			s.opts.Log.Debug("native transport expired", "remote", endpoint, "session", c.id, "activated", c.activated)
		}
	}
}

func (s *Server) readLoop(ctx context.Context, conn *net.UDPConn, master bool) error {
	buffer := make([]byte, 65535) // Preserve complete oversized datagrams for research capture.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := conn.SetReadDeadline(time.Now().Add(s.opts.CleanupInterval)); err != nil {
			return err
		}
		n, remote, err := conn.ReadFromUDPAddrPort(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				if !master {
					s.mu.Lock()
					s.expire(time.Now())
					s.unlockAndNotify()
				}
				continue
			}
			return err
		}
		remote = netip.AddrPortFrom(remote.Addr().Unmap(), remote.Port())
		if err := s.observe("rx", conn, remote, buffer[:n]); err != nil {
			return err
		}
		if n > proxycrypto.MaxDatagram {
			s.rejected.Add(1)
			continue
		}
		packet := buffer[:n]
		if master {
			s.handleMaster(packet, remote)
		} else {
			s.handleProxy(ctx, packet, remote)
		}
		if err := s.captureError(); err != nil {
			return err
		}
	}
}

// Both read loops share one recorder callback. The first error is sticky, so
// another endpoint cannot continue recording or transmitting after capture fails.
func (s *Server) observe(direction string, conn *net.UDPConn, remote netip.AddrPort, wire []byte) error {
	s.observeMu.Lock()
	defer s.observeMu.Unlock()
	if s.observeErr != nil {
		return s.observeErr
	}
	if s.opts.Observe != nil {
		local := conn.LocalAddr().(*net.UDPAddr).AddrPort()
		if err := s.opts.Observe(direction, local, remote, wire); err != nil {
			s.observeErr = fmt.Errorf("native %s capture: %w", direction, err)
		}
	}
	return s.observeErr
}

func (s *Server) captureError() error {
	s.observeMu.Lock()
	defer s.observeMu.Unlock()
	return s.observeErr
}

func (s *Server) handleMaster(packet []byte, remote netip.AddrPort) {
	request, err := proxycrypto.ParseMasterRequest(packet)
	if err != nil {
		s.rejected.Add(1)
		return
	}
	reply := proxycrypto.MasterReply{Proxy: s.opts.PublicIP, DirectoryIP: s.opts.DirectoryIP}
	if request.Region != proxycrypto.RegionUS || request.Revision != proxycrypto.RetailRevision {
		reply = proxycrypto.MasterReply{Proxy: netip.IPv4Unspecified(), DirectoryIP: netip.IPv4Unspecified()}
		s.opts.Log.Debug("native master unsupported build", "remote", remote, "region", request.Region, "revision", request.Revision)
	}
	wire, _ := reply.MarshalBinary()
	if err := s.observe("tx", s.master, remote, wire); err != nil {
		return
	}
	if _, err = s.master.WriteToUDPAddrPort(wire, remote); err != nil && !s.closed.Load() {
		s.opts.Log.Debug("native master reply failed", "remote", remote, "err", err)
	}
}

func (s *Server) allocateID() uint16 {
	for {
		id := s.nextSession
		s.nextSession++
		if id == 0xffff {
			continue
		}
		used := false
		for _, c := range s.connections {
			if c.id == id {
				used = true
				break
			}
		}
		if !used {
			return id
		}
	}
}

func (s *Server) reject(remote netip.AddrPort, err error) {
	s.rejected.Add(1)
	s.opts.Log.Debug("native transport packet rejected", "remote", remote, "err", err)
}

func (s *Server) unknown(remote netip.AddrPort, c *connection, command uint16, reason string) {
	s.unsupported.Add(1)
	if !c.unsupported[command] && len(c.unsupported) < 16 {
		c.unsupported[command] = true
		s.opts.Log.Warn("native transport unsupported request", "remote", remote, "session", c.id, "command_or_port", command, "activated", c.activated, "reason", reason)
	}
}

func (s *Server) write(remote netip.AddrPort, packet []byte) {
	if err := s.observe("tx", s.proxy, remote, packet); err != nil {
		return
	}
	if _, err := s.proxy.WriteToUDPAddrPort(packet, remote); err != nil && !s.closed.Load() {
		s.opts.Log.Debug("native proxy reply failed", "remote", remote, "err", err)
	}
}

func (s *Server) handleProxy(ctx context.Context, packet []byte, remote netip.AddrPort) {
	s.mu.Lock()
	defer s.unlockAndNotify()
	now := time.Now()
	s.expire(now)
	if len(packet) < proxycrypto.HeaderSize {
		s.reject(remote, proxycrypto.ErrPacket)
		return
	}
	flags := binary.BigEndian.Uint16(packet[:2])
	command := binary.BigEndian.Uint16(packet[4:6])
	c := s.connections[remote]
	if flags == 4 && command == 1 {
		hello, err := proxycrypto.ParseClientHello(packet)
		if err != nil {
			s.reject(remote, err)
			return
		}
		if hello.Region != proxycrypto.RegionUS {
			s.reject(remote, errors.New("unsupported native region"))
			return
		}
		if c != nil && bytes.Equal(packet, c.hello) {
			c.lastSeen = now
			s.write(remote, c.handshake.ServerHello())
			return
		}
		if c != nil && now.Sub(c.created) < s.opts.MinHelloInterval {
			s.reject(remote, errors.New("new native hello throttled"))
			return
		}
		if c == nil && len(s.connections) >= s.opts.MaxSessions {
			s.reject(remote, errors.New("native session limit reached"))
			return
		}
		id := s.allocateID()
		h, wire, err := proxycrypto.BeginServerHandshake(packet, id, s.opts.Random)
		if err != nil {
			s.reject(remote, err)
			return
		}
		if s.nextGeneration == ^uint64(0) {
			s.reject(remote, proxycrypto.ErrState)
			return
		}
		s.nextGeneration++
		if c != nil {
			s.dropLocked(remote, c)
		}
		c = &connection{id: id, generation: s.nextGeneration, hello: bytes.Clone(packet), handshake: h, region: hello.Region, created: now, lastSeen: now, unsupported: make(map[uint16]bool)}
		s.connections[remote] = c
		s.write(remote, wire)
		return
	}
	if c == nil || binary.BigEndian.Uint16(packet[2:4]) != c.id {
		s.reject(remote, proxycrypto.ErrState)
		return
	}
	if flags == 4 && command == 3 {
		finish, keys, err := c.handshake.Complete(packet)
		if err != nil {
			s.reject(remote, err)
			return
		}
		if c.sender == nil {
			c.sender, _ = proxycrypto.NewSender(keys, proxycrypto.ProxyToClient)
			c.receiver, _ = proxycrypto.NewReceiver(keys, proxycrypto.ClientToProxy)
			s.opts.Log.Info("native transport keys established", "remote", remote, "session", c.id, "account_authenticated", false)
		}
		c.lastSeen = now
		s.write(remote, finish)
		return
	}
	if c.receiver == nil {
		s.reject(remote, proxycrypto.ErrState)
		return
	}
	plain, _, err := c.receiver.Open(packet)
	if err != nil {
		s.reject(remote, err)
		return
	}
	c.lastSeen = now
	flags = binary.BigEndian.Uint16(plain[:2])
	command = binary.BigEndian.Uint16(plain[4:6])
	payload := plain[proxycrypto.HeaderSize:]
	if flags == 5 {
		switch command {
		case 29:
			if len(payload) != 0 {
				s.reject(remote, proxycrypto.ErrPacket)
				return
			}
			c.activated = true
			response := control(c.id, 30, nil)
			wire, err := c.sender.Seal(response)
			if err != nil {
				s.reject(remote, err)
				return
			}
			s.write(remote, wire)
		case 38:
			if !c.activated || len(payload) != 4 || binary.BigEndian.Uint32(payload) != c.region {
				s.reject(remote, proxycrypto.ErrPacket)
				return
			}
			s.opts.Log.Debug("native region report", "remote", remote, "session", c.id, "region", c.region, "account_authenticated", false)
		case 64:
			if len(payload) != 0 {
				s.reject(remote, proxycrypto.ErrPacket)
				return
			}
			s.dropLocked(remote, c)
			s.opts.Log.Debug("native transport closed", "remote", remote, "session", c.id)
		case 201:
			if !c.activated || len(payload) != 0 {
				s.reject(remote, proxycrypto.ErrPacket)
				return
			}
			// Observed client keepalive. No response layout is established.
		default:
			s.unknown(remote, c, command, "control operation not implemented")
		}
		return
	}
	if flags != 3 || !c.activated {
		s.reject(remote, proxycrypto.ErrState)
		return
	}
	ip := netip.AddrFrom4([4]byte(plain[6:10]))
	destination := netip.AddrPortFrom(ip, command)
	handler := s.opts.Services[command]
	batchHandler := s.opts.BatchServices[command]
	if ip != s.opts.PublicIP || (handler == nil && batchHandler == nil) {
		s.unknown(remote, c, command, "destination is not a registered server-owned service")
		return
	}
	info := c.info(remote)
	// Local application handlers may take their own locks or inspect Stats.
	s.mu.Unlock()
	var responses [][]byte
	if batchHandler != nil {
		responses, err = batchHandler(ctx, info, destination, bytes.Clone(payload))
	} else {
		var response []byte
		response, err = handler(ctx, info, destination, bytes.Clone(payload))
		if len(response) > 0 {
			responses = [][]byte{response}
		}
	}
	s.mu.Lock()
	if err != nil {
		s.unknown(remote, c, command, err.Error())
		return
	}
	if s.connections[remote] != c || ctx.Err() != nil || time.Since(c.lastSeen) >= s.opts.SessionTTL {
		return
	}
	s.applications.Add(1)
	if len(responses) > MaxReplyBatch {
		s.reject(remote, proxycrypto.ErrPacket)
		return
	}
	for _, response := range responses {
		if len(response) == 0 || len(response) > proxycrypto.MaxPlaintext-proxycrypto.HeaderSize {
			s.reject(remote, proxycrypto.ErrPacket)
			return
		}
	}
	for _, response := range responses {
		message := make([]byte, proxycrypto.HeaderSize+len(response))
		binary.BigEndian.PutUint16(message[:2], 2)
		binary.BigEndian.PutUint16(message[2:4], c.id)
		binary.BigEndian.PutUint16(message[4:6], command)
		address := s.opts.PublicIP.As4()
		copy(message[6:10], address[:])
		copy(message[10:], response)
		wire, err := c.sender.Seal(message)
		if err != nil {
			s.reject(remote, err)
			return
		}
		s.write(remote, wire)
	}

}

func control(session, command uint16, payload []byte) []byte {
	message := make([]byte, proxycrypto.HeaderSize+len(payload))
	binary.BigEndian.PutUint16(message[:2], 4)
	binary.BigEndian.PutUint16(message[2:4], session)
	binary.BigEndian.PutUint16(message[4:6], command)
	copy(message[10:], payload)
	return message
}

func (c *connection) info(remote netip.AddrPort) SessionInfo {
	return SessionInfo{Remote: remote, ID: c.id, Generation: c.generation, Region: c.region, TransportActivated: c.activated}
}

// Caller holds mu. Notifications are drained only after releasing the mutex.
func (s *Server) dropLocked(remote netip.AddrPort, c *connection) {
	if s.connections[remote] != c {
		return
	}
	delete(s.connections, remote)
	if s.opts.OnDisconnect != nil {
		s.disconnected = append(s.disconnected, c.info(remote))
	}
}
func (s *Server) unlockAndNotify() {
	removed := s.disconnected
	s.disconnected = nil
	s.mu.Unlock()
	if s.opts.OnDisconnect != nil {
		for _, info := range removed {
			s.opts.OnDisconnect(info)
		}
	}
}
func (s *Server) dropAll() {
	s.mu.Lock()
	for remote, c := range s.connections {
		s.dropLocked(remote, c)
	}
	s.unlockAndNotify()
}
