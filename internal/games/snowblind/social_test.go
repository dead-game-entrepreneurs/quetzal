package snowblind

import (
	"bytes"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func TestEmptySocialQueriesHaveCompleteNativeReplies(t *testing.T) {
	tests := []struct {
		p          byte
		m          uint32
		args, want []byte
	}{
		{103, 5, encode(func(w *quazal.Writer) { w.String("") }), []byte{0, 0, 0, 0}},
		{102, 16, nil, []byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 1, 0, 0}},
	}
	s := &startup{}
	for _, test := range tests {
		request := quazal.Request{Protocol: test.p, Method: test.m, Args: test.args}
		got, err := s.handle(prudp.Peer{}, 1000, request)
		if err != nil || !bytes.Equal(got, test.want) {
			t.Fatalf("%d/%d: %x %v", test.p, test.m, got, err)
		}
		if _, err = s.handle(prudp.Peer{}, quazal.GuestPID, request); err == nil {
			t.Fatal("guest social read")
		}
		request.Args = append(append([]byte{}, test.args...), 0)
		if _, err = socialQuery(request); err == nil {
			t.Fatal("trailing social arguments")
		}
	}
	for n := 0; n < 3; n++ {
		if _, err := socialQuery(quazal.Request{Protocol: 103, Method: 5, Args: []byte{1, 0, 0}[:n]}); err == nil {
			t.Fatal("truncated query")
		}
	}
}

func TestNoClanDescriptionIncludesEveryNativeOutput(t *testing.T) {
	request := quazal.Request{Protocol: 102, Method: 2}
	b, err := (&startup{}).handle(prudp.Peer{}, 1000, request)
	if err != nil {
		t.Fatal(err)
	}
	r := quazal.NewReader(b)
	if r.U32() != clanNotMember {
		t.Fatal("native no-membership status")
	}
	// Clan descriptor: two Strings, a signed 32-bit value, then notices.
	if r.String(64) != "" || r.String(64) != "" || r.U32() != 0 || r.U32() != 0 {
		t.Fatal("empty clan descriptor")
	}
	if r.Bool() || r.Bool() || r.Bool() || r.String(512) != "" || r.String(512) != "" {
		t.Fatal("empty clan alerts")
	}
	if r.U32() != 0 {
		t.Fatal("fabricated clan members")
	}
	if r.String(64) != "" || r.String(64) != "" || r.Bool() || r.Bool() || r.Bool() || r.U32() != 0 || r.Bool() || !r.Done() {
		t.Fatal("empty own-member descriptor")
	}
	request.Args = []byte{0}
	if _, err := socialQuery(request); err == nil {
		t.Fatal("trailing clan arguments")
	}
	if _, err := (&startup{}).handle(prudp.Peer{}, quazal.GuestPID, quazal.Request{Protocol: 102, Method: 2}); err == nil {
		t.Fatal("guest clan description")
	}
}
