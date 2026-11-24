package snowblind

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

const queryPrefix = "INNER JOIN BO3Matches ON Gatherings.id = BO3Matches.gid WHERE Gatherings.type='BO3Match' AND BO3Matches.platform="
const queryPopulation = " AND BO3Matches.numPlayers > 0 AND BO3Matches.numPlayers < Gatherings.maxParticipants"

var queryBase = regexp.MustCompile(`^` + regexp.QuoteMeta(queryPrefix) + `([0-9]+)` + regexp.QuoteMeta(queryPopulation))
var queryClauses = []struct {
	key     string
	pattern *regexp.Regexp
}{
	{"type", regexp.MustCompile(`^ AND BO3Matches.type=([0-9]+)`)},
	{"map", regexp.MustCompile(`^ AND BO3Matches.mapID=([0-9]+)`)},
	{"respawn", regexp.MustCompile(`^ AND \(BO3Matches.type = ([0-9]+) OR (!?)\(BO3Matches.mods & ([0-9]+)\)\)`)},
	{"mods", regexp.MustCompile(`^ AND \(BO3Matches.mods & ~\(([0-9]+) \| ([0-9]+)\)\)=([0-9]+)`)},
	{"password", regexp.MustCompile(`^ AND (!?)\(BO3Matches.mods & ([0-9]+)\)`)},
}

// Evaluate only the bounded native builder grammar (0x22fd18). Client text is
// never executed as SQL or passed to a general expression interpreter.
func parseQuery(query string) (func(gathering) bool, error) {
	if len(query) > 2048 {
		return nil, quazal.ErrRMC
	}
	query = strings.Join(strings.Fields(query), " ")
	base := queryBase.FindStringSubmatch(query)
	if base == nil {
		return nil, quazal.ErrRMC
	}
	platform, err := strconv.ParseUint(base[1], 10, 8)
	if err != nil {
		return nil, quazal.ErrRMC
	}
	predicates := []func(gathering) bool{func(g gathering) bool {
		return g.Kind == "BO3Match" && uint64(g.Platform) == platform && g.Players > 0 && uint16(g.Players) < g.MaxParticipants
	}}
	query = query[len(base[0]):]
	seen := map[string]bool{}
	for query != "" {
		matched := false
		for _, clause := range queryClauses {
			m := clause.pattern.FindStringSubmatch(query)
			if m == nil {
				continue
			}
			if seen[clause.key] {
				return nil, quazal.ErrRMC
			}
			seen[clause.key] = true
			values := make([]uint32, len(m)-1)
			for i, s := range m[1:] {
				if s == "" || s == "!" {
					continue
				}
				n, e := strconv.ParseUint(s, 10, 32)
				if e != nil {
					return nil, quazal.ErrRMC
				}
				values[i] = uint32(n)
			}
			switch clause.key {
			case "type":
				n := values[0]
				if n > 255 {
					return nil, quazal.ErrRMC
				}
				predicates = append(predicates, func(g gathering) bool { return uint32(g.Mode) == n })
			case "map":
				n := values[0]
				predicates = append(predicates, func(g gathering) bool { return g.Map == n })
			case "respawn":
				mode, mask, negative := values[0], values[2], m[2] == "!"
				if mode > 255 || mask > 65535 {
					return nil, quazal.ErrRMC
				}
				predicates = append(predicates, func(g gathering) bool { return uint32(g.Mode) == mode || ((uint32(g.Mods)&mask != 0) != negative) })
			case "mods":
				a, b, n := values[0], values[1], values[2]
				if a > 65535 || b > 65535 || n > 65535 {
					return nil, quazal.ErrRMC
				}
				predicates = append(predicates, func(g gathering) bool { return uint32(g.Mods)&^(a|b) == n })
			case "password":
				mask, negative := values[1], m[1] == "!"
				if mask > 65535 {
					return nil, quazal.ErrRMC
				}
				predicates = append(predicates, func(g gathering) bool { return (uint32(g.Mods)&mask != 0) != negative })
			}
			query = query[len(m[0]):]
			matched = true
			break
		}
		if !matched {
			return nil, quazal.ErrRMC
		}
	}
	return func(g gathering) bool {
		for _, p := range predicates {
			if !p(g) {
				return false
			}
		}
		return true
	}, nil
}
