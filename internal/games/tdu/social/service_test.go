package social

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

type accounts map[uint32]quazal.Account

func (a accounts) LookupPID(pid uint32) (quazal.Account, bool) { v, ok := a[pid]; return v, ok }
func testAccounts() accounts {
	return accounts{1000: {PID: 1000, Name: "Ace"}, 0x11223344: {PID: 0x11223344, Name: "Bee"}}
}
func request(args []byte) quazal.Request { return quazal.Request{Protocol: 20, Method: 10, Args: args} }
func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, e := hex.DecodeString(s)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestNativeFriendRequestAndRowFixtures(t *testing.T) {
	b, err := os.ReadFile("testdata/native-friends.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Requests []struct {
			Filter, Flag byte
			ArgsHex      string
		}
		Row struct {
			WireHex      string
			PID, Details uint32
			Relationship byte
			StatusHex    string
		}
		EmptyList struct{ WireHex string }
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Requests) != 5 {
		t.Fatal("missing native request fixtures")
	}
	for _, r := range fixture.Requests {
		var gotPID uint32
		var got Query
		s, err := New(testAccounts(), func(pid uint32, q Query) ([]Friend, error) { gotPID, got = pid, q; return nil, nil })
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.Handle(1000, request(mustHex(t, r.ArgsHex)))
		if err != nil || !bytes.Equal(b, mustHex(t, fixture.EmptyList.WireHex)) || gotPID != 1000 || got != (Query{Filter: r.Filter, Flag: r.Flag != 0}) {
			t.Fatal("native request selectors/empty roster mismatch", got, err)
		}
	}
	s, err := New(testAccounts(), func(uint32, Query) ([]Friend, error) {
		return []Friend{{PID: fixture.Row.PID, Relationship: fixture.Row.Relationship, Details: fixture.Row.Details, Status: string(mustHex(t, fixture.Row.StatusHex))}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.Handle(1000, request([]byte{0, 0}))
	want := append([]byte{1, 0, 0, 0}, mustHex(t, fixture.Row.WireHex)...)
	if err != nil || !bytes.Equal(b, want) {
		t.Fatalf("native friend row mismatch: %x want %x: %v", b, want, err)
	}
}

func TestRejectsMalformedRequestAndUnprovedOperations(t *testing.T) {
	calls := 0
	s, err := New(testAccounts(), func(uint32, Query) ([]Friend, error) { calls++; return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]byte{nil, {0}, {0, 0, 0}, {0, 2}, {255, 255}} {
		if _, err := s.Handle(1000, request(args)); !errors.Is(err, ErrRequest) {
			t.Fatal("malformed request accepted", args, err)
		}
	}
	for _, pid := range []uint32{0, quazal.GuestPID, 999} {
		if _, err := s.Handle(pid, request([]byte{0, 0})); !errors.Is(err, ErrAccount) {
			t.Fatal("unknown/guest principal accepted", pid, err)
		}
	}
	for _, q := range []quazal.Request{{Protocol: 20, Method: 1}, {Protocol: 20, Method: 9}, {Protocol: 101, Method: 10}} {
		if _, err := s.Handle(1000, q); !errors.Is(err, ErrUnsupported) {
			t.Fatal("unsupported write/operation accepted", q, err)
		}
	}
	if calls != 0 {
		t.Fatal("rejected request reached source")
	}
	sourceErr := errors.New("relationship state unavailable")
	s, _ = New(testAccounts(), func(uint32, Query) ([]Friend, error) { return nil, sourceErr })
	if _, err = s.Handle(1000, request([]byte{0, 0})); !errors.Is(err, sourceErr) {
		t.Fatal("source failure became successful empty roster", err)
	}
	if _, err = New(testAccounts(), nil); err == nil {
		t.Fatal("implicit roster source accepted")
	}
}

func TestRosterBoundsAndTenantAccounts(t *testing.T) {
	for _, rows := range [][]Friend{
		{{PID: 1000}}, {{PID: quazal.GuestPID}}, {{PID: 999}}, {{PID: 0x11223344}, {PID: 0x11223344}}, {{PID: 0x11223344, Status: "embedded\x00NUL"}}, {{PID: 0x11223344, Status: string(bytes.Repeat([]byte{'s'}, 256))}}, make([]Friend, MaxFriends+1),
	} {
		s, _ := New(testAccounts(), func(uint32, Query) ([]Friend, error) { return rows, nil })
		if b, err := s.Handle(1000, request([]byte{0, 0})); !errors.Is(err, ErrRoster) || b != nil {
			t.Fatal("invalid/foreign roster returned", err)
		}
	}
	all := testAccounts()
	rows := make([]Friend, MaxFriends)
	for i := range rows {
		pid := uint32(2000 + i)
		all[pid] = quazal.Account{PID: pid, Name: "Driver"}
		rows[i] = Friend{PID: pid, Status: string(bytes.Repeat([]byte{0xff}, 255))}
	}
	s, _ := New(all, func(uint32, Query) ([]Friend, error) { return rows, nil })
	if _, err := s.Handle(1000, request([]byte{255, 1})); err != nil {
		t.Fatal("bounded SDK byte strings rejected", err)
	}
}
