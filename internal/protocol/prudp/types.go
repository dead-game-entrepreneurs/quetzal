// Package prudp implements the US PS2 Quazal UDP transport. Application account
// and ticket acceptance is delegated to Handler after the endpoint cookie check.
package prudp

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"time"
)

type Peer struct {
	ID                uint64
	Addr              netip.AddrPort
	LocalPort         uint16
	SourceStream      byte
	DestinationStream byte
	ClientSession     byte
}
type ConnectResult struct{ Payload []byte }
type Handler interface {
	Connect(context.Context, Peer, []byte) (ConnectResult, error)
	Handle(context.Context, Peer, []byte) ([][]byte, error)
	Disconnect(Peer)
}

// OutboundHandler optionally queues application messages for another live peer.
// The transport drains these after inbound messages and on idle ticks, on its
// own loop, so asynchronous native relay introductions are delivered promptly.
type OutboundHandler interface {
	DrainOutbound() []OutboundMessage
}
type OutboundMessage struct {
	Peer    Peer
	Payload []byte
	// Retire revokes this exact transport generation instead of sending DATA.
	// It lets account registration retire an old connection promptly, without
	// retaining a transport peer that keeps sending otherwise valid PINGs.
	Retire bool
}

// UserDatagramHandler handles a checksum-validated, connectionless USER packet.
// It grants no connected peer or RPC/account authority. The reply is a complete
// datagram: some adapters require a raw reply to a PRUDP-framed request. A nil
// reply means drop; responses go only to the observed source. Titles opt in.
type UserDatagramHandler func(context.Context, netip.AddrPort, Packet) ([]byte, error)

type Options struct {
	Bind        netip.AddrPort
	Handler     Handler
	UserHandler UserDatagramHandler
	// USER streams can have a separate checksum key from connected RPC streams.
	UserChecksumSeed byte
	Log              *slog.Logger
	ChecksumSeed     byte
	// Conn optionally supplies an application-owned, prebound socket.
	Conn *net.UDPConn
	// OpaquePayload captures application data without assuming TDU's transforms.
	// The observing handler must not treat these bytes as authenticated RPC.
	OpaquePayload bool
	// PlainPayload uses the observed 25 to Life compression wrapper without
	// TDU's additional RC4 transport layer. Ticket crypto is separate.
	PlainPayload bool
	Observe      func(direction string, local, remote netip.AddrPort, wire []byte) error
	// Zero values select bounded server defaults, independently of retail timers.
	MaxPeers         int
	MaxInFlight      int
	MaxMessageSize   int
	MaxRetries       int
	RetryInterval    time.Duration
	HandshakeTimeout time.Duration
	IdleTimeout      time.Duration
}
