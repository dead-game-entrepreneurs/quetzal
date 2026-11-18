package ednet

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/nativeproxy"
	"net/netip"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, e := hex.DecodeString(s)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestFrameGoldenAndMalformed(t *testing.T) {
	// Independently written field fixture: tag509a, transaction01020304,
	// timeout1000, nestedlength12, directorytagc7bc and fiveactionIDs.
	golden := mustHex(t, "509a01020304000003e8000cc7bc00010002000300040005")
	f, e := ParseFrame(golden)
	if e != nil {
		t.Fatal(e)
	}
	if f.Kind != Call || f.RequestID != 0x01020304 || f.Timeout != 1000 || len(f.Action) != 12 {
		t.Fatalf("%+v", f)
	}
	b, e := f.MarshalBinary()
	if e != nil || !bytes.Equal(b, golden) {
		t.Fatalf("%x %v", b, e)
	}
	for n := 0; n < len(golden); n++ {
		if _, e := ParseFrame(golden[:n]); e == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	for _, bad := range [][]byte{append(append([]byte{}, golden...), 0), mustHex(t, "509a01020304000003e8ffffc7bc"), mustHex(t, "08e70000000100"), mustHex(t, "509a00000001000003e8000100")} {
		if _, e := ParseFrame(bad); !errors.Is(e, ErrMalformed) {
			t.Fatalf("bad %x: %v", bad, e)
		}
	}
	for _, f := range []Frame{{Kind: Ack, RequestID: 9}, {Kind: CallUnreliable, Action: []byte{1, 2}}, {Kind: Reply, RequestID: 9}, {Kind: Reply, RequestID: 9, Action: []byte{3, 4}}} {
		b, e := f.MarshalBinary()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = ParseFrame(b); e != nil {
			t.Fatal(e)
		}
	}
}
func fixture(t *testing.T) *Services {
	t.Helper()
	s, e := New(Options{PublicIP: netip.MustParseAddr("192.0.2.7"), DirectoryPort: 9000, ResourcePort: 9001, Resources: map[string][]byte{"p2pkey": bytes.Repeat([]byte{0xab}, 16), "netconfig.xml": bytes.Repeat([]byte{'x'}, 2049)}})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestDirectoryOnlyAdvertisesRegisteredServices(t *testing.T) {
	s := fixture(t)
	request := mustHex(t, "c7bc3be20001d976c7bc0002")
	out, e := s.DirectoryAction(request)
	if e != nil {
		t.Fatal(e)
	}
	// Request order retained and missing endpoint bytes allzero.
	expected := mustHex(t, "ee873be2c000020723290001000000000000d976c00002072329c7bcc000020723280002000000000000")
	if !bytes.Equal(out, expected) {
		t.Fatalf("got %x want %x", out, expected)
	}
	for n := 0; n < len(request); n++ {
		if _, e := s.DirectoryAction(request[:n]); e == nil {
			t.Fatalf("accepted %d", n)
		}
	}
}
func open(name string, upload byte) []byte {
	b := make([]byte, 14+len(name))
	binary.BigEndian.PutUint16(b, OpenRequest)
	b[2] = 2
	binary.BigEndian.PutUint16(b[7:], uint16(len(name)))
	copy(b[9:], name)
	b[9+len(name)] = upload
	return b
}
func get(id, offset uint32) []byte {
	b := make([]byte, 10)
	binary.BigEndian.PutUint16(b, GetRequest)
	binary.BigEndian.PutUint32(b[2:], id)
	binary.BigEndian.PutUint32(b[6:], offset)
	return b
}
func TestReadOnlyResourcesAndRetries(t *testing.T) {
	s := fixture(t)
	req := open("netconfig.xml", 0)
	a, e := s.ResourceAction(req)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(a, mustHex(t, "b7150000000001000008010000000a000003e8")) {
		t.Fatalf("open %x", a)
	}
	again, e := s.ResourceAction(req)
	if e != nil || !bytes.Equal(a, again) {
		t.Fatal("duplicate changed")
	}
	var collected []byte
	for _, offset := range []uint32{0, 1024, 2048} {
		out, e := s.ResourceAction(get(1, offset))
		if e != nil {
			t.Fatal(e)
		}
		if out[2] != 0 || binary.BigEndian.Uint32(out[7:]) != offset || int(binary.BigEndian.Uint16(out[13:])) != len(out)-15 {
			t.Fatalf("get %x", out)
		}
		collected = append(collected, out[15:]...)
	}
	if !bytes.Equal(collected, bytes.Repeat([]byte{'x'}, 2049)) {
		t.Fatal("resource did not reconstruct")
	}
	for _, req := range [][]byte{open("../../etc/passwd", 0), open("unknown.xml", 0), get(999, 0), get(1, 2050)} {
		out, e := s.ResourceAction(req)
		if e != nil || out[2] == 0 {
			t.Fatalf("missing resource %x %v", out, e)
		}
	}
	if _, e := s.ResourceAction(open("netconfig.xml", 1)); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
	for n := 0; n < len(req); n++ {
		if _, e := s.ResourceAction(req[:n]); e == nil {
			t.Fatalf("accepted open truncation %d", n)
		}
	}
	for n := 0; n < 10; n++ {
		if _, e := s.ResourceAction(get(1, 0)[:n]); e == nil {
			t.Fatalf("accepted get truncation %d", n)
		}
	}
}
func TestProxyHandlerEnvelopeAndAcknowledgement(t *testing.T) {
	s := fixture(t)
	handler := s.Handlers()[9001]
	action := open("p2pkey", 0)
	request, _ := (Frame{Kind: Call, RequestID: 42, Timeout: 1000, Action: action}).MarshalBinary()
	reply, e := handler(context.Background(), nativeproxy.SessionInfo{}, netip.AddrPort{}, request)
	if e != nil {
		t.Fatal(e)
	}
	f, e := ParseFrame(reply)
	if e != nil || f.Kind != Reply || f.RequestID != 42 || binary.BigEndian.Uint16(f.Action) != OpenReply || f.Action[2] != 0 {
		t.Fatalf("reply %x %v", reply, e)
	}
	duplicate, e := handler(context.Background(), nativeproxy.SessionInfo{}, netip.AddrPort{}, request)
	if e != nil || !bytes.Equal(duplicate, reply) {
		t.Fatal("retry changed")
	}
	ack, _ := (Frame{Kind: Ack, RequestID: 42}).MarshalBinary()
	if out, e := handler(context.Background(), nativeproxy.SessionInfo{}, netip.AddrPort{}, ack); e != nil || len(out) != 0 {
		t.Fatalf("ack %x %v", out, e)
	}
	unsupported, _ := (Frame{Kind: Call, RequestID: 42, Action: []byte{0xff, 0xff}}).MarshalBinary()
	if _, e := handler(context.Background(), nativeproxy.SessionInfo{}, netip.AddrPort{}, unsupported); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
}
func TestConfigurationOwnershipAndLimits(t *testing.T) {
	data := bytes.Repeat([]byte{1}, 16)
	opts := Options{PublicIP: netip.MustParseAddr("192.0.2.1"), DirectoryPort: 1, ResourcePort: 2, Resources: map[string][]byte{"p2pkey": data}}
	s, e := New(opts)
	if e != nil {
		t.Fatal(e)
	}
	data[0] = 99
	out, _ := s.ResourceAction(get(1, 0))
	if out[15] != 1 {
		t.Fatal("resource aliased caller")
	}
	for _, o := range []Options{{}, {PublicIP: opts.PublicIP, DirectoryPort: 1, ResourcePort: 1}, {PublicIP: opts.PublicIP, DirectoryPort: 1, Resources: map[string][]byte{"p2pkey": data}}, {PublicIP: opts.PublicIP, DirectoryPort: 1, ResourcePort: 2, Resources: map[string][]byte{"p2pkey": {1}}}, {PublicIP: opts.PublicIP, DirectoryPort: 1, ResourcePort: 2, Resources: map[string][]byte{"secret": data}}} {
		if _, e := New(o); e == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
}

func TestResourceOpenScopeRemainsReadOnly(t *testing.T) {
	service := fixture(t)
	for _, facility := range []byte{0, 1, 3, 255} {
		request := open("netconfig.xml", 0)
		request[2] = facility
		if _, err := service.ResourceAction(request); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("facility %d: %v", facility, err)
		}
	}
	for _, user := range []uint32{1, 1000, 0xffffffff} {
		request := open("netconfig.xml", 0)
		binary.BigEndian.PutUint32(request[3:], user)
		if _, err := service.ResourceAction(request); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("user %d: %v", user, err)
		}
	}
	request := open("netconfig.xml", 0)
	binary.BigEndian.PutUint32(request[len(request)-4:], 16)
	if _, err := service.ResourceAction(request); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("download with upload size: %v", err)
	}
}
