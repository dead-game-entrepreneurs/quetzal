// Package connectivity authorizes Quazal-assisted introductions using live TDU
// native session membership or recent browser discovery. It neither forwards
// UDP nor turns native identifiers into account proof.
package connectivity

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/sessionmaster"
	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

var ErrAdmission = errors.New("TDU connectivity introduction is unavailable")

type Options struct {
	// Membership must return current published host/reserved or active caller
	// membership. It must not call this Registry or the Quazal Service: callbacks
	// run under their respective locks. Native CID claims remain unverified.
	Membership func(callerCID, targetCID uint32) (sessionmaster.Membership, bool)
	// Discovery permits a prejoin introduction only to a recently returned,
	// still-published browser result. It never creates room membership. The
	// caller owner must independently be tied to a fresh native presence claim.
	Discovery func(callerPID, targetCID uint32) (sessionmaster.Membership, bool)
	Now       func() time.Time
}
type registration struct {
	peer  prudp.Peer
	cid   uint32
	epoch uint64
}
type introduction struct {
	caller, target       uint32
	callerReg, targetReg registration
	membership           sessionmaster.Membership
	sourceURL, targetURL string
	expires              time.Time
}
type Registry struct {
	mu      sync.Mutex
	options Options
	peers   map[uint32]registration
	next    uint64
	pending []introduction
}

func New(o Options) (*Registry, error) {
	if o.Membership == nil {
		return nil, errors.New("TDU connectivity requires live native membership lookup")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Registry{options: o, peers: map[uint32]registration{}}, nil
}

// Registered is called only from Quazal's registered, ticket-proven named-account
// callback. A native CID/IPv4 match is an endpoint compatibility check, not
// cryptographic evidence that the native generation owns the account.
func (r *Registry) Registered(peer prudp.Peer, pid uint32, url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fields, err := stationFields(url)
	if err != nil || pid == 0 || pid == quazal.GuestPID || !usable(peer.Addr) {
		return
	}
	got, err := number(fields["PID"])
	if err != nil || got != pid {
		return
	}
	cid, err := number(fields["CID"])
	if err != nil || cid == 0 {
		return
	}
	if _, ok := r.peers[pid]; !ok && len(r.peers) >= 4096 {
		return
	}
	r.next++
	r.peers[pid] = registration{peer: peer, cid: cid, epoch: r.next}
}
func (r *Registry) Disconnect(peer prudp.Peer, pid uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.peers[pid]; ok && old.peer == peer {
		delete(r.peers, pid)
	}
}
func usable(a netip.AddrPort) bool {
	return a.IsValid() && a.Port() != 0 && a.Addr().Is4() && !a.Addr().IsUnspecified() && !a.Addr().IsMulticast() && a.Addr() != netip.MustParseAddr("255.255.255.255")
}
func address(v uint32) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return netip.AddrFrom4(b)
}
func station(a netip.AddrPort, pid, cid uint32) string {
	return fmt.Sprintf("prudp:/address=%s;port=%d;stream=3;sid=1;PID=%d;CID=%d;type=2", a.Addr(), a.Port(), pid, cid)
}
func (r *Registry) resolve(peer prudp.Peer, pid, targetPID uint32) (introduction, error) {
	caller, cok := r.peers[pid]
	target, tok := r.peers[targetPID]
	if !cok || !tok || caller.peer != peer || pid == targetPID {
		return introduction{}, ErrAdmission
	}
	m, ok := r.options.Membership(caller.cid, target.cid)
	if !ok && r.options.Discovery != nil {
		m, ok = r.options.Discovery(pid, target.cid)
	}
	if !ok || m.SessionID == 0 || m.TargetHost == nil || m.CallerOwner.Generation == 0 || m.TargetOwner.Generation == 0 || m.CallerOwner == m.TargetOwner || !usable(m.CallerOwner.Remote) || !usable(m.TargetOwner.Remote) {
		return introduction{}, ErrAdmission
	}
	if m.CallerOwner.Remote.Addr() != caller.peer.Addr.Addr() || m.TargetOwner.Remote.Addr() != target.peer.Addr.Addr() {
		return introduction{}, ErrAdmission
	}
	host := m.TargetHost
	public := netip.AddrPortFrom(address(host.Reference), uint16(host.Connection[0]))
	if !usable(public) || public.Addr() != m.TargetOwner.Remote.Addr() || host.Connection[3] != target.cid {
		return introduction{}, ErrAdmission
	}
	selected := public
	if public.Addr() == m.CallerOwner.Remote.Addr() {
		local := netip.AddrPortFrom(address(host.Connection[1]), uint16(host.Connection[2]))
		if usable(local) {
			selected = local
		}
	}
	return introduction{caller: pid, target: targetPID, callerReg: caller, targetReg: target, membership: m, sourceURL: fmt.Sprintf("udp:/address=%s;port=%d", m.CallerOwner.Remote.Addr(), m.CallerOwner.Remote.Port()), targetURL: station(selected, targetPID, target.cid) + ";RVCID=" + strconv.FormatUint(uint64(target.cid), 10), expires: r.options.Now().Add(10 * time.Second)}, nil
}

// ConnectionURLs supplies native host endpoints, never the observed backend RMC
// port. Zero CID selects by PID; nonzero CID must select the same registered peer.
func (r *Registry) ConnectionURLs(peer prudp.Peer, pid, cid, targetPID uint32) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if targetPID == 0 && cid != 0 {
		for id, entry := range r.peers {
			if entry.cid == cid {
				if targetPID != 0 {
					return nil, ErrAdmission
				}
				targetPID = id
			}
		}
	}
	if targetPID == 0 {
		return nil, ErrAdmission
	}
	if entry, ok := r.peers[targetPID]; !ok || cid != 0 && entry.cid != cid {
		return nil, ErrAdmission
	}
	intro, err := r.resolve(peer, pid, targetPID)
	if err != nil {
		return nil, err
	}
	return []string{intro.targetURL}, nil
}

// Probe queues custom 3/2 instructions and returns no default targets. The shared
// default instruction would use the backend RMC StationURL instead of the native
// engine endpoint. All requested URLs are checked against live session records.
func (r *Registry) Probe(peer prudp.Peer, pid uint32, urls []string) ([]prudp.Peer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(urls) > 32 || len(r.pending)+len(urls) > 128 {
		return nil, ErrAdmission
	}
	additions := make([]introduction, 0, len(urls))
	seen := map[uint32]bool{}
	for _, url := range urls {
		fields, err := stationFields(url)
		if err != nil {
			return nil, ErrAdmission
		}
		// Stock browser URLs contain RVCID and no PID/CID. Keep the
		// connection selector distinct from authenticated account identity.
		var targetPID uint32
		if value := fields["PID"]; value != "" {
			targetPID, err = number(value)
			if err != nil || targetPID == 0 {
				return nil, ErrAdmission
			}
		}
		for _, key := range []string{"CID", "RVCID"} {
			if value := fields[key]; value != "" {
				cid, e := number(value)
				if e != nil || cid == 0 {
					return nil, ErrAdmission
				}
				selected := uint32(0)
				for registeredPID, entry := range r.peers {
					if entry.cid != cid {
						continue
					}
					if selected != 0 {
						return nil, ErrAdmission
					}
					selected = registeredPID
				}
				if selected == 0 || targetPID != 0 && selected != targetPID {
					return nil, ErrAdmission
				}
				targetPID = selected
			}
		}
		if targetPID == 0 || seen[targetPID] {
			return nil, ErrAdmission
		}
		seen[targetPID] = true
		intro, err := r.resolve(peer, pid, targetPID)
		if err != nil {
			return nil, err
		}
		expected, _ := stationFields(intro.targetURL)
		for _, key := range []string{"address", "port"} {
			if fields[key] != expected[key] {
				return nil, ErrAdmission
			}
		}
		for _, key := range []string{"CID", "RVCID"} {
			if value := fields[key]; value != "" && value != strconv.FormatUint(uint64(intro.targetReg.cid), 10) {
				return nil, ErrAdmission
			}
		}
		additions = append(additions, intro)
	}
	r.pending = append(r.pending, additions...)
	return nil, nil
}
func (r *Registry) ProbeInstructions() []quazal.ProbeInstruction {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := r.pending
	r.pending = nil
	out := make([]quazal.ProbeInstruction, 0, len(pending))
	for _, old := range pending {
		if !r.options.Now().Before(old.expires) {
			continue
		}
		current, err := r.resolve(old.callerReg.peer, old.caller, old.target)
		if err != nil || current.callerReg != old.callerReg || current.targetReg != old.targetReg || current.membership.SessionID != old.membership.SessionID || current.membership.CallerOwner != old.membership.CallerOwner || current.membership.TargetOwner != old.membership.TargetOwner || current.sourceURL != old.sourceURL || current.targetURL != old.targetURL {
			continue
		}
		out = append(out, quazal.ProbeInstruction{Peer: old.targetReg.peer, URL: old.sourceURL + ";RVCID=" + strconv.FormatUint(uint64(old.callerReg.cid), 10)})
	}
	return out
}

// HandleRPC implements TDU SecureConnection 11/2, which differs from the shared
// 11/3 URL-list result: bool, u32 row count, then (StationURL, u32 CID) rows.
// Evidence: writers 1f61a0/1f4b20, readers 1f51a0/1f5880/1f5df0 and consumer1f8240.
func (r *Registry) HandleRPC(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
	if request.Protocol != 11 || request.Method != 2 || len(request.Args) != 8 {
		return nil, quazal.ErrRMC
	}
	cid, targetPID := binary.LittleEndian.Uint32(request.Args), binary.LittleEndian.Uint32(request.Args[4:])
	urls, err := r.ConnectionURLs(peer, pid, cid, targetPID)
	if err != nil {
		return nil, err
	}
	out := []byte{1}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(urls)))
	for _, url := range urls {
		fields, _ := stationFields(url)
		targetCID, _ := number(fields["CID"])
		out = binary.LittleEndian.AppendUint16(out, uint16(len(url)+1))
		out = append(out, url...)
		out = append(out, 0)
		out = binary.LittleEndian.AppendUint32(out, targetCID)
	}
	return out, nil
}
func number(s string) (uint32, error) { n, e := strconv.ParseUint(s, 10, 32); return uint32(n), e }
func stationFields(url string) (map[string]string, error) {
	scheme, body, ok := strings.Cut(url, ":/")
	if !ok || (scheme != "prudp" && scheme != "prudps" && scheme != "udp") || len(url) > 512 {
		return nil, ErrAdmission
	}
	fields := map[string]string{}
	for _, part := range strings.Split(body, ";") {
		key, value, ok := strings.Cut(part, "=")
		if !ok || value == "" || fields[key] != "" {
			return nil, ErrAdmission
		}
		switch key {
		case "address", "port", "stream", "sid", "PID", "CID", "RVCID", "type":
		default:
			return nil, ErrAdmission
		}
		fields[key] = value
	}
	return fields, nil
}
