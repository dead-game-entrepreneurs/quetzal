package quazal

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// RankingSpec identifies title-owned bootstrap metadata. Empty column lists are
// supported; concrete column codecs must be added before accepting other lists.
type RankingSpec struct{ Description, Name string }

// RankingStore holds one title-specific shared Ranking gathering,
// creating it when absent. This is metadata, not a playable game session.
type RankingStore struct {
	mu     sync.Mutex
	path   string
	spec   RankingSpec
	record *AnyData
}

type rankingDisk struct {
	Version int     `json:"version"`
	Record  AnyData `json:"record"`
}

func OpenRanking(path string, spec RankingSpec) (*RankingStore, error) {
	if spec.Description == "" || spec.Name == "" {
		return nil, errors.New("ranking metadata is required")
	}
	s := &RankingStore{path: path, spec: spec}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return nil, errors.New("invalid ranking state file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var disk rankingDisk
	if err := d.Decode(&disk); err != nil {
		return nil, err
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF || disk.Version != 1 || ValidateRanking(disk.Record, false, spec) != nil {
		return nil, errors.New("invalid ranking state")
	}
	s.record = &disk.Record
	return s, nil
}

func ValidateRanking(data AnyData, creating bool, spec RankingSpec) error {
	if data.Type != "Ranking" || len(data.Payload) > 1024 {
		return ErrRMC
	}
	r := reader{data: data.Payload}
	n := r.u32()
	id, owner, host := r.u32(), r.u32(), r.u32()
	limits := r.u32() // min/max participants are two packed u16 values.
	policy, argument, flags, state := r.u32(), r.u32(), r.u32(), r.u32()
	description, name := r.str(255), r.str(255)
	children := r.u32()
	if !r.done() || uint64(n) != uint64(len(data.Payload)-4) || host != 0 || limits != 0 || policy != 2 || argument != 0x20 || flags != 1 || state != 0 || description != spec.Description || name != spec.Name || children != 0 {
		return ErrRMC
	}
	if creating && (id != 0 || owner != 0) || !creating && (id != 1 || owner < 1000) {
		return ErrRMC
	}
	return nil
}

func (s *RankingStore) Handle(pid uint32, request Request) ([]byte, error) {
	if request.Protocol == 21 && request.Method == 24 {
		r := reader{data: request.Args}
		r.u32() // result offset
		count := r.u32()
		if !r.done() || count > 100 {
			return nil, ErrRMC
		}
		// FindInvitations: no gathering invitations have been created.
		return binary.LittleEndian.AppendUint32(nil, 0), nil
	}
	if request.Protocol != 21 || (request.Method != 1 && request.Method != 18) {
		return nil, errors.New("unsupported protocol or method")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := reader{data: request.Args}
	if request.Method == 18 {
		description := r.str(255)
		offset, count := r.u32(), r.u32()
		if !r.done() || count > 100 {
			return nil, ErrRMC
		}
		// Reply is a count followed by AnyData records; no qResult prefix.
		if description != s.spec.Description || offset != 0 || count == 0 || s.record == nil {
			return binary.LittleEndian.AppendUint32(nil, 0), nil
		}
		var w Writer
		w.U32(1)
		w.Any(*s.record)
		return w.Bytes()
	}
	data := AnyData{Type: r.str(255)}
	n := r.u32()
	if n > 1024 {
		return nil, ErrRMC
	}
	data.Payload = bytes.Clone(r.take(int(n)))
	if !r.done() || ValidateRanking(data, true, s.spec) != nil || pid < 1000 {
		return nil, ErrRMC
	}
	// Two clients can both observe an empty lookup. Keep one shared record.
	if s.record == nil {
		binary.LittleEndian.PutUint32(data.Payload[4:], 1)
		binary.LittleEndian.PutUint32(data.Payload[8:], pid)
		if err := s.save(data); err != nil {
			return nil, err
		}
		s.record = &data
	}
	// RegisterGathering returns only the assigned gathering ID.
	return binary.LittleEndian.AppendUint32(nil, 1), nil
}

func (s *RankingStore) save(data AnyData) error {
	b, err := json.MarshalIndent(rankingDisk{Version: 1, Record: data}, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".ranking-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Contains reports whether bootstrap registration has assigned this ID.
func (s *RankingStore) Contains(id uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return id == 1 && s.record != nil
}
