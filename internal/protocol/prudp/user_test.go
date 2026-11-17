package prudp

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

// This request has no player identifiers or secrets: it is the constant stock
// TDU public-URL probe observed during startup, checksum included.
const publicURLProbe = "71710500000000000000cd8000000236"

func TestConnectionlessUSERIsOptInAndNeverAuthenticatesRPC(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			seen := make(chan netip.AddrPort, 1)
			s, h := startTestServer(t, func(o *Options) {
				o.ChecksumSeed = 0xbe
				if enabled {
					o.UserHandler = func(_ context.Context, source netip.AddrPort, p Packet) ([]byte, error) {
						seen <- source
						if p.Source != 0x71 || p.Destination != 0x71 || p.Type != USER || !bytes.Equal(p.Payload, []byte{0xcd, 0x80, 0, 0, 2}) {
							return nil, errors.New("unexpected USER request")
						}
						return (Packet{Source: p.Destination, Destination: p.Source, Type: USER, Payload: []byte("bounded observed-endpoint reply")}).Marshal(0)
					}
				}
			})
			conn, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(s.Addr()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			wire, _ := hex.DecodeString(publicURLProbe)
			if enabled {
				bad := bytes.Clone(wire)
				bad[len(bad)-1] ^= 1
				if _, err = conn.Write(bad); err != nil {
					t.Fatal(err)
				}
				conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
				junk := make([]byte, 512)
				_, err = conn.Read(junk)
				if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
					t.Fatal("bad USER checksum was accepted", err)
				}
			}
			if _, err = conn.Write(wire); err != nil {
				t.Fatal(err)
			}
			timeout := 100 * time.Millisecond
			if enabled {
				timeout = time.Second
			}
			conn.SetReadDeadline(time.Now().Add(timeout))
			buffer := make([]byte, 512)
			n, err := conn.Read(buffer)
			if enabled {
				if err != nil {
					t.Fatal(err)
				}
				reply, used, err := ParseFrame(buffer[:n], 0)
				if err != nil || used != n || reply.Type != USER || reply.Source != 0x71 || !bytes.Equal(reply.Payload, []byte("bounded observed-endpoint reply")) {
					t.Fatalf("USER reply %+v %v", reply, err)
				}
				if source := <-seen; source != conn.LocalAddr().(*net.UDPAddr).AddrPort() {
					t.Fatal("handler source is not observed endpoint")
				}
			} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
				t.Fatalf("USER replied without opt-in: %x %v", buffer[:n], err)
			}
			if h.connectCount.Load() != 0 {
				t.Fatal("USER request authenticated a peer")
			}
			// Ordinary RPC traffic continues through the unchanged SYN/cookie path.
			syn, err := (Packet{Source: 0x31, Destination: 0x31, Type: SYN, Flags: FlagNeedsAck}).Marshal(0xbe)
			if err != nil {
				t.Fatal(err)
			}
			conn.Write(syn)
			conn.SetReadDeadline(time.Now().Add(time.Second))
			n, err = conn.Read(buffer)
			if err != nil {
				t.Fatal(err)
			}
			reply, _, err := ParseFrame(buffer[:n], 0xbe)
			if err != nil || reply.Type != SYN || reply.Flags&FlagAck == 0 {
				t.Fatal("USER altered ordinary SYN handling", err)
			}
		})
	}
}
