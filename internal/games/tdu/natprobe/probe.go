// Package natprobe implements TDU's connectionless public-endpoint discovery.
// It reflects only the observed source; it creates no account or peer authority.
package natprobe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
)

var ErrProbe = errors.New("unsupported TDU public-endpoint probe")

// Handle receives the checksum-validated NATTraversalStream USER request.
// Unlike the request, the lrTraversalAdapter reply is a raw datagram: its four
// byte header, opcode2 and an uncounted, NUL-terminated StationURL. Receiver
// 244750 strips the header; 21bb60 reads the CString and completes JobGetPublicURL.
func Handle(ctx context.Context, source netip.AddrPort, packet prudp.Packet) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !source.IsValid() || !source.Addr().Is4() || source.Port() == 0 || source.Addr().IsUnspecified() || source.Addr().IsMulticast() || source.Addr() == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return nil, ErrProbe
	}
	if packet.Type != prudp.USER || packet.Source != 0x71 || packet.Destination != 0x71 || packet.Flags != 0 || packet.Session != 0 || packet.Signature != 0 || packet.Sequence != 0 || !bytes.Equal(packet.Payload, []byte{0xcd, 0x80, 0, 0, 2}) {
		return nil, ErrProbe
	}
	url := fmt.Sprintf("udp:/address=%s;port=%d", source.Addr(), source.Port())
	reply := append([]byte{0xcd, 0x80, 0, 0, 2}, url...)
	return append(reply, 0), nil
}
