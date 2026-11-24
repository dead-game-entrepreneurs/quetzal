package snowblind

import (
	"errors"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

type startup struct {
	ranking  *quazal.RankingStore
	sessions *sessions
	accounts *quazal.Store
	clans    *clanStore
	friends  *friendStore
	stats    *statsStore
}

func (s *startup) handle(peer prudp.Peer, pid uint32, request quazal.Request) ([]byte, error) {
	if pid < 1000 {
		return nil, errors.New("Snowblind requires a named account")
	}
	if request.Protocol == 101 && request.Method == 2 {
		return s.queryStats(pid, request.Args)
	}
	if request.Protocol == 102 && s.clans != nil {
		return s.clans.handle(pid, request)
	}
	if request.Protocol == 103 && s.friends != nil {
		return s.friends.handle(pid, request)
	}
	if (request.Protocol == 103 && request.Method == 5) || (request.Protocol == 102 && (request.Method == 2 || request.Method == 16)) {
		return socialQuery(request)
	}
	if request.Protocol == 101 && request.Method == 1 {
		if len(request.Args) != 0 {
			return nil, quazal.ErrRMC
		}
		// An empty tune list preserves the shipped default (0x228028, state 2).
		return []byte{0, 0, 0, 0}, nil
	}
	if request.Protocol == 26 && request.Method == 1 {
		r := quazal.NewReader(request.Args)
		id := r.U32()
		if !r.Done() {
			return nil, quazal.ErrRMC
		}
		var w quazal.Writer
		w.Bool(s.ranking.Contains(id))
		return w.Bytes()
	}
	if s.sessions != nil {
		if request.Protocol == 21 && request.Method == 1 {
			r := quazal.NewReader(request.Args)
			if r.String(128) == "Ranking" {
				return s.ranking.Handle(pid, request)
			}
		} else if request.Protocol == 21 && (request.Method == 18 || request.Method == 24) {
			return s.ranking.Handle(pid, request)
		}
		return s.sessions.handle(peer, pid, request)
	}
	return s.ranking.Handle(pid, request)
}
