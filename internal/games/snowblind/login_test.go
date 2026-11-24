package snowblind

import (
	"bytes"
	"context"
	"crypto/rc4"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

// Synthetic UDP client: validates adapter wiring, not retail transport selection.
func TestBootstrapLoginOverUDP(t *testing.T) {
	for _, mode := range []string{DefaultConfig().PayloadMode, "rc4", "raw"} {
		t.Run(mode, func(t *testing.T) {
			c := DefaultConfig()
			c.Bind = netip.MustParseAddr("127.0.0.1")
			c.PublicIP = c.Bind
			c.StateDir = t.TempDir()
			c.AuthPort, c.SecurePort = 0, 0
			c.PayloadMode = mode
			var key quazalcrypto.Key
			key[0] = 42
			def, err := Definition(slog.New(slog.NewTextHandler(io.Discard, nil)), c, key)
			if err != nil {
				t.Fatal(err)
			}
			var sockets []*net.UDPConn
			for range 2 {
				conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { conn.Close() })
				sockets = append(sockets, conn)
			}
			services, err := def.NewServices(sockets, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, len(services))
			for _, service := range services {
				go func() { done <- service.Serve(ctx) }()
			}
			t.Cleanup(func() {
				cancel()
				for _, service := range services {
					service.Close()
				}
				for range services {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("shutdown timeout")
					}
				}
			})
			conn, err := net.DialUDP("udp4", nil, sockets[0].LocalAddr().(*net.UDPAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			send := func(p prudp.Packet) {
				t.Helper()
				p.Source, p.Destination = 0x3f, 0x31
				b, err := p.Marshal(0x02)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = conn.Write(b); err != nil {
					t.Fatal(err)
				}
			}
			read := func() prudp.Packet {
				t.Helper()
				b := make([]byte, 4096)
				n, err := conn.Read(b)
				if err != nil {
					t.Fatal(err)
				}
				p, used, err := prudp.ParseFrame(b[:n], 0x02)
				if err != nil || used != n {
					t.Fatal("bad reply", err)
				}
				return p
			}
			send(prudp.Packet{Type: prudp.SYN, Flags: prudp.FlagNeedsAck})
			syn := read()
			if syn.Type != prudp.SYN || syn.Flags&prudp.FlagAck == 0 {
				t.Fatal("SYN rejected")
			}
			send(prudp.Packet{Type: prudp.CONNECT, Flags: prudp.FlagReliable | prudp.FlagNeedsAck, Sequence: 1, Signature: syn.ConnectionSignature, ConnectionSignature: 123, Session: 0x27})
			if p := read(); p.Type != prudp.CONNECT || p.Flags&prudp.FlagAck == 0 {
				t.Fatal("CONNECT rejected")
			}
			wire, err := quazal.EncodeRequest(quazal.Request{Protocol: 10, Method: 1, CallID: 4, Args: encode(func(w *quazal.Writer) { w.String("Guest") })})
			if err != nil {
				t.Fatal(err)
			}

			if mode == "plain" {
				// Public application fields from the 2026-11-23 US retail request.
				// Excludes the captured connection cookie and all player/account data.
				retail, err := hex.DecodeString("110000008a04000000010000000600477565737400")
				if err != nil || !bytes.Equal(wire, retail) {
					t.Fatal("retail guest-login shape changed")
				}
			}
			crypt := func(b []byte) { cipher, _ := rc4.NewCipher([]byte("CD&ML")); cipher.XORKeyStream(b, b) }
			if mode != "raw" {
				wire = append([]byte{0}, wire...)
			}
			if mode == "rc4" {
				crypt(wire)
			}
			send(prudp.Packet{Type: prudp.DATA, Flags: prudp.FlagReliable | prudp.FlagNeedsAck | prudp.FlagSize, Sequence: 2, Signature: syn.ConnectionSignature, Session: 0x27, Payload: wire})
			for range 8 {
				p := read()
				if p.Flags&prudp.FlagAck != 0 {
					continue
				}
				if p.Type != prudp.DATA {
					t.Fatal("unexpected packet")
				}
				if p.Flags&prudp.FlagSize != 0 {
					t.Fatal("Snowblind native reader cannot consume an explicit DATA size")
				}
				b := p.Payload
				if mode == "rc4" {
					crypt(b)
				}
				if mode != "raw" {
					if len(b) == 0 || b[0] != 0 {
						t.Fatal("compression selector")
					}
					b = b[1:]
				}
				response, err := quazal.ParseResponse(b)
				if err != nil || !response.Success || response.CallID != 4 || response.Protocol != 10 {
					t.Fatal("login rejected", err)
				}
				r := quazal.NewReader(response.Payload)
				if r.U32() != quazal.Success || r.U32() != quazal.GuestPID {
					t.Fatal("wrong signup identity")
				}
				return
			}
			t.Fatal("login reply missing")
		})
	}
}
