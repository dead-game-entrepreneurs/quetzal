package ttl25

import (
	"encoding/binary"
	"fmt"
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
			wantBool(t, b, false)
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
