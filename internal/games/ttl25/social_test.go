package ttl25

import (
	"bytes"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func TestNoClanReplyMatchesRetailDecoder(t *testing.T) {
	b, err := socialBootstrap(quazal.Request{Protocol: 101, Method: 17})
	if err != nil {
		t.Fatal(err)
	}
	r := rankingReader{b: b}
	if r.u32() != 0 || r.u32() != 0 || r.str() != "" || r.str() != "" {
		t.Fatal("status/clan identity")
	}
	for range 4 {
		if r.u32() != 0 {
			t.Fatal("clan statistics")
		}
	}
	if r.str() != "" || r.u32() != 0 || r.str() != "" || r.str() != "" {
		t.Fatal("leader/member identity")
	}
	if !bytes.Equal(r.take(3), []byte{0, 0, 0}) || r.u32() != 0 || !bytes.Equal(r.take(2), []byte{0, 0}) || !r.done() {
		t.Fatal("member flags/gatherings or unexpected fields")
	}
	if len(b) != 52 {
		t.Fatalf("concrete decoder consumes 52 bytes, got %d", len(b))
	}
}

func TestTextFilterUsesAcceptedPolarity(t *testing.T) {
	for _, tt := range []struct {
		text     string
		accepted byte
	}{
		{"Test Room", 1}, {"", 0}, {"   ", 0}, {"bad\nname", 0}, {"bad\x7fname", 0},
	} {
		got, err := socialBootstrap(quazal.Request{Protocol: 101, Method: 10, Args: rankingString(nil, tt.text)})
		if err != nil || !bytes.Equal(got, []byte{tt.accepted}) {
			t.Fatalf("filter polarity: %x %v", got, err)
		}
	}
	for _, args := range [][]byte{{}, {3, 0, 'a'}, append(rankingString(nil, "Room"), 0)} {
		if _, err := socialBootstrap(quazal.Request{Protocol: 101, Method: 10, Args: args}); err == nil {
			t.Fatal("accepted malformed filter request")
		}
	}
}

func TestInitialBrowseReturnsNoPlayableSessions(t *testing.T) {
	for _, query := range []string{
		"WHERE Gatherings.type = 'TTLSession' AND TTLSessions.isPrivate = 0",
		"WHERE Gatherings.type = 'TTLSession' AND TTLSessions.isPrivate = 0 AND TTLSessions.gameType = 2",
	} {
		args := rankingString(nil, query)
		args = rankingU32(args, 0)
		args = rankingU32(args, 25)
		got, err := newSessions().search(args)
		if err != nil || !bytes.Equal(got, []byte{0, 0, 0, 0}) {
			t.Fatalf("initial browse: %x %v", got, err)
		}
		for n := 0; n < len(args); n++ {
			if _, err := newSessions().search(args[:n]); err == nil {
				t.Fatalf("accepted truncated query %d", n)
			}
		}
	}
	s := &rankingStore{}
	got, err := s.handle(1000, quazal.Request{Protocol: 21, Method: 24, Args: append(rankingU32(nil, 0), rankingU32(nil, 25)...)})
	if err != nil || !bytes.Equal(got, []byte{0, 0, 0, 0}) {
		t.Fatal("empty gathering invitations", err)
	}
}

func TestInitialSocialAndStatsReads(t *testing.T) {
	for _, request := range []quazal.Request{
		{Protocol: 101, Method: 5, Args: rankingU32(nil, 25)},
		{Protocol: 101, Method: 6, Args: append(rankingU32(nil, 1), rankingU32(nil, 1000)...)},
		{Protocol: 101, Method: 7, Args: append(rankingU32(nil, 1000), rankingU32(nil, 25)...)},
		{Protocol: 101, Method: 16, Args: rankingString(nil, "en-US")},
	} {
		got, err := socialBootstrap(request)
		if err != nil || !bytes.Equal(got, []byte{0, 0, 0, 0}) {
			t.Fatalf("initial read %d: %x %v", request.Method, got, err)
		}
	}
	got, err := socialBootstrap(quazal.Request{Protocol: 101, Method: 8})
	if err != nil || !bytes.Equal(got, make([]byte, 12)) {
		t.Fatal("empty alert lists", err)
	}
	got, err = socialBootstrap(quazal.Request{Protocol: 101, Method: 38, Args: rankingString(nil, "English")})
	if err != nil || !bytes.Equal(got, rankingString(nil, "")) {
		t.Fatal("empty MOTD", err)
	}
}
func TestSocialBootstrapOnlyAcceptsVerifiedQueries(t *testing.T) {
	b, err := socialBootstrap(quazal.Request{Protocol: 101, Method: 6, Args: []byte{0, 0, 0, 0}})
	if err != nil || !bytes.Equal(b, []byte{0, 0, 0, 0}) {
		t.Fatal("empty player list", err)
	}
	for _, request := range []quazal.Request{
		{Protocol: 101, Method: 17, Args: []byte{0}},
		{Protocol: 101, Method: 6},
		{Protocol: 101, Method: 6, Args: []byte{1, 0, 0, 0}},
		{Protocol: 101, Method: 6, Args: []byte{0, 0, 0, 0, 0}},
		{Protocol: 101, Method: 18},
		{Protocol: 25, Method: 17},
	} {
		if _, err := socialBootstrap(request); err == nil {
			t.Fatalf("accepted unsupported query %+v", request)
		}
	}
}
