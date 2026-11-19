package ttl25

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func testRoundArgs() []byte {
	var body []byte
	for range 8 {
		body = rankingU32(body, 0)
	}
	body = rankingString(body, "")
	body = append(body, 0)
	for _, v := range []uint32{13, 0, 240} {
		body = rankingU32(body, v)
	}
	body = rankingString(body, "Test Level")
	body = rankingU32(body, 1)
	return rankingAny(nil, quazal.AnyData{Type: "TTLRound", Payload: append(rankingU32(nil, uint32(len(body))), body...)})
}

func TestRoundReportingParticipantOrderingAndHostCleanup(t *testing.T) {
	for _, guestFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(guestFirst), func(t *testing.T) {
			f := newCommunityFixture(t)
			c, s := f.c, f.c.sessions
			now := time.Unix(100, 0)
			s.now = func() time.Time { return now }
			b, err := s.register(f.pa, f.a.PID, testRoundArgs())
			if err != nil {
				t.Fatal(err)
			}
			gid := binary.LittleEndian.Uint32(b)
			for _, who := range []struct {
				peer prudp.Peer
				pid  uint32
			}{{f.pa, f.a.PID}, {f.pb, f.b.PID}} {
				b, err = s.lobby(who.peer, who.pid, quazal.Request{Method: 11, Args: rankingString(rankingU32(nil, gid), "")})
				if err != nil {
					t.Fatal(err)
				}
				wantBool(t, b, true)
			}
			a, bStats := make([]float32, 15), make([]float32, 15)
			a[0], bStats[0] = 50, 10
			args := reportArgs(gid, []uint32{f.a.PID, f.b.PID}, [][]float32{a, bStats})
			if guestFirst {
				b, err = c.gatheringReport(f.pb, f.b.PID, args)
				if err != nil {
					t.Fatal(err)
				}
				wantBool(t, b, true)
				if len(c.state.Stats) != 0 || len(c.state.Reports) != 0 {
					t.Fatal("participant wrote aggregate totals")
				}
			}
			b, err = c.gatheringReport(f.pa, f.a.PID, args)
			if err != nil {
				t.Fatal(err)
			}
			wantBool(t, b, true)
			leave := rankingString(rankingU32(nil, gid), "Ending round participation")
			b, err = s.extension(f.pa, f.a.PID, quazal.Request{Method: 1, Args: leave})
			if err != nil {
				t.Fatal(err)
			}
			wantBool(t, b, true)
			if _, exists := s.rooms[gid]; exists {
				t.Fatal("finished round discoverable")
			}
			b, err = c.gatheringReport(f.pb, f.b.PID, args)
			if err != nil {
				t.Fatal(err)
			}
			wantBool(t, b, true)
			personal := make([]float32, 87)
			personal[0] = 3
			communityCall(t, c, f.pb, f.b.PID, 9, append(rankingU32(nil, 87), floatBytes(personal)...))
			if c.state.Stats[f.b.PID].Values[15] != 3 || len(c.state.Reports) != 3 {
				t.Fatal("late personal stats not saved under round receipt")
			}
			b, err = s.extension(f.pb, f.b.PID, quazal.Request{Method: 1, Args: leave})
			if err != nil {
				t.Fatal(err)
			}
			wantBool(t, b, true)
			a[0] = 500
			changed := reportArgs(gid, []uint32{f.a.PID, f.b.PID}, [][]float32{a, bStats})
			b, err = c.gatheringReport(f.pb, f.b.PID, changed)
			if err != nil {
				t.Fatal(err)
			}
			wantBool(t, b, true)
			if c.state.Stats[f.a.PID].Values[0] != 50 {
				t.Fatal("non-authoritative report changed host totals")
			}
			b, err = c.gatheringReport(f.pd, f.d.PID, args)
			if err != nil {
				t.Fatal(err)
			}
			wantBool(t, b, false)
			otherConnection := f.pb
			otherConnection.ID++
			b, err = c.gatheringReport(otherConnection, f.b.PID, args)
			if err != nil {
				t.Fatal(err)
			}
			wantBool(t, b, false)
			now = now.Add(2 * time.Minute)
			b, err = c.gatheringReport(f.pb, f.b.PID, args)
			if err != nil {
				t.Fatal(err)
			}
			wantBool(t, b, false)
			if _, ok := s.reportKeyFor(f.pb, f.b.PID); ok {
				t.Fatal("expired round grants reporting authority")
			}
		})
	}
}

func TestRoundReportLateParticipantAndDepartedRoster(t *testing.T) {
	for _, hostFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(hostFirst), func(t *testing.T) {
			f := newCommunityFixture(t)
			c, s := f.c, f.c.sessions
			b, err := s.register(f.pa, f.a.PID, testRoundArgs())
			if err != nil {
				t.Fatal(err)
			}
			gid := binary.LittleEndian.Uint32(b)
			for _, who := range []struct {
				peer prudp.Peer
				pid  uint32
			}{{f.pa, f.a.PID}, {f.pb, f.b.PID}, {f.pd, f.d.PID}} {
				b, err = s.lobby(who.peer, who.pid, quazal.Request{Method: 11, Args: rankingString(rankingU32(nil, gid), "")})
				if err != nil {
					t.Fatal(err)
				}
				wantBool(t, b, true)
			}
			// The stock late participant submits the initial roster without a row
			// for itself, and may have a different snapshot from the round host.
			a, bStats := make([]float32, 15), make([]float32, 15)
			a[0], bStats[0] = 50, 10
			authoritative := reportArgs(gid, []uint32{f.a.PID, f.b.PID}, [][]float32{a, bStats})
			a[0], bStats[0] = 2, 1
			late := reportArgs(gid, []uint32{f.a.PID, f.b.PID}, [][]float32{a, bStats})
			report := func(peer prudp.Peer, pid uint32, args []byte, accepted bool) {
				t.Helper()
				b, err := c.gatheringReport(peer, pid, args)
				if err != nil {
					t.Fatal(err)
				}
				wantBool(t, b, accepted)
			}
			if !hostFirst {
				report(f.pd, f.d.PID, late, true)
				if len(c.state.Stats) != 0 || len(c.state.Reports) != 0 {
					t.Fatal("late participant committed aggregate")
				}
			}
			report(f.pa, f.a.PID, authoritative, true)
			// An early participant exits before the host. Its row must remain a
			// valid report target after the host removes the discoverable round.
			leave := rankingString(rankingU32(nil, gid), "Ending round participation")
			for _, who := range []struct {
				peer prudp.Peer
				pid  uint32
			}{{f.pb, f.b.PID}, {f.pa, f.a.PID}} {
				b, err = s.extension(who.peer, who.pid, quazal.Request{Method: 1, Args: leave})
				if err != nil {
					t.Fatal(err)
				}
				wantBool(t, b, true)
			}
			if _, exists := s.rooms[gid]; exists {
				t.Fatal("completed round remains discoverable")
			}
			b, err = s.extension(f.pb, f.b.PID, quazal.Request{Method: 1, Args: leave})
			if err != nil {
				t.Fatal(err)
			}
			wantBool(t, b, true)
			report(f.pd, f.d.PID, late, true)
			if len(c.state.Stats) != 2 || len(c.state.Reports) != 2 || c.state.Stats[f.a.PID].Values[0] != 50 || c.state.Stats[f.b.PID].Values[0] != 10 {
				t.Fatal("late participant overwrote host totals")
			}
			// A disconnected host cannot revoke other live participants' grace.
			s.disconnect(f.pa, f.a.PID)
			report(f.pd, f.d.PID, late, true)
			report(f.pa, f.a.PID, authoritative, false)
			personal := make([]float32, 87)
			personal[0] = 7
			communityCall(t, c, f.pd, f.d.PID, 9, append(rankingU32(nil, 87), floatBytes(personal)...))
			if c.state.Stats[f.d.PID].Values[15] != 7 || c.state.Stats[f.d.PID].Values[0] != 0 {
				t.Fatal("late participant personal statistics missing or aggregate invented")
			}
			outsider := f.pd
			outsider.ID++
			report(outsider, f.d.PID, late, false)
			unknownTarget := reportArgs(gid, []uint32{99999}, [][]float32{a})
			report(f.pd, f.d.PID, unknownTarget, false)
			s.disconnect(f.pd, f.d.PID)
			report(f.pd, f.d.PID, late, false)
			if _, ok := s.reportKeyFor(f.pd, f.d.PID); ok {
				t.Fatal("disconnected participant retains personal reporting authority")
			}
		})
	}
}

func TestEmptyPersonalReportAcknowledgedWithoutChangingStatistics(t *testing.T) {
	f := newCommunityFixture(t)
	c, s := f.c, f.c.sessions
	b, err := s.register(f.pa, f.a.PID, testRoundArgs())
	if err != nil {
		t.Fatal(err)
	}
	gid := binary.LittleEndian.Uint32(b)
	b, err = s.lobby(f.pb, f.b.PID, quazal.Request{Method: 11, Args: rankingString(rankingU32(nil, gid), "")})
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, b, true)
	empty := rankingU32(nil, 0)
	before, err := os.ReadFile(c.path)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if reply := communityCall(t, c, f.pb, f.b.PID, 9, empty); len(reply) != 0 {
			t.Fatal("empty personal report has unexpected output fields")
		}
	}
	after, err := os.ReadFile(c.path)
	if err != nil || !bytes.Equal(before, after) || len(c.state.Stats) != 0 || len(c.state.Reports) != 0 {
		t.Fatal("empty personal report created persistent statistics", err)
	}
	values := make([]float32, 87)
	values[0], values[86] = 7, 3
	communityCall(t, c, f.pb, f.b.PID, 9, append(rankingU32(nil, 87), floatBytes(values)...))
	before, err = os.ReadFile(c.path)
	if err != nil {
		t.Fatal(err)
	}
	communityCall(t, c, f.pb, f.b.PID, 9, empty)
	after, err = os.ReadFile(c.path)
	if err != nil || !bytes.Equal(before, after) || c.state.Stats[f.b.PID].Values[15] != 7 || c.state.Stats[f.b.PID].Values[101] != 3 || len(c.state.Reports) != 1 {
		t.Fatal("empty report cleared stored values or replay receipts", err)
	}
	for _, invalid := range [][]byte{append(empty, 0), append(rankingU32(nil, 1), floatBytes([]float32{0})...)} {
		if _, err := c.handle(f.pb, f.b.PID, quazal.Request{Protocol: 101, Method: 9, Args: invalid}); err == nil {
			t.Fatal("malformed or partial personal vector accepted")
		}
	}
	if _, err := c.handle(f.pd, f.d.PID, quazal.Request{Protocol: 101, Method: 9, Args: empty}); err == nil {
		t.Fatal("empty report bypassed round authority")
	}
	s.disconnect(f.pb, f.b.PID)
	if _, err := c.handle(f.pb, f.b.PID, quazal.Request{Protocol: 101, Method: 9, Args: empty}); err == nil {
		t.Fatal("disconnected participant submitted empty report")
	}
}
