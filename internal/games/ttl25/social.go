package ttl25

import (
	"errors"
	"strings"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// socialBootstrap supplies initial empty social/statistics state. Mutation and
// populated statistics remain unsupported. Access is gated by the shared
// service's registered named-account proof before calling this adapter.
func socialBootstrap(request quazal.Request) ([]byte, error) {
	if request.Protocol != 101 {
		return nil, errors.New("unsupported social protocol")
	}
	switch request.Method {
	case 5, 7: // GetTopPlayers / GetLadderStats: no reported statistics yet.
		r := rankingReader{b: request.Args}
		if request.Method == 7 {
			r.u32()
		} // pivot PID
		count := r.u32()
		if !r.done() || count > 100 {
			return nil, quazal.ErrRMC
		}
		return rankingU32(nil, 0), nil
	case 8: // GetAlerts: friend, clan and match invitation PID lists.
		if len(request.Args) != 0 {
			return nil, quazal.ErrRMC
		}
		return make([]byte, 12), nil
	case 16, 38: // FriendsQuery / GetMessageOfTheDay: SDK locale String.
		r := rankingReader{b: request.Args}
		r.strLimit(64)
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		if request.Method == 38 {
			return rankingString(nil, ""), nil
		}
		return rankingU32(nil, 0), nil
	case 10: // FilterText returns one bool: true means the text is accepted.
		r := rankingReader{b: request.Args}
		text := r.str()
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		if acceptsText(text) {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	case 17: // ClanGetMyClanInfo: status u32, EidosClanProps, EidosClanMember.
		if len(request.Args) != 0 {
			return nil, quazal.ErrRMC
		}
		// This title uses zero for the application status (not Quazal qResult).
		b := rankingU32(nil, 0)
		// Concrete structs serialize fields directly, without AnyData headers.
		b = rankingU32(b, 0)     // no clan ID
		b = rankingString(b, "") // abbreviation
		b = rankingString(b, "") // name
		for range 4 {
			b = rankingU32(b, 0)
		} // rank, rating, kills, member count
		b = rankingString(b, "") // leader
		b = rankingU32(b, 0)     // no clan-member identity
		b = rankingString(b, "") // player
		b = rankingString(b, "") // clan abbreviation
		b = append(b, 0, 0, 0)   // leader, officer, accepting invitations
		b = rankingU32(b, 0)     // no member gatherings
		b = append(b, 0, 0)      // friend, online
		return b, nil
	case 6: // GetStatsForPlayers: bounded list of PIDs, not a friend query.
		r := rankingReader{b: request.Args}
		n := r.u32()
		if n > 256 {
			return nil, quazal.ErrRMC
		}
		for range n {
			r.u32()
		}
		if !r.done() {
			return nil, quazal.ErrRMC
		}
		// No statistic records exist until the reporting service is implemented.
		return rankingU32(nil, 0), nil
	default:
		return nil, errors.New("unsupported social method")
	}
}

func acceptsText(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	for _, b := range []byte(text) {
		if b < 0x20 || b == 0x7f {
			return false
		}
	}
	return true
}
