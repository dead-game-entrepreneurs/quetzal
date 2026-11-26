package rsvpsp

import (
	"slices"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// SparkProtocol is Ubisoft's title protocol, built by 0x08b915a8. Reply
// layouts come from the server-side stubs the client links (0x08ba1f38).
const SparkProtocol = 60

// Invitations 22 replies with two lists (0x08ba471c, then GatheringURLs
// 0x08bf0e10); there are no invitations yet.
var sparkTwoLists = []uint32{22}

// spark routes Spark calls: hosted games (create 4, join 5, player records 9,
// report 12, notices 15/16, next host 18, start 19, browse 20, quick match
// 21), then the empty invitations lists. Statistics (6, 7, 8, 17) are in
// careers.go, friends (10, 11) in friends.go.
func (g *games) spark(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, bool, error) {
	switch request.Method {
	case 4:
		reply, err := g.create(peer, pid, request.Args)
		return reply, true, err
	case 5:
		reply, err := g.join(pid, request.Args)
		return reply, true, err
	case 19:
		reply, err := g.start(pid, request.Args)
		return reply, true, err
	case 9:
		reply, err := g.records(pid, request.Args)
		return reply, true, err
	case 18:
		reply, err := g.nextHost(pid, request.Args)
		return reply, true, err
	case 20, 21:
		reply, err := g.search(pid, request)
		return reply, true, err
	case 12:
		reply, err := g.report(pid, request.Args)
		return reply, true, err
	case 16:
		reply, err := g.leave(pid, request.Args)
		return reply, true, err
	case 15:
		// Game ID and play time, void (stub 0x800f): 0x08b8b9e8 sends it in
		// place of a 60/12 report when the context has no stats to report.
		r := reader{b: request.Args}
		a, b := r.u32(), r.u32()
		if !r.done() {
			return nil, true, quazal.ErrRMC
		}
		g.log.Info("rsvpsp match notice", "method", request.Method, "pid", pid, "a", a, "b", b)
		return nil, true, nil
	}
	if slices.Contains(sparkTwoLists, request.Method) {
		return make([]byte, 8), true, nil
	}
	return nil, false, nil
}
