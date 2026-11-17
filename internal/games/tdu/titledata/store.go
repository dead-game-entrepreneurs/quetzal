package titledata

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	maxRecords    = 16384
	maxGames      = 4096
	maxFiles      = 128
	maxStateBytes = 24 << 20
)

type File struct {
	Stats FileStats
	Owner uint32
	Data  []byte
}

// diskState is the live snapshot and the legacy version-1 decoding shape.
// Current persistence uses the explicit, byte-preserving stateV2 DTO.
type diskState struct {
	Version  int
	NextFile uint32
	Games    map[uint32]GameStats
	Events   map[string]EventStats
	Files    map[uint32]File
}
type Store struct {
	mu   sync.Mutex
	path string
	data diskState
}

func eventKey(pid uint32, id uint64) string { return fmt.Sprintf("%d:%d", pid, id) }
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("title data path required")
	}
	s := &Store{path: path, data: diskState{Version: diskVersion, NextFile: 1, Games: map[uint32]GameStats{}, Events: map[string]EventStats{}, Files: map[uint32]File{}}}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		if err = s.persist(s.data); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxStateBytes {
		return nil, errors.New("title data must be a bounded private regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxStateBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxStateBytes {
		return nil, errors.New("title data capacity reached")
	}
	s.data, err = decodeState(b)
	if err != nil {
		return nil, err
	}
	if s.data.Version != diskVersion || s.data.NextFile == 0 || s.data.Games == nil || s.data.Events == nil || s.data.Files == nil || len(s.data.Games) > maxGames || len(s.data.Events) > maxRecords || len(s.data.Files) > maxFiles {
		return nil, errors.New("invalid title data state")
	}
	for pid, g := range s.data.Games {
		if pid == 0 || !validstr(g.Name, 15) || g.Name == "" {
			return nil, ErrWire
		}
	}
	for key, e := range s.data.Events {
		parts := strings.Split(key, ":")
		var pid uint32
		var id uint64
		if len(parts) != 2 {
			return nil, ErrWire
		}
		if _, err := fmt.Sscanf(key, "%d:%d", &pid, &id); err != nil || pid == 0 || id != e.EventID || key != eventKey(pid, id) || !validstr(e.Name, 15) || e.Name == "" {
			return nil, ErrWire
		}
	}
	for id, f := range s.data.Files {
		if id == 0 || id >= s.data.NextFile || f.Stats.ID != uint64(id) || f.Owner == 0 || len(f.Data) == 0 || len(f.Data) > MaxBlob || !validstr(f.Stats.Name, 255) || !validstr(f.Stats.Description, 255) || !validstr(f.Stats.Creator, 15) {
			return nil, ErrWire
		}
	}
	return s, nil
}
func (s *Store) persist(d diskState) error {
	b, err := encodeState(d)
	if err != nil {
		return err
	}
	if len(b)+1 > maxStateBytes {
		return errors.New("title data capacity reached")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".title-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	if dir, e := os.Open(filepath.Dir(s.path)); e == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// Transactions commit the private file before replacing the live snapshot.
func (s *Store) mutate(fn func(*diskState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data
	next.Games = make(map[uint32]GameStats, len(s.data.Games))
	for k, v := range s.data.Games {
		next.Games[k] = v
	}
	next.Events = make(map[string]EventStats, len(s.data.Events))
	for k, v := range s.data.Events {
		next.Events[k] = v
	}
	next.Files = make(map[uint32]File, len(s.data.Files))
	for k, v := range s.data.Files {
		next.Files[k] = v
	}
	if err := fn(&next); err != nil {
		return err
	}
	if err := s.persist(next); err != nil {
		return err
	}
	s.data = next
	return nil
}
