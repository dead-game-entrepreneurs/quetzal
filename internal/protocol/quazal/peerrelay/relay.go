// Package peerrelay forwards admitted native Quazal peer datagrams without
// decrypting gameplay. Title adapters authorize offers and own room policy.
package peerrelay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

const (
	relayIdle         = 180 * time.Second
	relayCandidateTTL = 30 * time.Second
	relayProofTTL     = 3 * time.Second
	relayMaxPacket    = 2048
	relayMaxBuffered  = 8
	relayDrain        = 5 * time.Second
)

type Identity struct {
	Owner Owner
	Peer  prudp.Peer
	CID   uint32
	URL   string
}
type relayPairKey struct {
	gid  uint32
	a, b Owner
}
type relayMapping struct {
	address     netip.AddrPort
	proved      bool
	nonce       [8]byte
	until, sent time.Time
	buffer      [][]byte
}
type relayPair struct {
	key        relayPairKey
	identities [2]Identity
	// socket i represents player i to the other player. Its incoming packets
	// are from 1-i, so each pair retains separate endpoint-dependent mappings.
	sockets                 [2]*net.UDPConn
	urls                    [2]string
	mapping                 [2]relayMapping
	created, last, reported time.Time
	active                  bool
	closing                 time.Time
	packets, bytes          [2]uint64
	tokens                  float64
	budgetAt                time.Time
}
type Relay struct {
	mu           sync.Mutex
	cfg          Config
	log          *slog.Logger
	pairs        map[relayPairKey]*relayPair
	ports        map[uint16]bool
	instructions []quazal.ProbeInstruction
	closed       bool
	done         chan struct{}
	wg           sync.WaitGroup
	now          func() time.Time
	listen       func(uint16) (*net.UDPConn, error)
}

func New(options Options) (*Relay, error) {
	cfg := options.Config
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	log := options.Log
	if log == nil {
		log = slog.Default()
	}
	r := &Relay{cfg: cfg, log: log, pairs: map[relayPairKey]*relayPair{}, ports: map[uint16]bool{}, now: time.Now, done: make(chan struct{})}
	if options.Now != nil {
		r.now = options.Now
	}
	r.listen = options.Listen
	if r.listen == nil {
		r.listen = func(port uint16) (*net.UDPConn, error) {
			return net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(cfg.Bind, port)))
		}
	}
	return r, nil
}

func stationField(url, key string) string {
	_, body, _ := strings.Cut(url, ":/")
	for _, part := range strings.Split(body, ";") {
		k, v, ok := strings.Cut(part, "=")
		if ok && k == key {
			return v
		}
	}
	return ""
}
func relayStationURL(original string, address netip.AddrPort, cid uint32) string {
	_, body, _ := strings.Cut(original, ":/")
	parts := []string{"address=" + address.Addr().String(), "port=" + strconv.Itoa(int(address.Port()))}
	for _, part := range strings.Split(body, ";") {
		key, _, _ := strings.Cut(part, "=")
		if key != "address" && key != "port" && key != "CID" && key != "RVCID" && key != "type" {
			parts = append(parts, part)
		}
	}
	parts = append(parts, "CID="+strconv.FormatUint(uint64(cid), 10), "RVCID="+strconv.FormatUint(uint64(cid), 10), "type=2")
	return "prudp:/" + strings.Join(parts, ";")
}
func (r *Relay) allocate() (*net.UDPConn, error) {
	for port := int(r.cfg.FirstPort); port <= int(r.cfg.LastPort); port++ {
		if r.ports[uint16(port)] {
			continue
		}
		conn, err := r.listen(uint16(port))
		if err != nil {
			continue
		}
		r.ports[conn.LocalAddr().(*net.UDPAddr).AddrPort().Port()] = true
		return conn, nil
	}
	return nil, errors.New("native relay port pool exhausted")
}

// offer is called only after authenticated accessible-room lookup. The real
// registered peers, never a client-supplied address, determine proof targets.
func (r *Relay) Offer(gid uint32, a, b Identity, direct string) []string {
	if r == nil || r.cfg.Mode == "direct" || a.Owner == b.Owner {
		return []string{direct}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || a.CID == 0 || b.CID == 0 {
		return r.unavailable(direct)
	}
	key := relayPairKey{gid, a.Owner, b.Owner}
	p := r.pairs[key]
	remote := 1
	if p == nil {
		p = r.pairs[relayPairKey{gid, b.Owner, a.Owner}]
		if p != nil {
			remote = 0
		}
	}
	if p != nil && !p.closing.IsZero() {
		return r.unavailable(direct)
	}
	if p == nil {
		if len(r.pairs) >= int(r.cfg.LastPort-r.cfg.FirstPort+1)/2 {
			return r.unavailable(direct)
		}
		var conns [2]*net.UDPConn
		for i := range conns {
			var err error
			conns[i], err = r.allocate()
			if err != nil {
				for _, conn := range conns {
					if conn != nil {
						delete(r.ports, conn.LocalAddr().(*net.UDPAddr).AddrPort().Port())
						conn.Close()
					}
				}
				r.log.Warn("native relay allocation failed", "reason", err.Error())
				return r.unavailable(direct)
			}
		}
		now := r.now()
		p = &relayPair{key: key, identities: [2]Identity{a, b}, sockets: conns, created: now, last: now, reported: now, tokens: 512, budgetAt: now}
		for i := range conns {
			port := conns[i].LocalAddr().(*net.UDPAddr).AddrPort().Port()
			p.urls[i] = relayStationURL(p.identities[i].URL, netip.AddrPortFrom(r.cfg.PublicIP, port), p.identities[i].CID)
		}
		r.pairs[key] = p
		for i := range conns {
			r.wg.Add(1)
			go r.read(p, i)
		}
		r.log.Info("native relay candidate", "gid", gid, "connector", a.Owner.PID, "host", b.Owner.PID, "port_a", conns[0].LocalAddr().(*net.UDPAddr).Port, "port_b", conns[1].LocalAddr().(*net.UDPAddr).Port, "mode", r.cfg.Mode)
	}
	if r.cfg.Mode == "relay" {
		return []string{p.urls[remote]}
	}
	// Both addresses retain the host's CID/RVCID and public type. Retail's
	// 0x2d34e8 preserves order within this type; 0x2c9750 advances on failure.
	return []string{direct, p.urls[remote]}
}

func (r *Relay) unavailable(direct string) []string {
	if r.cfg.Mode == "relay" {
		return nil
	}
	return []string{direct}
}

// Resolve only a virtual URL issued to this exact authenticated connection.
// The session adapter separately rechecks live room access before activation.
func (r *Relay) ProbeTarget(caller Owner, url string) (Owner, uint32, bool) {
	if r == nil {
		return Owner{}, 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.pairs {
		if !p.closing.IsZero() {
			continue
		}
		for side, identity := range p.identities {
			if identity.Owner == caller && p.urls[1-side] == url {
				return p.identities[1-side].Owner, p.key.gid, true
			}
		}
	}
	return Owner{}, 0, false
}

func (r *Relay) activate(p *relayPair, source int, reason string) {
	if p.active {
		return
	}
	p.active = true
	p.last = r.now()
	if len(r.instructions) < 128 {
		r.instructions = append(r.instructions, quazal.ProbeInstruction{Peer: p.identities[1-source].Peer, URL: p.urls[source]})
	}
	r.log.Info("native relay activated", "gid", p.key.gid, "connector", p.identities[source].Owner.PID, "host", p.identities[1-source].Owner.PID, "reason", reason)
}

func (r *Relay) RequestProbe(caller Owner, url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if (r.cfg.Mode != "relay" && !r.cfg.IntroduceCandidates) || r.closed {
		return
	}
	for _, p := range r.pairs {
		if !p.closing.IsZero() {
			continue
		}
		for side, identity := range p.identities {
			if identity.Owner == caller && p.urls[1-side] == url {
				if r.cfg.Mode == "relay" {
					r.activate(p, side, "forced native introduction")
				} else {
					instruction := quazal.ProbeInstruction{Peer: p.identities[1-side].Peer, URL: p.urls[side]}
					found := false
					for _, old := range r.instructions {
						found = found || old == instruction
					}
					if !found && len(r.instructions) < 128 {
						r.instructions = append(r.instructions, instruction)
					}
				}
				return
			}
		}
	}
}

func (r *Relay) read(p *relayPair, destination int) {
	defer r.wg.Done()
	buf := make([]byte, relayMaxPacket+1)
	for {
		n, from, err := p.sockets[destination].ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		if n > relayMaxPacket {
			continue
		}
		r.receive(p, destination, from, buf[:n])
	}
}

func (r *Relay) send(p *relayPair, socket int, to netip.AddrPort, wire []byte) {
	_, _ = p.sockets[socket].WriteToUDPAddrPort(wire, to)
}
func probePacket(kind byte, cid uint32, nonce []byte) []byte {
	payload := make([]byte, 13)
	payload[0] = kind
	binary.LittleEndian.PutUint32(payload[1:], cid)
	copy(payload[5:], nonce)
	// Native peer streams use seed 0, independently of title backend keys.
	wire, _ := (prudp.Packet{Source: 0x51, Destination: 0x51, Type: prudp.USER, Payload: payload}).Marshal(0)
	return wire
}

func (r *Relay) challenge(p *relayPair, source int, from netip.AddrPort, now time.Time) {
	m := &p.mapping[source]
	if !m.sent.IsZero() && now.Sub(m.sent) < 500*time.Millisecond {
		return
	}
	if now.After(m.until) || m.address != from {
		if _, err := rand.Read(m.nonce[:]); err != nil {
			return
		}
		m.address = from
		m.until = now.Add(relayProofTTL)
		m.proved = false
	}
	m.sent = now
	r.send(p, 1-source, from, probePacket(0, p.identities[1-source].CID, m.nonce[:]))
}

func (r *Relay) receive(p *relayPair, destination int, from netip.AddrPort, wire []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.pairs[p.key] != p {
		return
	}
	source := 1 - destination
	now := r.now()
	if from.Addr() != p.identities[source].Peer.Addr.Addr() {
		return
	}
	// Bound work and bandwidth before parsing or sending any response.
	p.tokens = min(512, p.tokens+max(0, now.Sub(p.budgetAt).Seconds())*256)
	p.budgetAt = now
	if p.tokens < 1 {
		return
	}
	p.tokens--
	m := &p.mapping[source]
	packet, consumed, err := prudp.ParseFrame(wire, 0)
	// A proved mapping belongs to this exact control-connection generation.
	// Reconnect allocates a new route instead of accepting unsolicited rebinding.
	if m.proved && m.address != from {
		return
	}
	if !p.closing.IsZero() {
		if !now.Before(p.closing) {
			r.remove(p, "exit drain complete")
			return
		}
		// Retain only the already proved route for native leave DATA and ACKs.
		// It cannot admit a new handshake, mapping or traversal instruction.
		if !m.proved || !p.mapping[1-source].proved || err != nil {
			return
		}
		switch packet.Type {
		case prudp.DATA, prudp.DISCONNECT, prudp.PING, prudp.ACK:
			r.forward(p, source, wire, now)
		}
		return
	}
	nativeProbe := err == nil && consumed == len(wire) && packet.Type == prudp.USER && packet.Source == 0x51 && packet.Destination == 0x51 && len(packet.Payload) == 13
	if nativeProbe {
		body := packet.Payload
		if binary.LittleEndian.Uint32(body[1:]) != p.identities[source].CID {
			return
		}
		if body[0] == 1 {
			if m.address != from || !now.Before(m.until) || !bytes.Equal(body[5:], m.nonce[:]) {
				return
			}
			if !m.proved {
				r.log.Info("native relay mapping proved", "gid", p.key.gid, "pid", p.identities[source].Owner.PID, "source", from.String())
			}
			m.proved = true
			r.flush(p, now)
			return
		}
		if body[0] != 0 || (!p.active && !r.cfg.ProbeCandidates) {
			return
		}
		if !p.active && m.sent.IsZero() {
			r.log.Info("native relay reachability probe", "gid", p.key.gid, "pid", p.identities[source].Owner.PID, "active", false)
		}
		r.send(p, destination, from, probePacket(1, p.identities[destination].CID, body[5:]))
		if !m.proved || m.address != from {
			r.challenge(p, source, from, now)
		}
		return
	}
	if !p.active {
		// Candidate publication and probe ACKs are not evidence of failure.
		// Only a native connection SYN selecting the alternate activates it.
		if err != nil || consumed != len(wire) || packet.Type != prudp.SYN || packet.Flags&prudp.FlagAck != 0 || packet.Source>>4 != 1 || packet.Destination>>4 != 1 {
			return
		}
		r.activate(p, source, "native alternate SYN")
	}
	if !m.proved || m.address != from {
		if len(m.buffer) < relayMaxBuffered {
			m.buffer = append(m.buffer, bytes.Clone(wire))
		}
		r.challenge(p, source, from, now)
		return
	}
	if !p.mapping[1-source].proved {
		if len(m.buffer) < relayMaxBuffered {
			m.buffer = append(m.buffer, bytes.Clone(wire))
		}
		return
	}
	r.forward(p, source, wire, now)
}
func (r *Relay) forward(p *relayPair, source int, wire []byte, now time.Time) {
	r.send(p, source, p.mapping[1-source].address, wire)
	p.packets[source]++
	p.bytes[source] += uint64(len(wire))
	p.last = now
}
func (r *Relay) flush(p *relayPair, now time.Time) {
	if !p.active || !p.mapping[0].proved || !p.mapping[1].proved {
		return
	}
	for source := range p.mapping {
		for _, wire := range p.mapping[source].buffer {
			r.forward(p, source, wire, now)
		}
		p.mapping[source].buffer = nil
	}
}
func (r *Relay) DrainInstructions() []quazal.ProbeInstruction {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.instructions
	r.instructions = nil
	return out
}
func (r *Relay) remove(p *relayPair, reason string) {
	delete(r.pairs, p.key)
	for _, conn := range p.sockets {
		delete(r.ports, conn.LocalAddr().(*net.UDPAddr).AddrPort().Port())
		conn.Close()
	}
	// Remove unsent introductions for the removed generation too.
	out := r.instructions[:0]
	for _, instruction := range r.instructions {
		if instruction.URL != p.urls[0] && instruction.URL != p.urls[1] {
			out = append(out, instruction)
		}
	}
	r.instructions = out
	r.log.Info("native relay released", "gid", p.key.gid, "reason", reason, "active", p.active, "packets", p.packets, "bytes", p.bytes)
}
func (r *Relay) RemoveParticipant(owner Owner, gid uint32) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.pairs {
		if (gid == 0 || p.key.gid == gid) && (p.key.a == owner || p.key.b == owner) {
			if p.active && p.mapping[0].proved && p.mapping[1].proved {
				if p.closing.IsZero() {
					p.closing = r.now().Add(relayDrain)
					out := r.instructions[:0]
					for _, instruction := range r.instructions {
						if instruction.URL != p.urls[0] && instruction.URL != p.urls[1] {
							out = append(out, instruction)
						}
					}
					r.instructions = out
					r.log.Info("native relay draining", "gid", p.key.gid, "seconds", relayDrain.Seconds())
				}
			} else {
				r.remove(p, "participation ended")
			}
		}
	}
}
func (r *Relay) tick() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	for _, p := range r.pairs {
		if !p.closing.IsZero() {
			if !now.Before(p.closing) {
				r.remove(p, "exit drain complete")
			}
			continue
		}
		ttl := relayCandidateTTL
		if p.active {
			ttl = relayIdle
		}
		if now.Sub(p.last) >= ttl {
			r.remove(p, "idle timeout")
			continue
		}
		for source := range p.mapping {
			m := &p.mapping[source]
			if !m.proved && !m.until.IsZero() {
				if !now.Before(m.until) {
					m.buffer = nil
					continue
				}
				r.challenge(p, source, m.address, now)
			}
		}
		if p.active && now.Sub(p.reported) >= 10*time.Second {
			r.log.Info("native relay traffic", "gid", p.key.gid, "packets", p.packets, "bytes", p.bytes)
			p.reported = now
		}
	}
}
func (r *Relay) Serve(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.tick()
		}
	}
}
func (r *Relay) Close() error {
	r.mu.Lock()
	if !r.closed {
		close(r.done)
	}
	r.closed = true
	for _, p := range r.pairs {
		r.remove(p, "shutdown")
	}
	r.mu.Unlock()
	r.wg.Wait()
	return nil
}
