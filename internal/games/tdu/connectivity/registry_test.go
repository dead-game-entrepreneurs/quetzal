package connectivity

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/sessionmaster"
	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

const callerPID, targetPID uint32 = 2001, 2002

type fixture struct {
	r              *Registry
	caller, target prudp.Peer
	member         sessionmaster.Membership
	live           bool
	now            time.Time
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{live: true, now: time.Unix(1800000000, 0)}
	f.caller = prudp.Peer{ID: 1, LocalPort: 30201, Addr: netip.MustParseAddrPort("198.51.100.1:45001")}
	f.target = prudp.Peer{ID: 2, LocalPort: 30201, Addr: netip.MustParseAddrPort("198.51.100.2:45002")}
	f.member = sessionmaster.Membership{SessionID: 7, CallerOwner: sessionmaster.Owner{Generation: 1, Remote: netip.MustParseAddrPort("198.51.100.1:39001")}, TargetOwner: sessionmaster.Owner{Generation: 2, Remote: netip.MustParseAddrPort("198.51.100.2:39002")}, TargetHost: &sessionmaster.FreeRide{Reference: 0xc6336402, Connection: [4]uint32{45122, 0x0a000002, 8122, 22}}}
	var err error
	f.r, err = New(Options{Now: func() time.Time { return f.now }, Membership: func(caller, target uint32) (sessionmaster.Membership, bool) {
		return f.member, f.live && caller == 11 && target == 22
	}})
	if err != nil {
		t.Fatal(err)
	}
	register(f.r, f.caller, callerPID, 11)
	register(f.r, f.target, targetPID, 22)
	return f
}
func register(r *Registry, p prudp.Peer, pid, cid uint32) {
	r.Registered(p, pid, station(p.Addr, pid, cid))
}
func lookup(t *testing.T, f *fixture) string {
	t.Helper()
	u, e := f.r.ConnectionURLs(f.caller, callerPID, 0, targetPID)
	if e != nil || len(u) != 1 {
		t.Fatalf("lookup %v %v", u, e)
	}
	return u[0]
}
func TestNativePublishedHostEndpointAndSelectors(t *testing.T) {
	f := setup(t)
	url := lookup(t, f)
	if !strings.HasSuffix(url, ";RVCID=22") {
		t.Fatal("native NAT result lost rendezvous connection selector", url)
	}
	if !strings.Contains(url, "address=198.51.100.2;port=45122;") || strings.Contains(url, "port=45002;") {
		t.Fatal("lookup used backend RMC port", url)
	}
	if u, e := f.r.ConnectionURLs(f.caller, callerPID, 22, 0); e != nil || len(u) != 1 || u[0] != url {
		t.Fatal("CID selector", u, e)
	}
	for _, selectors := range [][2]uint32{{0, 0}, {21, targetPID}, {22, callerPID}, {0, 3000}} {
		if _, e := f.r.ConnectionURLs(f.caller, callerPID, selectors[0], selectors[1]); e == nil {
			t.Fatal("invalid selectors", selectors)
		}
	}
	spoof := f.caller
	spoof.ID++
	if _, e := f.r.ConnectionURLs(spoof, callerPID, 0, targetPID); e == nil {
		t.Fatal("wrong registered caller")
	}
}
func TestProbeUsesObservedNativeSourceAndLiveRegisteredRecipient(t *testing.T) {
	f := setup(t)
	url := lookup(t, f)
	targets, e := f.r.Probe(f.caller, callerPID, []string{url})
	if e != nil || len(targets) != 0 {
		t.Fatal("must suppress default backend URL instruction", targets, e)
	}
	instructions := f.r.ProbeInstructions()
	if len(instructions) != 1 || instructions[0].Peer != f.target || instructions[0].URL != "udp:/address=198.51.100.1;port=39001;RVCID=11" {
		t.Fatal("native probe instruction", instructions)
	}
	if len(f.r.ProbeInstructions()) != 0 {
		t.Fatal("instruction repeated")
	}
	// Native SDK target URLs may omit optional CID/stream metadata; endpoint and
	// PID must still exactly identify the current published host.
	minimal := "prudp:/address=198.51.100.2;port=45122;PID=2002"
	if _, e = f.r.Probe(f.caller, callerPID, []string{minimal}); e != nil {
		t.Fatal(e)
	}
	if len(f.r.ProbeInstructions()) != 1 {
		t.Fatal("minimal native URL rejected")
	}
}
func TestProbeRejectsArbitraryTargetsAtomically(t *testing.T) {
	f := setup(t)
	good := lookup(t, f)
	for _, bad := range []string{
		strings.Replace(good, "198.51.100.2", "203.0.113.99", 1), strings.Replace(good, "port=45122", "port=9", 1), strings.Replace(good, "PID=2002", "PID=2003", 1), good + ";CID=99", strings.Replace(good, "CID=22", "CID=99", 1), good + ";arbitrary=1", good + ";RVCID=99",
	} {
		if _, e := f.r.Probe(f.caller, callerPID, []string{good, bad}); e == nil {
			t.Fatal("accepted bad target", bad)
		}
		if len(f.r.ProbeInstructions()) != 0 {
			t.Fatal("partial request queued")
		}
	}
	if _, e := f.r.Probe(f.caller, callerPID, []string{good, good}); e == nil {
		t.Fatal("duplicate targets")
	}
}
func TestPendingProbeRevalidatesOwnershipAndRegistration(t *testing.T) {
	changes := []struct {
		name   string
		change func(*fixture)
	}{
		{"native replacement", func(f *fixture) { f.member.TargetOwner.Generation++ }},
		{"caller replacement", func(f *fixture) { f.member.CallerOwner.Generation++ }},
		{"left session", func(f *fixture) { f.live = false }},
		{"expired", func(f *fixture) { f.now = f.now.Add(10 * time.Second) }},
		{"peer disconnect", func(f *fixture) { f.r.Disconnect(f.target, targetPID) }},
		{"registration replacement", func(f *fixture) { p := f.target; p.ID++; register(f.r, p, targetPID, 23) }},
		{"endpoint changed", func(f *fixture) { f.member.TargetHost.Connection[0]++ }},
	}
	for _, test := range changes {
		t.Run(test.name, func(t *testing.T) {
			f := setup(t)
			if _, e := f.r.Probe(f.caller, callerPID, []string{lookup(t, f)}); e != nil {
				t.Fatal(e)
			}
			test.change(f)
			if len(f.r.ProbeInstructions()) != 0 {
				t.Fatal("stale probe delivered")
			}
		})
	}
	f := setup(t)
	old := f.target
	f.target.ID++
	register(f.r, f.target, targetPID, 23)
	f.r.Disconnect(old, targetPID)
	if _, err := f.r.ConnectionURLs(f.caller, callerPID, 0, targetPID); err == nil {
		t.Fatal("old native CID authorized replacement registration")
	}
	f.member.TargetHost.Connection[3] = 23
	f.r.options.Membership = func(caller, target uint32) (sessionmaster.Membership, bool) {
		return f.member, f.live && caller == 11 && target == 23
	}
	if !strings.Contains(lookup(t, f), "CID=23;") {
		t.Fatal("old disconnect erased replacement")
	}
}
func TestMembershipAndEndpointCompatibilityAreRequired(t *testing.T) {
	changes := []func(*fixture){func(f *fixture) { f.member.TargetHost = nil }, func(f *fixture) { f.member.SessionID = 0 }, func(f *fixture) { f.member.TargetOwner.Generation = 0 }, func(f *fixture) { f.member.TargetHost.Reference = 0 }, func(f *fixture) { f.member.TargetHost.Connection[0] = 0 }, func(f *fixture) { f.member.TargetHost.Connection[3]++ }, func(f *fixture) { f.member.TargetOwner.Remote = netip.MustParseAddrPort("203.0.113.9:39002") }, func(f *fixture) { f.r.Disconnect(f.target, targetPID) }}
	for i, change := range changes {
		f := setup(t)
		change(f)
		if _, e := f.r.ConnectionURLs(f.caller, callerPID, 0, targetPID); e == nil {
			t.Fatalf("accepted unavailable/incompatible membership %d", i)
		}
	}
	f := setup(t)
	f.target.Addr = netip.MustParseAddrPort("198.51.100.1:45002")
	register(f.r, f.target, targetPID, 22)
	f.member.TargetOwner.Remote = netip.MustParseAddrPort("198.51.100.1:39002")
	f.member.TargetHost.Reference = 0xc6336401
	if !strings.Contains(lookup(t, f), "address=10.0.0.2;port=8122;") {
		t.Fatal("same public address did not choose published LAN endpoint")
	}
}
func TestTDUConnectionDataMethodTwoWire(t *testing.T) {
	f := setup(t)
	args := binary.LittleEndian.AppendUint32(nil, 0)
	args = binary.LittleEndian.AppendUint32(args, targetPID)
	request := quazal.Request{Protocol: 11, Method: 2, Args: args}
	wire, e := f.r.HandleRPC(f.caller, callerPID, request)
	if e != nil {
		t.Fatal(e)
	}
	if len(wire) < 11 || wire[0] != 1 || binary.LittleEndian.Uint32(wire[1:]) != 1 {
		t.Fatalf("bool/list prefix %x", wire)
	}
	n := int(binary.LittleEndian.Uint16(wire[5:]))
	if n < 1 || len(wire) != 7+n+4 || wire[7+n-1] != 0 {
		t.Fatalf("URL bounds %x", wire)
	}
	if string(wire[7:7+n-1]) != lookup(t, f) || binary.LittleEndian.Uint32(wire[7+n:]) != 22 {
		t.Fatal("ConnectionData URL/CID row")
	}
	request.Args = append(args, 0)
	if _, e = f.r.HandleRPC(f.caller, callerPID, request); e == nil {
		t.Fatal("extra lookup argument")
	}
	request.Args = args
	request.Method = 3
	if _, e = f.r.HandleRPC(f.caller, callerPID, request); e == nil {
		t.Fatal("intercepted shared method 3")
	}
}

func TestBrowserRVCIDDiscoveryBeforeMembership(t *testing.T) {
	f := setup(t)
	f.r.options.Membership = func(uint32, uint32) (sessionmaster.Membership, bool) { return sessionmaster.Membership{}, false }
	f.r.options.Discovery = func(pid, cid uint32) (sessionmaster.Membership, bool) {
		return f.member, f.live && pid == callerPID && cid == 22
	}
	url := "udp:/address=198.51.100.2;port=45122;RVCID=22"
	if _, err := f.r.Probe(f.caller, callerPID, []string{url}); err != nil {
		t.Fatal(err)
	}
	if got := f.r.ProbeInstructions(); len(got) != 1 || got[0].Peer != f.target {
		t.Fatal("prejoin discovery did not authorize native introduction", got)
	}
	if _, err := f.r.Probe(f.caller, callerPID, []string{url}); err != nil {
		t.Fatal(err)
	}
	f.live = false
	if got := f.r.ProbeInstructions(); len(got) != 0 {
		t.Fatal("revoked discovery retained queued introduction")
	}
	if _, err := f.r.Probe(f.caller, callerPID, []string{url}); err == nil {
		t.Fatal("expired browser discovery was admitted")
	}
	f.live = true
	for _, bad := range []string{strings.Replace(url, "RVCID=22", "RVCID=2002", 1), url + ";PID=2001", url + ";CID=11", strings.Replace(url, "port=45122", "port=9", 1)} {
		if _, err := f.r.Probe(f.caller, callerPID, []string{bad}); err == nil {
			t.Fatal("bad discovery selector/endpoint admitted", bad)
		}
	}
}
