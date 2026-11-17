package tdu

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestRetailPublicEndpointProbeOnSecureSocket(t *testing.T) {
	cfg := localConfig(t)
	addresses := startAdapter(t, cfg)
	// The engine socket is separate from the ticket-authenticated backend socket.
	engine, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(addresses.Secure))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	local := engine.LocalAddr().(*net.UDPAddr).AddrPort()
	// Constant observed stock USER packet: stream71, opcode2, zero checksum key.
	request, err := hex.DecodeString("71710500000000000000cd8000000236")
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte{0xcd, 0x80, 0, 0, 2}, []byte(fmt.Sprintf("udp:/address=%s;port=%d\x00", local.Addr(), local.Port()))...)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err = engine.Write(request); err != nil {
			t.Fatal(err)
		}
		engine.SetReadDeadline(time.Now().Add(time.Second))
		reply := make([]byte, 512)
		n, err := engine.Read(reply)
		if err != nil || !bytes.Equal(reply[:n], want) {
			t.Fatalf("raw endpoint discovery reply %x: %v", reply[:n], err)
		}
	}
	// Discovery does not replace normal registered account proof or bootstrap RPC.
	_, account := testAccount(t, addresses, "natdriver", 0x91919000)
	if roster := account.rpc(20, 10, []byte{0, 0}); !bytes.Equal(roster.Payload, []byte{0, 0, 0, 0}) {
		t.Fatal("actual empty friend roster has the wrong startup schema")
	}
	if invalid := account.exchange(20, 10, []byte{0, 2}); invalid.Success {
		t.Fatal("invalid roster bool accepted")
	}
	if mutation := account.exchange(20, 1, nil); mutation.Success {
		t.Fatal("unsupported friendship mutation acknowledged")
	}
	if bootstrap := account.rpc(104, 1, nil); len(bootstrap.Payload) != 4 {
		t.Fatal("bootstrap RPC changed")
	}
}
