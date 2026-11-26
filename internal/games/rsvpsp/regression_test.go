package rsvpsp

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestAccountRPCLogsOmitPayloads(t *testing.T) {
	logs := &logBuffer{}
	a := startWithLog(t, slog.New(slog.NewTextHandler(logs, nil)))
	g := guest(t, a, 8000, true)
	// A malformed public profile must not put its real name or birthdate
	// into the validation error that the shared RPC handler logs.
	public := record(str("Private Real Name"), []byte{2}, make([]byte, 8), str("US"), str("en"), str(""), str("Private City"))
	if g.exchange(25, 21, signupArgs("Rejected", "secretpass", public, privateRecord())).Success {
		t.Fatal("malformed profile accepted")
	}
	user, _ := account(t, a, "LogTest", 8100)
	initial := quazalcrypto.InitialKey([]byte("DifferentPassword"))
	hash := hex.EncodeToString(initial[:])
	private := record(str("Private Question"), str("Private Answer"))
	for _, request := range []struct {
		protocol byte
		method   uint32
		args     []byte
	}{
		{25, 4, str(hash)},
		{25, 23, cat(anyData(publicType, public), anyData(privateType, private))},
		{60, 14, cat(str("LogTest"), str("Private Answer"), str(hash))},
	} {
		if user.exchange(request.protocol, request.method, request.args).Success {
			t.Fatalf("unsupported RPC %d/%d accepted", request.protocol, request.method)
		}
	}
	output := logs.String()
	for _, value := range []string{"Private Real Name", "Private City", "Private Question", "Private Answer", hash} {
		if strings.Contains(output, value) || strings.Contains(output, hex.EncodeToString([]byte(value))) {
			t.Fatalf("account payload leaked into routine logs: %q", value)
		}
	}
	if !strings.Contains(output, "rsvpsp unsupported rpc") || !strings.Contains(output, "method=4") ||
		!strings.Contains(output, "bytes=35") || !strings.Contains(output, "bad bool") {
		t.Fatal("missing RPC or validation metadata")
	}
}

func TestLateReportsCannotConsumeNextRound(t *testing.T) {
	rig := newStatsRig(t, t.TempDir())
	id := binary.LittleEndian.Uint32(rig.spark(t, rig.host, 1002, 4, createArgs(t)))
	join := cat(u32(id), []byte{0, 0})
	rig.spark(t, rig.joiner, 1000, 5, join)
	table := cat(u32(2), statRecord(1000, "SNEED", 2, 0, 0, 0, 0, 0, 2, 93), statRecord(1002, "TRIS", 1, 0, 0, 0, 0, 0, 1, 3))
	report := cat(u32(id), u32(184), table)
	rig.spark(t, rig.host, 1002, 12, report)
	for round := uint32(2); round <= 3; round++ {
		rig.spark(t, rig.host, 1002, 18, u32(id))
		rig.spark(t, rig.host, 1002, 4, createNextRoundArgs(t))
		// The host has advanced, but the other client's previous-round
		// report arrives before that client follows it with 60/5.
		rig.spark(t, rig.joiner, 1000, 12, report)
		if got := rig.c.players[1000].Matches; got != round-1 {
			t.Fatalf("late report counted in round %d: matches=%d", round, got)
		}
		if len(rig.g.byID[id].stats) != 0 {
			t.Fatal("late report populated next round's records")
		}
		rig.spark(t, rig.joiner, 1000, 5, join)
		// Identical stats and play time are legitimate in a new round.
		// A payload hash carried across rounds would discard this result.
		rig.spark(t, rig.joiner, 1000, 12, report)
		rig.spark(t, rig.host, 1002, 12, report)
		for _, pid := range []uint32{1000, 1002} {
			got := rig.c.players[pid]
			if got.Matches != round || got.Seconds != uint64(round)*184 {
				t.Fatalf("round %d pid %d: %+v", round, pid, got)
			}
		}
	}
}

func TestStatsSaveFailureCanRecoverOverUDP(t *testing.T) {
	a := start(t)
	host, hostPID := account(t, a, "RetryHost", 8200)
	joiner, joinerPID := account(t, a, "RetryJoiner", 8300)
	id := binary.LittleEndian.Uint32(host.rpc(60, 4, createArgs(t)))
	joiner.rpc(60, 5, cat(u32(id), []byte{0, 0}))
	table := cat(u32(2), statRecord(hostPID, "RetryHost", 2, 0, 0, 0, 0, 0, 2, 10),
		statRecord(joinerPID, "RetryJoiner", 1, 0, 0, 0, 0, 0, 1, 10))
	report := cat(u32(id), u32(180), table)
	path := filepath.Join(a.state, "stats.json")
	// Make the atomic rename fail without relying on filesystem permissions
	// or exhausting the host disk, then restore the same path.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if host.exchange(60, 12, report).Success {
		t.Fatal("stats upload acknowledged despite save failure")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	joiner.rpc(60, 12, report)
	host.rpc(60, 12, report)
	reopened, err := openCareers(path, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, pid := range []uint32{hostPID, joinerPID} {
		got := reopened.players[pid]
		if got.Matches != 1 || got.Seconds != 180 || got.Shots != 10 {
			t.Fatalf("recovered stats for %d: %+v", pid, got)
		}
	}
}
