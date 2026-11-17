// Package game defines the endpoints composed by a game adapter.
package game

import (
	"context"
	"net"
	"net/http"
	"net/netip"
)

type Definition struct {
	Key       string
	UDPPorts  []int
	HTTP      http.Handler
	Mode      string
	NewUDP    func(conn *net.UDPConn, observe func(string, netip.AddrPort, netip.AddrPort, []byte) error) (UDPService, error)
	Auxiliary []UDPService
}

type UDPService interface {
	Serve(context.Context) error
	Close() error
}
