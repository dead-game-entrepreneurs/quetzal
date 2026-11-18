package prudp

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/netip"
	"testing"
	"time"
)

func TestTimeoutDiagnosticsDistinguishIdleFragmentsAndRetries(t *testing.T) {
	for _, reason := range []string{"idle timeout", "fragment timeout", "reliable retries exhausted"} {
		t.Run(reason, func(t *testing.T) {
			var output bytes.Buffer
			handler := &testHandler{disconnected: make(chan Peer, 1)}
			s, err := New(Options{Bind: netip.MustParseAddrPort("127.0.0.1:0"), Handler: handler,
				Log: slog.New(slog.NewJSONHandler(&output, nil))})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			now := time.Unix(1000, 0)
			peer := Peer{ID: 77, Addr: netip.MustParseAddrPort("127.0.0.1:1234"), LocalPort: 30121}
			key := peerKey{addr: peer.Addr, source: 0x31, destination: 0x31}
			c := &connection{peer: peer, last: now, pending: map[uint16]*transmission{}, connectRequest: []byte("never-log-private-handshake")}
			s.peers[key] = c
			switch reason {
			case "idle timeout":
				c.last = now.Add(-s.opts.IdleTimeout)
			case "fragment timeout":
				c.fragmentStarted = now.Add(-s.opts.HandshakeTimeout)
			default:
				c.pending[1] = &transmission{sent: now.Add(-s.opts.RetryInterval), retries: s.opts.MaxRetries}
			}
			s.expire(now)
			var event map[string]any
			if err := json.Unmarshal(output.Bytes(), &event); err != nil {
				t.Fatal(err)
			}
			if len(s.peers) != 0 || event["reason"] != reason || event["peer"] != float64(77) || event["local_port"] != float64(30121) {
				t.Fatal("missing correlated timeout diagnostics", event)
			}
			if bytes.Contains(output.Bytes(), c.connectRequest) {
				t.Fatal("logged private handshake")
			}
		})
	}
}
