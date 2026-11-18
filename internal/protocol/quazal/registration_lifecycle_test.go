package quazal

import (
	"context"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func TestFreshRegistrationRetiresOnlyTheSameNamedAccount(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "opt_out", true: "single_registration"}[enabled], func(t *testing.T) {
			f := newFixture(t)
			f.secure.options.SingleRegistration = enabled
			key := quazalcrypto.Key{1}
			account, err := f.store.Create("Lifecycle", key)
			if err != nil {
				t.Fatal(err)
			}
			key = quazalcrypto.ExtendInitial(key, account.PID)
			pid, ticket := startAuth(t, f, authPeer(1), account.Name, key)
			old, fresh := securePeer(1), securePeer(2)
			startSecure(t, f, old, pid, ticket, 11)
			registerPeer(t, f, old)
			var disconnected []prudp.Peer
			f.secure.options.GameDisconnect = func(peer prudp.Peer, gotPID uint32) {
				if gotPID != pid {
					t.Fatal("retired another account")
				}
				disconnected = append(disconnected, peer)
			}
			f.secure.outbound = []prudp.OutboundMessage{{Peer: old, Payload: []byte{1}}}
			startSecure(t, f, fresh, pid, ticket, 12)
			if f.secure.peers[peerKey(old)] == nil || len(disconnected) != 0 {
				t.Fatal("CONNECT alone retired active registration")
			}
			// Invalid registration cannot evict the valid old connection.
			if response := rpc(t, f.secure, fresh, 11, 1, 9, []byte{0}); response.Success {
				t.Fatal("invalid registration accepted")
			}
			if len(disconnected) != 0 {
				t.Fatal("malformed registration retired active connection")
			}
			if enabled {
				queued := f.secure.outbound
				f.secure.outbound = make([]prudp.OutboundMessage, 128)
				args := arguments(func(w *writer) { w.u32(0) })
				if rpc(t, f.secure, fresh, 11, 1, 8, args).Success || len(disconnected) != 0 || f.secure.peers[peerKey(old)] == nil {
					t.Fatal("full retirement queue partially replaced the registration")
				}
				f.secure.outbound = queued
			}
			registerPeer(t, f, fresh)
			if enabled {
				if len(disconnected) != 1 || disconnected[0] != old || f.secure.peers[peerKey(old)] != nil {
					t.Fatal("old registration or pending instructions survived replacement")
				}
				out := f.secure.DrainOutbound()
				if len(out) != 1 || !out[0].Retire || out[0].Peer != old || len(out[0].Payload) != 0 {
					t.Fatal("old transport was not retired or a queued probe survived")
				}
				wire, err := EncodeRequest(Request{Protocol: 11, Method: 1, CallID: 10})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.secure.Handle(context.Background(), old, wire); err == nil {
					t.Fatal("retired peer replayed cached registration")
				}
				f.secure.Disconnect(old)
				if len(disconnected) != 1 || f.secure.peers[peerKey(fresh)] == nil {
					t.Fatal("late old disconnect affected fresh registration")
				}
			} else if len(disconnected) != 0 || f.secure.peers[peerKey(old)] == nil {
				t.Fatal("opt-out no longer supports concurrent registration")
			}
		})
	}
}

func TestSingleRegistrationKeepsOtherAccountsAndGuests(t *testing.T) {
	f := newFixture(t)
	f.secure.options.SingleRegistration = true
	for i, name := range []string{"First", "Second", guestName, guestName} {
		key := quazalcrypto.Key{byte(i + 1)}
		if name == guestName {
			key = f.secure.guestKey
		} else {
			account, err := f.store.Create(name, key)
			if err != nil {
				t.Fatal(err)
			}
			key = quazalcrypto.ExtendInitial(key, account.PID)
		}
		pid, ticket := startAuth(t, f, authPeer(uint64(i+1)), name, key)
		peer := securePeer(uint64(i + 1))
		startSecure(t, f, peer, pid, ticket, uint32(100+i))
		registerPeer(t, f, peer)
	}
	if len(f.secure.peers) != 4 {
		t.Fatal("another principal or a shared guest was retired")
	}
}
