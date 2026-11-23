package quazal

import (
	"net/netip"
	"strconv"
	"strings"
)

// StationURL parses a bounded PRUDP endpoint without granting endpoint or
// account authority. Callers must bind PID and address claims to live proof.
type StationURL struct {
	Address netip.AddrPort
	PID     uint32
	Fields  map[string]string
}

// WithRVCID preserves an explicit rendezvous identity, or derives it from the
// server-issued CID. Native URL probes group endpoints by RVCID, not CID.
// This changes URL metadata only; it grants no account or endpoint authority.
func WithRVCID(url string) string {
	var cid string
	for _, part := range strings.Split(url, ";") {
		if strings.HasPrefix(part, "RVCID=") {
			return url
		}
		if strings.HasPrefix(part, "CID=") {
			cid = strings.TrimPrefix(part, "CID=")
		}
	}
	if n, err := strconv.ParseUint(cid, 10, 32); err == nil && n != 0 {
		return url + ";RVCID=" + cid
	}
	return url
}

func ParseStationURL(url string) (StationURL, error) {
	scheme, body, ok := strings.Cut(url, ":/")
	if !ok || len(url) > 512 || (scheme != "prudp" && scheme != "prudps") {
		return StationURL{}, ErrRMC
	}
	fields := map[string]string{}
	for _, part := range strings.Split(body, ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok || k == "" || v == "" || fields[k] != "" {
			return StationURL{}, ErrRMC
		}
		fields[k] = v
	}
	ip, err := netip.ParseAddr(fields["address"])
	if err != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() {
		return StationURL{}, ErrRMC
	}
	port, err := strconv.ParseUint(fields["port"], 10, 16)
	if err != nil || port == 0 {
		return StationURL{}, ErrRMC
	}
	pid, err := strconv.ParseUint(fields["PID"], 10, 32)
	if err != nil {
		return StationURL{}, ErrRMC
	}
	return StationURL{Address: netip.AddrPortFrom(ip, uint16(port)), PID: uint32(pid), Fields: fields}, nil
}
