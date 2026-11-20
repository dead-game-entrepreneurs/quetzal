package quazal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func TestNATProbeInitiationAndCorrelatedServerReply(t *testing.T) {
	f := newFixture(t)
	for i, name := range []string{"Prober", "Hoster"} {
		initial := quazalcrypto.InitialKey([]byte(name))
		a, err := f.store.Create(name, initial)
		if err != nil {
			t.Fatal(err)
		}
		pid, ticket := startAuth(t, f, authPeer(uint64(i+1)), name, quazalcrypto.ExtendInitial(initial, a.PID))
		startSecure(t, f, securePeer(uint64(i+11)), pid, ticket, uint32(i+1))
		registerPeer(t, f, securePeer(uint64(i+11)))
	}
	caller, target := securePeer(11), securePeer(12)
	f.secure.options.GameProbe = func(peer prudp.Peer, pid uint32, urls []string) ([]prudp.Peer, error) {
		if peer != caller || pid != 1000 || len(urls) != 1 || urls[0] != "published-host" {
			t.Fatal("probe callback identity/args")
		}
		return []prudp.Peer{target}, nil
	}
	args := arguments(func(w *writer) { w.u32(1); w.str("published-host") })
	f.secure.options.GameConnectionURLs = func(peer prudp.Peer, pid, cid, targetPID uint32) ([]string, error) {
		if peer != caller || pid != 1000 || cid != 0 || targetPID != 1001 {
			t.Fatal("connection lookup selectors/proof")
		}
		return []string{"published-host"}, nil
	}
	lookupArgs := arguments(func(w *writer) { w.u32(0); w.u32(1001) })
	lookup := rpc(t, f.secure, caller, 11, 3, 30, lookupArgs)
	lr := reader{data: lookup.Payload}
	if !lookup.Success || string(lr.take(1)) != "\x01" || lr.u32() != 1 || lr.str(512) != "published-host" || !lr.done() {
		t.Fatal("connection lookup bool/list reply shape")
	}
	if rpc(t, f.secure, caller, 11, 3, 31, append(lookupArgs, 0)).Success {
		t.Fatal("connection lookup trailing data")
	}
	got := rpc(t, f.secure, caller, 3, 1, 40, args)
	if !got.Success || len(got.Payload) != 0 {
		t.Fatal("probe initiation reply is not empty success")
	}
	out := f.secure.DrainOutbound()
	if len(out) != 1 || out[0].Peer != target {
		t.Fatal("wrong probe recipient")
	}
	call, err := ParseRequest(out[0].Payload)
	if err != nil || call.Protocol != 3 || call.Method != 2 {
		t.Fatal("probe instruction shape", err)
	}
	r := reader{data: call.Args}
	url := r.str(512)
	if !r.done() || !strings.HasPrefix(url, "prudp:/") || !strings.Contains(url, "PID=1000;") || !strings.HasSuffix(url, ";RVCID=1") || !strings.Contains(url, "address=127.0.0.1;") {
		t.Fatal("probe did not use observed caller URL/RVCID")
	}
	// Repeated client calls use the RMC cache, without extra introductions.
	rpc(t, f.secure, caller, 3, 1, 40, args)
	if len(f.secure.DrainOutbound()) != 0 {
		t.Fatal("duplicate probe forwarded twice")
	}
	reply, _ := SuccessReply(call, nil)
	if _, err := f.secure.Handle(context.Background(), caller, reply); err != nil {
		t.Fatal(err)
	}
	if len(f.secure.peers[peerKey(target)].probes) != 1 {
		t.Fatal("another caller acknowledged target's RPC")
	}
	response, err := f.secure.Handle(context.Background(), target, reply)
	if err != nil || len(response) != 0 || len(f.secure.peers[peerKey(target)].probes) != 0 {
		t.Fatal("server call reply was not consumed", err)
	}
	if rpc(t, f.secure, caller, 3, 1, 41, append(args, 0)).Success {
		t.Fatal("probe trailing data accepted")
	}
	f.secure.peers[peerKey(caller)].guest = true
	if rpc(t, f.secure, caller, 11, 3, 32, lookupArgs).Success {
		t.Fatal("guest connection lookup")
	}
	if rpc(t, f.secure, caller, 3, 1, 42, args).Success {
		t.Fatal("guest initiated probes")
	}
	f.secure.peers[peerKey(caller)].guest = false
	f.secure.peers[peerKey(caller)].registered = false
	if rpc(t, f.secure, caller, 11, 3, 33, lookupArgs).Success {
		t.Fatal("unregistered connection lookup")
	}
	if rpc(t, f.secure, caller, 3, 1, 43, args).Success {
		t.Fatal("unregistered caller initiated probes")
	}
	f.secure.peers[peerKey(caller)].registered = true
	other := f.secure.peers[peerKey(target)]
	other.probes = map[uint32]time.Time{}
	for i := uint32(0); i < 32; i++ {
		other.probes[i] = f.now.Add(time.Second)
	}
	if rpc(t, f.secure, caller, 3, 1, 44, args).Success || len(f.secure.DrainOutbound()) != 0 {
		t.Fatal("pending probe cap ignored")
	}
	f.now = f.now.Add(2 * time.Second)
	if !rpc(t, f.secure, caller, 3, 1, 45, args).Success || len(f.secure.DrainOutbound()) != 1 {
		t.Fatal("expired probe capacity not reclaimed")
	}
	virtual := "prudp:/address=127.0.0.1;port=30200;stream=3;sid=15;PID=1000;CID=1;RVCID=1;type=2"
	pending := []ProbeInstruction{{Peer: target, URL: virtual}}
	f.secure.options.GameProbeInstructions = func() []ProbeInstruction { out := pending; pending = nil; return out }
	out = f.secure.DrainOutbound()
	if len(out) != 1 || out[0].Peer != target {
		t.Fatal("async title instruction not sent")
	}
	request, err := ParseRequest(out[0].Payload)
	if err != nil || request.Protocol != 3 || request.Method != 2 {
		t.Fatal("async instruction envelope", err)
	}
	r = reader{data: request.Args}
	if r.str(512) != virtual || !r.done() {
		t.Fatal("async virtual URL changed")
	}
	reply, err = SuccessReply(request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if replies, err := f.secure.Handle(context.Background(), target, reply); err != nil || len(replies) != 0 {
		t.Fatal("async response correlation", err)
	}
	stale := target
	stale.ID += 100
	pending = []ProbeInstruction{{Peer: stale, URL: virtual}}
	if len(f.secure.DrainOutbound()) != 0 {
		t.Fatal("async instruction used stale peer")
	}
}

func TestGameServerCallsReachOnlyRegisteredNamedPeers(t *testing.T) {
	f := newFixture(t)
	initial := quazalcrypto.InitialKey([]byte("Notified"))
	a, err := f.store.Create("Notified", initial)
	if err != nil {
		t.Fatal(err)
	}
	pid, ticket := startAuth(t, f, authPeer(1), "Notified", quazalcrypto.ExtendInitial(initial, a.PID))
	startSecure(t, f, securePeer(11), pid, ticket, 1)
	registerPeer(t, f, securePeer(11))
	unknown := securePeer(99)
	f.secure.options.GameServerCalls = func() []ServerCall {
		return []ServerCall{
			{Peer: securePeer(11), Protocol: 14, Method: 1, Args: []byte{1, 2, 3}},
			{Peer: unknown, Protocol: 14, Method: 1},
		}
	}
	out := f.secure.DrainOutbound()
	if len(out) != 1 || out[0].Peer != securePeer(11) {
		t.Fatalf("outbound %+v", out)
	}
	r, err := ParseRequest(out[0].Payload)
	if err != nil || r.Protocol != 14 || r.Method != 1 || r.CallID&0x80000000 == 0 || string(r.Args) != "\x01\x02\x03" {
		t.Fatalf("server call %+v %v", r, err)
	}
	// The client's reply is ignored rather than answered or treated as an error.
	reply, _ := SuccessReply(r, nil)
	if got, err := f.secure.Handle(context.Background(), securePeer(11), reply); err != nil || got != nil {
		t.Fatalf("reply handling %v %v", got, err)
	}
	// Auth role never sends title calls.
	f.auth.options.GameServerCalls = f.secure.options.GameServerCalls
	if out := f.auth.DrainOutbound(); len(out) != 0 {
		t.Fatal("auth service sent title calls")
	}
}
