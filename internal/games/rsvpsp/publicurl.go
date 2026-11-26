package rsvpsp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
)

var errProbe = errors.New("rsvpsp: unsupported public-URL probe")

// probeRequest is the PRUDP USER payload (stream 7, checksum seed 0) the game
// socket sends to the secure port before hosting, every 250 ms for ten tries:
// the title transport adapter's marker 0x00001234, then operation 2 (public
// URL). Without a reply, match creation fails and the session is closed.
var probeRequest = []byte{0x34, 0x12, 0, 0, 2}

// publicURL answers that probe the way TDU's identical SDK expects (see
// docs/games/tdu/nat-probe.md): a raw datagram, not a PRUDP frame, holding the
// marker, operation 2 and the observed source as a NUL-terminated StationURL.
// The game's receive loop 0x089543a8 matches the first four bytes against
// 0x1234 and hands the rest to the Quazal transport. Only the sender's own
// observed address is reflected; this grants no account or peer authority.
func publicURL(ctx context.Context, source netip.AddrPort, packet prudp.Packet) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	addr := source.Addr()
	if !source.IsValid() || !addr.Is4() || source.Port() == 0 || addr.IsUnspecified() || addr.IsMulticast() || addr == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
		return nil, errProbe
	}
	if packet.Type != prudp.USER || packet.Source != 0x71 || packet.Destination != 0x71 || packet.Flags != 0 || packet.Session != 0 || packet.Signature != 0 || packet.Sequence != 0 || !bytes.Equal(packet.Payload, probeRequest) {
		return nil, errProbe
	}
	reply := append(bytes.Clone(probeRequest), fmt.Sprintf("udp:/address=%s;port=%d", addr, source.Port())...)
	return append(reply, 0), nil
}
