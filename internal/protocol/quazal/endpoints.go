package quazal

import (
	"errors"
	"net"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
)

// NewEndpoints composes auth and secure roles over two application-owned
// sockets, sharing one account/ticket store. The advertised secure port always
// comes from the actual second socket, including ephemeral test binds.
func NewEndpoints(sockets []*net.UDPConn, options Options, transport [2]prudp.Options) ([]*prudp.Server, error) {
	if len(sockets) != 2 || sockets[0] == nil || sockets[1] == nil || sockets[0] == sockets[1] {
		return nil, errors.New("quazal: two distinct auth/secure sockets required")
	}
	for _, conn := range sockets {
		addr, ok := conn.LocalAddr().(*net.UDPAddr)
		if !ok || !addr.AddrPort().Addr().Is4() || conn.RemoteAddr() != nil {
			return nil, errors.New("quazal: unconnected IPv4 sockets required")
		}
	}
	options.SecurePort = sockets[1].LocalAddr().(*net.UDPAddr).AddrPort().Port()
	var servers []*prudp.Server
	for i, role := range []Role{Auth, Secure} {
		options.Role = role
		handler, err := New(options)
		if err != nil {
			for _, s := range servers {
				_ = s.Close()
			}
			return nil, err
		}
		opts := transport[i]
		opts.Handler = handler
		opts.Conn = sockets[i]
		opts.Bind = sockets[i].LocalAddr().(*net.UDPAddr).AddrPort()
		server, err := prudp.New(opts)
		if err != nil {
			for _, s := range servers {
				_ = s.Close()
			}
			return nil, err
		}
		servers = append(servers, server)
	}
	return servers, nil
}
