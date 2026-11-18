package prudp

import (
	"testing"
	"time"
)

func TestApplicationRetiresOnlyTheOldTransportGeneration(t *testing.T) {
	registered := make(chan Peer, 1)
	retire := make(chan Peer, 1)
	s, h := startTestServer(t, func(o *Options) {
		handler := o.Handler.(*testHandler)
		handler.respond = func(message []byte) [][]byte { return [][]byte{message} }
		handler.push = func(peer Peer, message []byte) []OutboundMessage {
			switch string(message) {
			case "old registration":
				registered <- peer
			case "fresh registration":
				return []OutboundMessage{{Peer: <-retire, Retire: true}}
			}
			return nil
		}
	})
	first, fresh := newClient(t, s), newClient(t, s)
	first.connect(t)
	first.send(t, first.data(2, 0, []byte("old registration")))
	first.read(t) // request ACK
	response, _ := first.read(t)
	first.ack(t, response)
	old := <-registered
	retire <- old
	fresh.connect(t)
	fresh.send(t, fresh.data(2, 0, []byte("fresh registration")))
	fresh.read(t)
	response, _ = fresh.read(t)
	fresh.ack(t, response)
	select {
	case dropped := <-h.disconnected:
		if dropped != old {
			t.Fatal("retired another transport generation")
		}
	case <-time.After(time.Second):
		t.Fatal("old transport was not retired promptly")
	}
	first.send(t, first.data(3, 0, []byte("stale RPC")))
	first.quiet(t, 50*time.Millisecond)
	fresh.send(t, fresh.data(3, 0, []byte("still registered")))
	fresh.read(t)
	response, _ = fresh.read(t)
	fresh.ack(t, response)
	message, err := decodePayload(response.Payload, 1024)
	if err != nil || string(message) != "still registered" {
		t.Fatal("fresh connection was affected", err)
	}
}
