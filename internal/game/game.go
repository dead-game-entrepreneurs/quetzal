// Package game defines the endpoints composed by a game adapter.
package game

import (
	"context"
	"net"
	"net/http"
	"net/netip"
)

// Observe records a complete raw UDP datagram, scoped to the owning tenant.
type Observe func(string, netip.AddrPort, netip.AddrPort, []byte) error

// PortRange reserves lazy UDP listeners such as a title's native relay pool.
type PortRange struct{ First, Last int }

type Definition struct {
	// Key identifies the tenant; Game identifies its adapter (defaults to Key).
	Key         string
	Game        string
	Bind        string
	ReservedUDP []PortRange
	// HTTPPaths are original retail/diagnostic paths owned by this tenant.
	// Every tenant additionally gets /games/{Key}/ as a scoped HTTP mount.
	HTTPPaths []string
	UDPPorts  []int
	HTTP      http.Handler
	Mode      string
	NewUDP    func(conn *net.UDPConn, observe func(string, netip.AddrPort, netip.AddrPort, []byte) error) (UDPService, error)
	// NewServices receives all sockets in UDPPorts order after binding. It lets
	// adapters compose protocols that share state across several endpoints.
	// It is mutually exclusive with NewUDP. No goroutines start until it returns.
	NewServices func([]*net.UDPConn, Observe) ([]UDPService, error)
	Auxiliary   []UDPService
}

type UDPService interface {
	Serve(context.Context) error
	Close() error
}
