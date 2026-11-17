// Adapted from sibling tdu-server/internal/prudp/server.go.
package prudp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

var ErrClosed = errors.New("PRUDP server is closed")

type Server struct {
	conn       *net.UDPConn
	opts       Options
	secret     [32]byte
	observeErr error
	serving    atomic.Bool
	closed     atomic.Bool
	closeOnce  sync.Once
	nextID     uint64
	peers      map[peerKey]*connection
}
type peerKey struct {
	addr                netip.AddrPort
	source, destination byte
}
type transmission struct {
	wire           []byte
	kind, fragment byte
	sent           time.Time
	retries        int
}
type connection struct {
	peer                       Peer
	remoteCookie               uint32
	session                    byte
	expected, next             uint16
	received                   map[uint16]Packet
	receivedBytes              int
	pending                    map[uint16]*transmission
	fragments                  []byte
	fragmentNext               byte
	fragmentStarted            time.Time
	last                       time.Time
	connectRequest, connectAck []byte
	localCookie                uint32
	reconnectCookie            uint32
	reconnectSignature         uint32
	reconnectUntil             time.Time
}

func New(opts Options) (*Server, error) {
	if !opts.Bind.IsValid() || !opts.Bind.Addr().Is4() || opts.Handler == nil {
		return nil, errors.New("PRUDP requires an IPv4 bind address and handler")
	}
	if opts.MaxPeers == 0 {
		opts.MaxPeers = 128
	}
	if opts.MaxInFlight == 0 {
		opts.MaxInFlight = 64
	}
	if opts.MaxMessageSize == 0 {
		opts.MaxMessageSize = 65536
	}
	if opts.MaxRetries == 0 {
		opts.MaxRetries = 5
	}
	if opts.RetryInterval == 0 {
		opts.RetryInterval = 500 * time.Millisecond
	}
	if opts.HandshakeTimeout == 0 {
		opts.HandshakeTimeout = 10 * time.Second
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 2 * time.Minute
	}
	if opts.MaxPeers < 1 || opts.MaxPeers > 65536 || opts.MaxInFlight < 1 || opts.MaxInFlight > 4096 || opts.MaxMessageSize < 1 || opts.MaxMessageSize > 4<<20 || opts.MaxRetries < 1 || opts.MaxRetries > 32 || opts.RetryInterval < time.Millisecond || opts.HandshakeTimeout < time.Millisecond || opts.IdleTimeout < time.Millisecond {
		return nil, errors.New("invalid PRUDP limits")
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Server{opts: opts, peers: make(map[peerKey]*connection)}
	if _, err := rand.Read(s.secret[:]); err != nil {
		return nil, err
	}
	conn := opts.Conn
	if conn == nil {
		var err error
		conn, err = net.ListenUDP("udp4", net.UDPAddrFromAddrPort(opts.Bind))
		if err != nil {
			return nil, err
		}
	}
	s.conn = conn
	return s, nil
}
func (s *Server) Addr() netip.AddrPort { return s.conn.LocalAddr().(*net.UDPAddr).AddrPort() }
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() { s.closed.Store(true); err = s.conn.Close() })
	return err
}

// Serve owns transport state and serializes every handler callback. Cancellation
// closes the socket. Callbacks must respect their context to allow prompt stop.
func (s *Server) Serve(ctx context.Context) error {
	if !s.serving.CompareAndSwap(false, true) {
		return errors.New("PRUDP Serve called more than once")
	}
	if s.closed.Load() {
		return ErrClosed
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-done:
		}
	}()
	defer close(done)
	defer s.Close()
	defer func() {
		for k := range s.peers {
			s.drop(k)
		}
	}()
	buf := make([]byte, 65535)
	tick := min(100*time.Millisecond, s.opts.RetryInterval/2, s.opts.IdleTimeout/2, s.opts.HandshakeTimeout/2)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.conn.SetReadDeadline(time.Now().Add(tick)); err != nil {
			if s.closed.Load() {
				return nil
			}
			return err
		}
		n, addr, err := s.conn.ReadFromUDPAddrPort(buf)
		now := time.Now()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if s.closed.Load() {
				return nil
			}
			if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
				return err
			}
		} else {
			if err := s.observe("rx", addr, buf[:n]); err != nil {
				return err
			}
			// A bounded datagram may carry several explicitly sized frames.
			for offset, frames := 0, 0; offset < n && frames < 128; frames++ {
				p, used, e := ParseFrame(buf[offset:n], s.opts.ChecksumSeed)
				if e != nil {
					break
				}
				offset += used
				s.receive(ctx, addr, p, now)
			}
		}
		s.expire(now)
		s.drainOutbound(now)
		if s.observeErr != nil {
			return s.observeErr
		}
	}
}

func (s *Server) cookie(addr netip.AddrPort) uint32 {
	h := hmac.New(md5.New, s.secret[:])
	ip := addr.Addr().As4()
	h.Write(ip[:])
	var port [2]byte
	binary.BigEndian.PutUint16(port[:], addr.Port())
	h.Write(port[:])
	return binary.LittleEndian.Uint32(h.Sum(nil))
}
func (s *Server) write(addr netip.AddrPort, b []byte) error {
	_, err := s.conn.WriteToUDPAddrPort(b, addr)
	if err != nil {
		return err
	}
	return s.observe("tx", addr, b)
}
func (s *Server) send(addr netip.AddrPort, p Packet) ([]byte, error) {
	b, err := p.Marshal(s.opts.ChecksumSeed)
	if err != nil {
		return nil, err
	}
	return b, s.write(addr, b)
}
func (s *Server) base(c *connection, kind, flags byte, seq uint16) Packet {
	return Packet{Source: c.peer.DestinationStream, Destination: c.peer.SourceStream, Type: kind, Flags: flags, Session: c.session, Signature: c.remoteCookie, Sequence: seq}
}
func (s *Server) drop(k peerKey) {
	c := s.peers[k]
	if c == nil {
		return
	}
	delete(s.peers, k)
	s.opts.Handler.Disconnect(c.peer)
}
func (s *Server) receive(ctx context.Context, addr netip.AddrPort, p Packet, now time.Time) {
	// Only the type-3 Quazal RPC stream is enabled. USER,
	// flag0x80 and other stream settings require separate evidence and handlers.
	if p.Source>>4 != 3 || p.Destination>>4 != 3 || p.Flags&0x80 != 0 || p.Type == USER {
		return
	}
	k := peerKey{addr, p.Source, p.Destination}
	if p.Type == SYN {
		if p.Flags&FlagAck != 0 || len(p.Payload) != 0 {
			return
		}
		cookie := s.cookie(addr)
		if c := s.peers[k]; c != nil {
			// A reboot can reuse the same UDP endpoint without DISCONNECT. Keep
			// the active connection until a new endpoint challenge and application
			// proof pass; a replayed CONNECT alone cannot replace it.
			if !now.Before(c.reconnectUntil) || c.reconnectSignature != p.ConnectionSignature {
				var challenge [4]byte
				if _, err := rand.Read(challenge[:]); err != nil {
					return
				}
				c.reconnectCookie = binary.LittleEndian.Uint32(challenge[:])
				if c.reconnectCookie == 0 || c.reconnectCookie == c.localCookie {
					return
				}
				c.reconnectSignature = p.ConnectionSignature
				c.reconnectUntil = now.Add(s.opts.HandshakeTimeout)
			}
			cookie = c.reconnectCookie
		}
		_, _ = s.send(addr, Packet{Source: p.Destination, Destination: p.Source, Type: SYN, Flags: FlagAck, Sequence: p.Sequence, ConnectionSignature: cookie})
		return
	}
	cookie := s.cookie(addr)
	previous := s.peers[k]
	if previous != nil {
		cookie = previous.localCookie
	}
	reconnecting := previous != nil && now.Before(previous.reconnectUntil) && p.Signature == previous.reconnectCookie
	if p.Signature != cookie && !(p.Type == CONNECT && reconnecting) {
		return
	}
	if p.Type == CONNECT && p.Flags&FlagAck == 0 {
		if p.Session == 0 || p.Sequence != 1 || p.Flags&(FlagReliable|FlagNeedsAck) != (FlagReliable|FlagNeedsAck) {
			return
		}
		if c := previous; c != nil && !reconnecting {
			if p.Signature == c.localCookie && c.peer.ClientSession == p.Session && c.remoteCookie == p.ConnectionSignature && bytes.Equal(c.connectRequest, p.Payload) {
				c.last = now
				_ = s.write(addr, c.connectAck)
			}
			return
		}
		if previous == nil && len(s.peers) >= s.opts.MaxPeers {
			return
		}
		payload, err := s.decodePayload(p.Payload)
		if err != nil {
			return
		}
		s.nextID++
		peer := Peer{ID: s.nextID, Addr: addr, LocalPort: s.Addr().Port(), SourceStream: p.Source, DestinationStream: p.Destination, ClientSession: p.Session}
		callCtx, cancel := context.WithTimeout(ctx, s.opts.HandshakeTimeout)
		result, err := s.opts.Handler.Connect(callCtx, peer, payload)
		cancel()
		if err != nil {
			s.opts.Log.Debug("PRUDP CONNECT rejected", "peer", peer.ID)
			return
		}
		if len(result.Payload) > s.opts.MaxMessageSize || len(result.Payload) > 65000 {
			s.opts.Handler.Disconnect(peer)
			return
		}
		var session [1]byte
		if _, err = rand.Read(session[:]); err != nil {
			s.opts.Handler.Disconnect(peer)
			return
		}
		if session[0] == 0 {
			session[0] = 1
		}
		c := &connection{peer: peer, localCookie: p.Signature, remoteCookie: p.ConnectionSignature, session: session[0], expected: 2, next: 1, received: make(map[uint16]Packet), pending: make(map[uint16]*transmission), fragmentNext: 1, last: now, connectRequest: append([]byte(nil), p.Payload...)}
		ack := s.base(c, CONNECT, FlagAck, p.Sequence)
		ack.Payload = s.encodePayload(result.Payload)
		c.connectAck, err = s.send(addr, ack)
		if err != nil {
			s.opts.Handler.Disconnect(peer)
			return
		}
		if previous != nil {
			s.drop(k)
			s.opts.Log.Info("PRUDP connection replaced after fresh handshake", "peer", peer.ID, "local_port", peer.LocalPort)
		}
		s.peers[k] = c
		s.opts.Log.Debug("PRUDP connected", "peer", peer.ID, "local_port", peer.LocalPort)
		return
	}
	c := s.peers[k]
	if c == nil || p.Session != c.peer.ClientSession {
		return
	}
	// Reject malformed compression even on duplicate packets, before mutation.
	payload, err := s.decodePayload(p.Payload)
	if err != nil {
		return
	}
	p.Payload = payload
	c.last = now
	if p.Flags&FlagAck != 0 || p.Type == ACK {
		if pending := c.pending[p.Sequence]; pending != nil && (p.Type == ACK || p.Type == pending.kind) && (p.Type != DATA || p.Fragment == pending.fragment) {
			delete(c.pending, p.Sequence)
		}
		return
	}
	if p.Type != DATA && p.Type != PING && p.Type != DISCONNECT {
		return
	}
	// Do not acknowledge data outside our bounded receive window: the sender
	// must retain it until preceding packets make space. Old duplicates may
	// still be acknowledged, since they were already delivered.
	if p.Flags&FlagReliable != 0 {
		distance := uint16(p.Sequence - c.expected)
		if distance < 0x8000 && int(distance) >= s.opts.MaxInFlight {
			return
		}
		if _, exists := c.received[p.Sequence]; !exists && distance < 0x8000 && len(p.Payload) > s.opts.MaxMessageSize-c.receivedBytes-len(c.fragments) {
			return
		}
	}
	if p.Flags&FlagNeedsAck != 0 {
		ack := s.base(c, p.Type, FlagAck, p.Sequence)
		ack.Fragment = p.Fragment
		if _, err = s.send(addr, ack); err != nil {
			s.drop(k)
			return
		}
	}
	if p.Type == DISCONNECT {
		s.drop(k)
		return
	}
	if p.Type == PING && p.Flags&FlagReliable == 0 {
		return
	}
	if p.Flags&FlagReliable == 0 {
		// Native RPC uses reliable DATA. Unreliable gameplay is a different layer;
		// do not dispatch unaudited/unordered RMC side effects through this endpoint.
		return
	}
	distance := uint16(p.Sequence - c.expected)
	if distance >= 0x8000 || int(distance) >= s.opts.MaxInFlight {
		return
	}
	if _, exists := c.received[p.Sequence]; exists {
		return
	}
	c.received[p.Sequence] = p
	c.receivedBytes += len(p.Payload)
	for {
		current, exists := c.received[c.expected]
		if !exists {
			return
		}
		delete(c.received, c.expected)
		c.receivedBytes -= len(current.Payload)
		c.expected++
		if current.Type != DATA {
			continue
		}
		message, complete, e := s.assemble(c, current, now)
		if e != nil {
			s.drop(k)
			return
		}
		if !complete {
			continue
		}
		responses, e := s.opts.Handler.Handle(ctx, c.peer, message)
		if e != nil {
			s.opts.Log.Debug("PRUDP request rejected", "peer", c.peer.ID)
			s.drop(k)
			return
		}
		if e = s.respond(c, responses, now); e != nil {
			s.opts.Log.Debug("PRUDP response exceeds transport bounds", "peer", c.peer.ID)
			s.drop(k)
			return
		}
		s.drainOutbound(now)
	}
}

func (s *Server) drainOutbound(now time.Time) {
	if handler, ok := s.opts.Handler.(OutboundHandler); ok {
		for _, message := range handler.DrainOutbound() {
			key := peerKey{message.Peer.Addr, message.Peer.SourceStream, message.Peer.DestinationStream}
			target := s.peers[key]
			if target == nil || target.peer != message.Peer {
				continue // an expired/replaced connection must never receive this
			}
			if err := s.respond(target, [][]byte{message.Payload}, now); err != nil {
				s.opts.Log.Warn("PRUDP outbound message rejected", "peer", target.peer.ID)
			}
		}
	}
}
func (s *Server) assemble(c *connection, p Packet, now time.Time) ([]byte, bool, error) {
	if c.fragmentStarted.IsZero() {
		if p.Fragment == 0 {
			return p.Payload, true, nil
		}
		if p.Fragment != 1 {
			return nil, false, ErrPacket
		}
		c.fragmentStarted = now
		c.fragmentNext = 1
	}
	if p.Fragment != 0 && p.Fragment != c.fragmentNext {
		return nil, false, ErrPacket
	}
	if len(p.Payload) > s.opts.MaxMessageSize-len(c.fragments) {
		return nil, false, ErrPacket
	}
	c.fragments = append(c.fragments, p.Payload...)
	if p.Fragment == 0 {
		message := c.fragments
		c.fragments = nil
		c.fragmentStarted = time.Time{}
		c.fragmentNext = 1
		return message, true, nil
	}
	c.fragmentNext++
	if c.fragmentNext == 0 {
		c.fragmentNext = 1
	}
	return nil, false, nil
}
func (s *Server) respond(c *connection, responses [][]byte, now time.Time) error {
	// A conservative UDP payload avoids IP fragmentation. Each reliable PRUDP
	// fragment is independently wrapped/encrypted; the final fragment ID is zero.
	const chunk = 1000
	total := 0
	for _, message := range responses {
		if len(message) > s.opts.MaxMessageSize {
			return ErrPacket
		}
		count := max(1, (len(message)+chunk-1)/chunk)
		total += count
		if total > s.opts.MaxInFlight-len(c.pending) {
			return ErrPacket
		}
	}
	for _, message := range responses {
		count := max(1, (len(message)+chunk-1)/chunk)
		fragment := byte(1)
		for i := 0; i < count; i++ {
			start := i * chunk
			end := min(len(message), start+chunk)
			if _, exists := c.pending[c.next]; exists {
				return ErrPacket
			}
			for seq := range c.pending {
				if uint16(c.next-seq) >= 0x8000 {
					return ErrPacket
				}
			}
			p := s.base(c, DATA, FlagReliable|FlagNeedsAck|FlagSize, c.next)
			c.next++
			if i < count-1 {
				p.Fragment = fragment
				fragment++
				if fragment == 0 {
					fragment = 1
				}
			}
			p.Payload = s.encodePayload(message[start:end])
			wire, err := s.send(c.peer.Addr, p)
			if err != nil {
				return err
			}
			c.pending[p.Sequence] = &transmission{wire: wire, kind: DATA, fragment: p.Fragment, sent: now}
		}
	}
	return nil
}
func (s *Server) expire(now time.Time) {
	for k, c := range s.peers {
		if now.Sub(c.last) >= s.opts.IdleTimeout || (!c.fragmentStarted.IsZero() && now.Sub(c.fragmentStarted) >= s.opts.HandshakeTimeout) {
			s.drop(k)
			continue
		}
		for _, p := range c.pending {
			if now.Sub(p.sent) < s.opts.RetryInterval {
				continue
			}
			if p.retries >= s.opts.MaxRetries {
				s.drop(k)
				break
			}
			if err := s.write(c.peer.Addr, p.wire); err != nil {
				s.drop(k)
				break
			}
			p.retries++
			p.sent = now
		}
	}
}

// Capture callbacks run on the owning loop. Fail the service if recording fails.
func (s *Server) observe(direction string, remote netip.AddrPort, wire []byte) error {
	if s.opts.Observe != nil {
		if err := s.opts.Observe(direction, s.Addr(), remote, wire); err != nil {
			s.observeErr = err
			return err
		}
	}
	return nil
}
func (s *Server) decodePayload(wire []byte) ([]byte, error) {
	if s.opts.OpaquePayload {
		if len(wire) > s.opts.MaxMessageSize {
			return nil, ErrPacket
		}
		return bytes.Clone(wire), nil
	}
	if s.opts.PlainPayload {
		return decodePlainPayload(wire, s.opts.MaxMessageSize)
	}
	return decodePayload(wire, s.opts.MaxMessageSize)
}
func (s *Server) encodePayload(plain []byte) []byte {
	if s.opts.OpaquePayload {
		return bytes.Clone(plain)
	}
	if s.opts.PlainPayload {
		return encodePlainPayload(plain)
	}
	return encodePayload(plain)
}
