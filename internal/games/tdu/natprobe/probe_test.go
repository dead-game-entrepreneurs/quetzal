package natprobe

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
)

func request(t *testing.T) prudp.Packet {
	t.Helper()
	wire, _ := hex.DecodeString("71710500000000000000cd8000000236")
	packet, n, err := prudp.ParseFrame(wire, 0)
	if err != nil || n != len(wire) {
		t.Fatal("constant stock probe", err)
	}
	return packet
}
func TestPublicEndpointReplyUsesObservedNativeSocket(t *testing.T) {
	packet := request(t)
	source := netip.MustParseAddrPort("198.51.100.7:48122")
	reply, err := Handle(context.Background(), source, packet)
	want := append([]byte{0xcd, 0x80, 0, 0, 2}, []byte("udp:/address=198.51.100.7;port=48122\x00")...)
	if err != nil || !bytes.Equal(reply, want) {
		t.Fatalf("raw adapter reply %x: %v", reply, err)
	}
	if bytes.Contains(reply, []byte("PID=")) || bytes.Contains(reply, []byte("CID=")) {
		t.Fatal("NAT observation invented account identity")
	}
}
func TestMalformedAndForeignProbeTrafficIsRejected(t *testing.T) {
	source := netip.MustParseAddrPort("198.51.100.7:48122")
	for _, change := range []func(*prudp.Packet){
		func(p *prudp.Packet) { p.Type = prudp.DATA }, func(p *prudp.Packet) { p.Source = 0x31 }, func(p *prudp.Packet) { p.Destination = 0x72 }, func(p *prudp.Packet) { p.Flags = prudp.FlagAck }, func(p *prudp.Packet) { p.Signature = 1 }, func(p *prudp.Packet) { p.Session = 1 }, func(p *prudp.Packet) { p.Sequence = 1 }, func(p *prudp.Packet) { p.Payload = p.Payload[:4] }, func(p *prudp.Packet) { p.Payload[4] = 3 }, func(p *prudp.Packet) { p.Payload = append(p.Payload, 0) },
	} {
		packet := request(t)
		change(&packet)
		if reply, err := Handle(context.Background(), source, packet); !errors.Is(err, ErrProbe) || len(reply) != 0 {
			t.Fatal("unsupported packet produced a reply", err)
		}
	}
	for _, addr := range []netip.AddrPort{{}, netip.MustParseAddrPort("[::1]:8122"), netip.MustParseAddrPort("0.0.0.0:8122"), netip.MustParseAddrPort("198.51.100.7:0")} {
		if _, err := Handle(context.Background(), addr, request(t)); !errors.Is(err, ErrProbe) {
			t.Fatal("invalid source accepted", addr, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Handle(ctx, source, request(t)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestReplyMatchesRetailExecutedJobCompletionFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/native-reply.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Entries []struct {
			Name          string
			ReplyHex      string
			JobCompleted  bool
			DemuxAccepted int
		}
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, entry := range fixture.Entries {
		if entry.Name != "native-little" {
			continue
		}
		if !entry.JobCompleted || entry.DemuxAccepted != 1 {
			t.Fatal("fixture did not complete retail public-URL job")
		}
		want, err := hex.DecodeString(entry.ReplyHex)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Handle(context.Background(), netip.MustParseAddrPort("198.51.100.23:8122"), request(t))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("server reply diverges from retail-consumed fixture: %x %v", got, err)
		}
		return
	}
	t.Fatal("retail-consumed reply fixture missing")
}
