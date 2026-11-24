package snowblind

import (
	"bytes"
	"math"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

func statsArgs(selector byte, name string, count uint32) []byte {
	return encode(func(w *quazal.Writer) { w.U8(selector); w.String(name); w.U32(count) })
}

func TestStatsMenusReturnEmptyStateAndKnownPlayerBaseline(t *testing.T) {
	accounts, err := quazal.OpenStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	account, err := accounts.Create("Synthetic", quazalcrypto.Key{1})
	if err != nil {
		t.Fatal(err)
	}
	s := &startup{accounts: accounts}
	call := func(args []byte) []byte {
		t.Helper()
		b, err := s.handle(prudp.Peer{}, account.PID, quazal.Request{Protocol: 101, Method: 2, Args: args})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, args := range [][]byte{statsArgs(1, "", 15), statsArgs(2, "", 15), statsArgs(3, "", 15), statsArgs(0, "Missing", 15), statsArgs(0, "Synthetic", 0)} {
		if !bytes.Equal(call(args), []byte{0, 0, 0, 0}) {
			t.Fatal("invented stats rows")
		}
	}
	r := quazal.NewReader(call(statsArgs(0, "sYnThEtIc", 15)))
	if r.U32() != 1 || r.String(255) != account.Name || r.String(255) != "" || r.U32() != account.PID || r.U32() != 6 {
		t.Fatal("native StatsRow header")
	}
	for _, want := range []float32{0, -1, 0, 0, 0, 0} {
		if got := math.Float32frombits(r.U32()); got != want {
			t.Fatalf("baseline field: %v, want %v", got, want)
		}
	}
	if !r.Done() {
		t.Fatal("trailing reply fields")
	}
	request := quazal.Request{Protocol: 101, Method: 2, Args: statsArgs(1, "", 15)}
	if _, err := s.handle(prudp.Peer{}, quazal.GuestPID, request); err == nil {
		t.Fatal("guest stats query")
	}
}

func TestStatsQueriesRejectTruncationAndInvalidSelectors(t *testing.T) {
	good := statsArgs(1, "", 15)
	for n := range len(good) {
		if _, err := statsQuery(nil, good[:n], nil); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	for _, args := range [][]byte{append(bytes.Clone(good), 0), statsArgs(4, "", 15), statsArgs(1, "", 101), statsArgs(1, "unexpected", 15)} {
		if _, err := statsQuery(nil, args, nil); err == nil {
			t.Fatal("accepted invalid stats query")
		}
	}
}
