package prudp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"testing"
	"time"
)

func TestDelayedAcknowledgementSurvivesTitleRetryGrace(t *testing.T) {
	// Stock 25 to Life publishes max retransmission 20 and initial RTT 750ms.
	// The old shared default dropped a connection in about three seconds.
	for _, extended := range []bool{false, true} {
		name := "shared defaults"
		if extended {
			name = "title grace"
		}
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			h := &testHandler{messages: make(chan []byte, 1), disconnected: make(chan Peer, 1)}
			opts := Options{Bind: netip.MustParseAddrPort("127.0.0.1:0"), Handler: h, PlainPayload: true,
				Log: slog.New(slog.NewJSONHandler(&output, nil))}
			if extended {
				opts.MaxRetries, opts.RetryInterval = 20, 750*time.Millisecond
			}
			s, err := New(opts)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			now := time.Unix(1000, 0)
			peer := Peer{ID: 77, Addr: netip.MustParseAddrPort("127.0.0.1:1234"), LocalPort: 30121,
				SourceStream: 0x31, DestinationStream: 0x31, ClientSession: 7}
			key := peerKey{peer.Addr, 0x31, 0x31}
			c := &connection{peer: peer, last: now, expected: 2, next: 2,
				pending: map[uint16]*transmission{}, received: map[uint16]Packet{}}
			packet := s.base(c, DATA, FlagReliable|FlagNeedsAck|FlagSize, 1)
			packet.Payload = encodePlainPayload([]byte("reply"))
			wire, err := packet.Marshal(0)
			if err != nil {
				t.Fatal(err)
			}
			c.pending[1] = &transmission{wire: wire, kind: DATA, sent: now, started: now}
			s.peers[key] = c
			for elapsed := 250 * time.Millisecond; elapsed <= 4250*time.Millisecond; elapsed += 250 * time.Millisecond {
				s.expire(now.Add(elapsed))
			}
			if !extended {
				if s.peers[key] != nil {
					t.Fatal("control case did not reproduce the old short retry budget")
				}
				var event map[string]any
				if err := json.Unmarshal(output.Bytes(), &event); err != nil {
					t.Fatal(err)
				}
				if event["pending_age_ms"] != float64(3000) || event["retry_count"] != float64(5) || event["pending_sequence"] != float64(1) {
					t.Fatal("missing bounded retry diagnostics", event)
				}
				return
			}
			if s.peers[key] == nil {
				t.Fatal("brief missing acknowledgement removed the live title connection")
			}
			ack := Packet{Source: 0x31, Destination: 0x31, Session: 7, Type: DATA, Flags: FlagAck, Sequence: 1}
			s.receive(context.Background(), peer.Addr, ack, now.Add(4250*time.Millisecond))
			if len(c.pending) != 0 {
				t.Fatal("late acknowledgement did not retire retry")
			}
			s.expire(now.Add(30 * time.Second))
			request := Packet{Source: 0x31, Destination: 0x31, Session: 7, Type: DATA,
				Flags: FlagReliable | FlagNeedsAck, Sequence: 2, Payload: encodePlainPayload([]byte("next request"))}
			s.receive(context.Background(), peer.Addr, request, now.Add(30*time.Second))
			if s.peers[key] == nil {
				t.Fatal("connection did not recover after delayed acknowledgement")
			}
			select {
			case message := <-h.messages:
				if string(message) != "next request" {
					t.Fatal("recovered connection changed request")
				}
			default:
				t.Fatal("recovered connection did not deliver request")
			}
			// Permanent loss is still bounded; it must not leave a stale room.
			packet.Sequence = 2
			wire, err = packet.Marshal(0)
			if err != nil {
				t.Fatal(err)
			}
			c.pending[2] = &transmission{wire: wire, kind: DATA, sent: now.Add(30 * time.Second), started: now.Add(30 * time.Second)}
			for i := 1; i <= 21; i++ {
				s.expire(now.Add(30*time.Second + time.Duration(i)*750*time.Millisecond))
			}
			if s.peers[key] != nil {
				t.Fatal("permanent loss retained connection beyond retry budget")
			}
		})
	}
}
