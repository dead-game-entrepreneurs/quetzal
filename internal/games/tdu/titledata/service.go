package titledata

import (
	"bytes"
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

type Accounts interface {
	LookupPID(uint32) (quazal.Account, bool)
}
type Service struct {
	store    *Store
	accounts Accounts
}

func New(store *Store, accounts Accounts) (*Service, error) {
	if store == nil || accounts == nil {
		return nil, errors.New("title data requires tenant store and accounts")
	}
	return &Service{store, accounts}, nil
}
func (s *Service) Handle(pid uint32, q quazal.Request) ([]byte, error) {
	a, ok := s.accounts.LookupPID(pid)
	if !ok {
		return nil, errors.New("unknown title data account")
	}
	switch q.Protocol {
	case 102:
		return s.challenge(a, q)
	case 103:
		return s.stats(a, q)
	}
	return nil, errors.New("unsupported title data protocol")
}
func (s *Service) stats(a quazal.Account, q quazal.Request) ([]byte, error) {
	r := reader{b: q.Args}
	switch q.Method {
	case 1:
		g := readGame(&r)
		if !r.done() || !strings.EqualFold(g.Name, a.Name) {
			return nil, ErrWire
		}
		g.Name = a.Name
		err := s.store.mutate(func(d *diskState) error {
			if _, ok := d.Games[a.PID]; !ok && len(d.Games) >= maxGames {
				return errors.New("game record capacity")
			}
			d.Games[a.PID] = g
			return nil
		})
		if err != nil {
			return nil, err
		}
		return []byte{1}, nil
	case 6:
		e := readEvent(&r)
		if !r.done() || !strings.EqualFold(e.Name, a.Name) {
			return nil, ErrWire
		}
		e.Name = a.Name
		err := s.store.mutate(func(d *diskState) error {
			k := eventKey(a.PID, e.EventID)
			if _, ok := d.Events[k]; !ok && len(d.Events) >= maxRecords {
				return errors.New("event record capacity")
			}
			d.Events[k] = e
			return nil
		})
		if err != nil {
			return nil, err
		}
		return []byte{1}, nil
	case 2:
		selector, start, count := r.u32(), r.u32(), r.u32()
		if !r.done() || selector < 2 || selector > 7 || count > 20 {
			return nil, ErrWire
		}
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		games := sortedGames(s.store.data, selector)
		page := slicePage(games, start, count)
		b := put32(nil, uint32(len(page)))
		for _, g := range page {
			v, _ := g.MarshalBinary()
			b = append(b, v...)
		}
		return put32(b, uint32(len(games))), nil
	case 4:
		name := r.str(15)
		if !r.done() {
			return nil, ErrWire
		}
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		var g GameStats
		found := false
		for _, v := range s.store.data.Games {
			if strings.EqualFold(v.Name, name) {
				g = v
				found = true
				break
			}
		}
		if !found {
			return append(put32(nil, 0), make([]byte, 8)...), nil
		}
		wire, _ := g.MarshalBinary()
		b := append(put32(nil, 1), wire...)
		b = put32(b, uint32(len(s.store.data.Games)))
		b = put32(b, 6)
		for selector := uint32(2); selector <= 7; selector++ {
			rank := uint32(0)
			for i, v := range sortedGames(s.store.data, selector) {
				if strings.EqualFold(v.Name, name) {
					rank = uint32(i + 1)
					break
				}
			}
			b = put32(b, rank)
		}
		return b, nil
	case 7:
		id, selector, start, count := r.u64(), r.u32(), r.u32(), r.u32()
		if !r.done() || (selector != 1 && selector != 4) || count > 20 {
			return nil, ErrWire
		}
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		events := sortedEvents(s.store.data, id, selector)
		page := slicePage(events, start, count)
		b := put32(nil, uint32(len(page)))
		for _, e := range page {
			v, _ := e.MarshalBinary()
			b = append(b, v...)
		}
		return put32(b, uint32(len(events))), nil
	case 10:
		n := r.u32()
		if n > 20 {
			return nil, ErrWire
		}
		ids := make([]uint64, n)
		for i := range ids {
			ids[i] = r.u64()
		}
		name := r.str(15)
		if !r.done() {
			return nil, ErrWire
		}
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		var events []EventStats
		var ranks []uint32
		for _, id := range ids {
			for i, e := range sortedEvents(s.store.data, id, 1) {
				if strings.EqualFold(e.Name, name) {
					events = append(events, e)
					ranks = append(ranks, uint32(i+1))
					break
				}
			}
		}
		b := put32(nil, uint32(len(events)))
		for _, e := range events {
			v, _ := e.MarshalBinary()
			b = append(b, v...)
		}
		b = put32(b, uint32(len(ranks)))
		for _, rank := range ranks {
			b = put32(b, rank)
		}
		return b, nil
	default:
		return nil, errors.New("unsupported event statistics method")
	}
}
func sortedGames(d diskState, selector uint32) []GameStats {
	out := make([]GameStats, 0, len(d.Games))
	for _, g := range d.Games {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		x, y := out[i].metric(selector), out[j].metric(selector)
		if math.IsNaN(x) {
			x = math.Inf(-1)
		}
		if math.IsNaN(y) {
			y = math.Inf(-1)
		}
		if x != y {
			return x > y
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Ranking direction is explicit replacement-server policy, not an inferred
// anticheat/winner calculation: selector1 ascending metric0, selector4 descending3.
func sortedEvents(d diskState, id uint64, selector uint32) []EventStats {
	var out []EventStats
	for _, e := range d.Events {
		if e.EventID == id {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		field := 0
		if selector == 4 {
			field = 3
		}
		x, y := out[i].Values[field], out[j].Values[field]
		if x != y {
			if selector == 4 {
				return x > y
			}
			return x < y
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
func slicePage[T any](rows []T, start, count uint32) []T {
	if uint64(start) >= uint64(len(rows)) {
		return nil
	}
	end := min(uint64(len(rows)), uint64(start)+uint64(count))
	return rows[int(start):int(end)]
}
func (s *Service) challenge(a quazal.Account, q quazal.Request) ([]byte, error) {
	r := reader{b: q.Args}
	switch q.Method {
	case 1:
		category, name, description, creator := r.u32(), r.str(255), r.str(255), r.str(15)
		data := r.blob()
		if !r.done() || len(data) == 0 || name == "" || !strings.EqualFold(creator, a.Name) {
			return nil, ErrWire
		}
		var id uint32
		err := s.store.mutate(func(d *diskState) error {
			// A repeated upload by this account with identical content is idempotent.
			for key, f := range d.Files {
				if f.Owner == a.PID && f.Stats.Category == category && f.Stats.Name == name && f.Stats.Description == description && bytes.Equal(f.Data, data) {
					id = key
					return nil
				}
			}
			if len(d.Files) >= maxFiles || d.NextFile == 0 || d.NextFile == ^uint32(0) {
				return errors.New("challenge capacity reached")
			}
			id = d.NextFile
			d.NextFile++
			d.Files[id] = File{Stats: FileStats{ID: uint64(id), Category: category, Name: name, Description: description, Creator: a.Name}, Owner: a.PID, Data: data}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return put32(nil, id), nil
	case 2:
		category, id := r.u32(), r.u64()
		if !r.done() {
			return nil, ErrWire
		}
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		f, ok := s.store.data.Files[uint32(id)]
		if !ok || id != uint64(uint32(id)) || f.Stats.Category != category {
			return putblob([]byte{0}, nil), nil
		}
		return putblob([]byte{1}, f.Data), nil
	case 3, 4:
		var category, start, count uint32
		var name string
		if q.Method == 3 {
			category = r.u32()
			count = 20
		} else {
			count, start = r.u32(), r.u32()
			flag := r.u8()
			name = r.str(255)
			if flag != 0 {
				return nil, errors.New("unsupported challenge filter flag")
			}
		}
		if !r.done() || count > 100 {
			return nil, ErrWire
		}
		count = min(count, 20)
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		var files []File
		for _, f := range s.store.data.Files {
			if q.Method == 3 && f.Stats.Category != category {
				continue
			}
			if name != "" && !strings.Contains(strings.ToLower(f.Stats.Name), strings.ToLower(name)) {
				continue
			}
			files = append(files, f)
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Stats.ID < files[j].Stats.ID })
		files = slicePage(files, start, count)
		b := put32(nil, uint32(len(files)))
		for _, f := range files {
			b = append(b, f.Stats.marshal()...)
		}
		return b, nil
	default:
		return nil, errors.New("unsupported challenge method")
	}
}
