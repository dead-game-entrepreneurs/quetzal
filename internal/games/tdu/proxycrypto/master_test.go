package proxycrypto

import (
	"bytes"
	"net/netip"
	"testing"
)

func TestMasterWireLayout(t *testing.T) {
	want := unhex(t, "0000757300001128")
	request := MasterRequest{Region: RegionUS, Revision: RetailRevision}
	if !bytes.Equal(request.MarshalBinary(), want) {
		t.Fatal("wrong US master request")
	}
	got, err := ParseMasterRequest(want)
	if err != nil || got != request {
		t.Fatalf("request %+v %v", got, err)
	}
	reply := MasterReply{Proxy: netip.MustParseAddr("198.51.100.7"), DirectoryIP: netip.MustParseAddr("203.0.113.77")}
	wire, err := reply.MarshalBinary()
	if err != nil || !bytes.Equal(wire, unhex(t, "c6336407cb00714d")) {
		t.Fatalf("reply %x %v", wire, err)
	}
	parsed, err := ParseMasterReply(append(wire, 9, 8))
	if err != nil || parsed != reply {
		t.Fatalf("reply %+v %v", parsed, err)
	}
	for n := 0; n < 8; n++ {
		if _, err := ParseMasterRequest(want[:n]); err == nil {
			t.Fatal("short request accepted")
		}
		if _, err := ParseMasterReply(wire[:n]); err == nil {
			t.Fatal("short reply accepted")
		}
	}
	if _, err := ParseMasterRequest(append(want, 0)); err == nil {
		t.Fatal("oversized request accepted")
	}
	if _, err := (MasterReply{Proxy: netip.MustParseAddr("::1")}).MarshalBinary(); err == nil {
		t.Fatal("IPv6 assignment accepted")
	}
	if _, err := (MasterReply{Proxy: netip.MustParseAddr("198.51.100.7"), DirectoryIP: netip.MustParseAddr("::1")}).MarshalBinary(); err == nil {
		t.Fatal("IPv6 directory accepted")
	}
	unsupported := MasterReply{Proxy: netip.IPv4Unspecified(), DirectoryIP: netip.IPv4Unspecified()}
	wire, err = unsupported.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = ParseMasterReply(wire)
	if err != nil || !parsed.DirectoryIP.IsUnspecified() {
		t.Fatal("zero directory address lost")
	}
}
