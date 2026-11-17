package proxycrypto

import (
	"encoding/binary"
	"net/netip"
)

// RetailRevision is parsed by native 0x465970 from the US GOLD build string.
const RetailRevision uint32 = 4392
const RegionUS uint32 = 0x7573 // ASCII "us", assembled by 0x377b80.

type MasterRequest struct {
	Region   uint32
	Revision uint32
}

func (r MasterRequest) MarshalBinary() []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint32(out[:4], r.Region)
	binary.BigEndian.PutUint32(out[4:], r.Revision)
	return out
}
func ParseMasterRequest(packet []byte) (MasterRequest, error) {
	if len(packet) != 8 {
		return MasterRequest{}, ErrPacket
	}
	return MasterRequest{Region: binary.BigEndian.Uint32(packet[:4]), Revision: binary.BigEndian.Uint32(packet[4:])}, nil
}

// MasterReply is the raw UDP 8124 assignment: proxy IPv4 then directory IPv4,
// both in network byte order. The client copies DirectoryIP at 0x154410 and
// installs it in the directory descriptor at 0x377ea4..0x377ec4. A zero
// directory address selects the OLD VERSION failure path.
type MasterReply struct {
	Proxy       netip.Addr
	DirectoryIP netip.Addr
}

func (r MasterReply) MarshalBinary() ([]byte, error) {
	if !r.Proxy.Is4() || !r.DirectoryIP.Is4() {
		return nil, ErrPacket
	}
	proxy, directory := r.Proxy.As4(), r.DirectoryIP.As4()
	out := make([]byte, 8)
	copy(out[:4], proxy[:])
	copy(out[4:], directory[:])
	return out, nil
}
func ParseMasterReply(packet []byte) (MasterReply, error) {
	if len(packet) < 8 {
		return MasterReply{}, ErrPacket
	}
	return MasterReply{Proxy: netip.AddrFrom4([4]byte(packet[:4])), DirectoryIP: netip.AddrFrom4([4]byte(packet[4:8]))}, nil
}
