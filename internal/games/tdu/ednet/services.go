package ednet

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"sort"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/nativeproxy"
)

// Options explicitly chooses logical service ports. They are not UDP listeners.
// No default stock directory port is claimed by this package.
type Options struct {
	PublicIP                    netip.Addr
	DirectoryPort, ResourcePort uint16
	// Registrations maps recovered action IDs to implemented logical ports. The
	// caller must actually install handlers for any additional advertised ports.
	Registrations map[uint16]uint16
	// Resources contains server-generated policy or privately supplied public bytes.
	// Only netconfig.xml, prize.xml and p2pkey are accepted.
	Resources map[string][]byte
}

type Services struct {
	opts     Options
	files    map[string]uint32
	contents map[uint32][]byte
}

func New(opts Options) (*Services, error) {
	if !opts.PublicIP.Is4() || opts.PublicIP.IsUnspecified() || opts.DirectoryPort == 0 {
		return nil, fmt.Errorf("edNet requires public IPv4 and a directory logical port")
	}
	if opts.ResourcePort == opts.DirectoryPort && opts.ResourcePort != 0 {
		return nil, fmt.Errorf("edNet directory and resource ports must differ")
	}
	s := &Services{opts: opts, files: make(map[string]uint32), contents: make(map[uint32][]byte)}
	s.opts.Registrations = make(map[uint16]uint16, len(opts.Registrations)+3)
	for action, port := range opts.Registrations {
		if port == 0 {
			return nil, fmt.Errorf("zero registered port")
		}
		s.opts.Registrations[action] = port
	}
	s.opts.Registrations[DirectoryRequest] = opts.DirectoryPort
	names := make([]string, 0, len(opts.Resources))
	for name, data := range opts.Resources {
		max := 4096
		switch name {
		case "netconfig.xml", "prize.xml":
		case "p2pkey":
			max = 16
		default:
			return nil, fmt.Errorf("unsupported public resource %q", name)
		}
		if len(data) == 0 || len(data) > max || (name == "p2pkey" && len(data) != 16) {
			return nil, fmt.Errorf("invalid resource size for %q", name)
		}
		names = append(names, name)
	}
	if len(names) > 0 && opts.ResourcePort == 0 {
		return nil, fmt.Errorf("resources require a logical port")
	}
	sort.Strings(names)
	for i, name := range names {
		id := uint32(i + 1)
		s.files[name] = id
		s.contents[id] = append([]byte(nil), opts.Resources[name]...)
	}
	if len(names) > 0 {
		s.opts.Registrations[OpenRequest] = opts.ResourcePort
		s.opts.Registrations[GetRequest] = opts.ResourcePort
	}
	return s, nil
}

// Handlers returns local routing entries suitable for nativeproxy.Options.Services.
func (s *Services) Handlers() map[uint16]nativeproxy.DataHandler {
	m := map[uint16]nativeproxy.DataHandler{s.opts.DirectoryPort: s.directory}
	if len(s.files) > 0 {
		m[s.opts.ResourcePort] = s.resource
	}
	return m
}

func (s *Services) handle(packet []byte, action func([]byte) ([]byte, error)) ([]byte, error) {
	f, err := ParseFrame(packet)
	if err != nil {
		return nil, err
	}
	if f.Kind == Ack {
		return nil, nil
	}
	if f.Kind != Call && f.Kind != CallUnreliable {
		return nil, ErrUnsupported
	}
	reply, err := action(f.Action)
	if err != nil {
		return nil, err
	}
	// Native unreliable calls have no transaction and expect no response.
	if f.Kind == CallUnreliable || f.RequestID == 0 {
		return nil, nil
	}
	return (Frame{Kind: Reply, RequestID: f.RequestID, Action: reply}).MarshalBinary()
}

func (s *Services) directory(_ context.Context, _ nativeproxy.SessionInfo, _ netip.AddrPort, packet []byte) ([]byte, error) {
	return s.handle(packet, s.DirectoryAction)
}
func (s *Services) resource(_ context.Context, _ nativeproxy.SessionInfo, _ netip.AddrPort, packet []byte) ([]byte, error) {
	return s.handle(packet, s.ResourceAction)
}

// DirectoryAction preserves all five request slots; unsupported actions get a
// zero endpoint, matching the client's explicit service-unavailable path.
func (s *Services) DirectoryAction(b []byte) ([]byte, error) {
	if len(b) != 12 || binary.BigEndian.Uint16(b) != DirectoryRequest {
		return nil, fmt.Errorf("%w: directory action", ErrMalformed)
	}
	out := make([]byte, 42)
	binary.BigEndian.PutUint16(out, DirectoryReply)
	ip := s.opts.PublicIP.As4()
	for i := 0; i < 5; i++ {
		id := binary.BigEndian.Uint16(b[2+i*2:])
		row := out[2+i*8:]
		binary.BigEndian.PutUint16(row, id)
		if port := s.opts.Registrations[id]; port != 0 {
			copy(row[2:6], ip[:])
			binary.BigEndian.PutUint16(row[6:], port)
		}
	}
	return out, nil
}

// ResourceAction is a public read-only file operation. File IDs are stable for
// this immutable service instance, so duplicate requests produce identical bytes.
func (s *Services) ResourceAction(b []byte) ([]byte, error) {
	if len(b) < 2 {
		return nil, ErrMalformed
	}
	switch binary.BigEndian.Uint16(b) {
	case OpenRequest:
		// signature, facility byte, user u32, string u16+bytes, upload byte, size u32.
		if len(b) < 14 {
			return nil, ErrMalformed
		}
		n := int(binary.BigEndian.Uint16(b[7:]))
		if n < 1 || n > 255 || len(b) != 14+n {
			return nil, ErrMalformed
		}
		// Config callers select facility 2 through retail setter 0x15c190.
		if b[2] != 2 || binary.BigEndian.Uint32(b[3:]) != 0 || b[9+n] != 0 || binary.BigEndian.Uint32(b[10+n:]) != 0 {
			return nil, fmt.Errorf("%w: resource facility, user or upload", ErrUnsupported)
		}
		name := string(b[9 : 9+n])
		id, ok := s.files[name]
		out := make([]byte, 19)
		binary.BigEndian.PutUint16(out, OpenReply)
		if !ok {
			out[2] = 1
			return out, nil
		}
		binary.BigEndian.PutUint32(out[3:], id)
		binary.BigEndian.PutUint32(out[7:], uint32(len(s.contents[id])))
		binary.BigEndian.PutUint32(out[11:], 10)
		binary.BigEndian.PutUint32(out[15:], 1000)
		return out, nil
	case GetRequest:
		if len(b) != 10 {
			return nil, ErrMalformed
		}
		id := binary.BigEndian.Uint32(b[2:])
		offset := binary.BigEndian.Uint32(b[6:])
		data, ok := s.contents[id]
		out := make([]byte, 15)
		binary.BigEndian.PutUint16(out, GetReply)
		binary.BigEndian.PutUint32(out[3:], id)
		binary.BigEndian.PutUint32(out[7:], offset)
		if !ok || uint64(offset) > uint64(len(data)) {
			out[2] = 1
			return out, nil
		}
		chunk := data[int(offset):]
		if len(chunk) > 1024 {
			chunk = chunk[:1024]
		}
		binary.BigEndian.PutUint16(out[13:], uint16(len(chunk)))
		return append(out, chunk...), nil
	default:
		return nil, fmt.Errorf("%w: resource action %#04x", ErrUnsupported, binary.BigEndian.Uint16(b))
	}
}
