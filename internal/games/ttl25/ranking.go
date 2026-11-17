package ttl25

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

// The login state machine looks up the shared Nonladder Ranking gathering,
// creating it when absent. This is metadata, not a playable game session.
type rankingStore struct {
	mu     sync.Mutex
	path   string
	record *quazal.AnyData
}

type rankingDisk struct {
	Version int            `json:"version"`
	Record  quazal.AnyData `json:"record"`
}

func openRanking(path string) (*rankingStore, error) {
	s := &rankingStore{path: path}
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
	if d.Decode(&trailing) != io.EOF || disk.Version != 1 || validateRanking(disk.Record, false) != nil {
		return nil, errors.New("invalid ranking state")
	}
	s.record = &disk.Record
	return s, nil
}

// Small bounded SDK reader/writer for this title's verified bootstrap records.
type rankingReader struct {
	b   []byte
	err error
}

func (r *rankingReader) take(n int) []byte {
	if r.err != nil || n < 0 || n > len(r.b) {
		r.err = quazal.ErrRMC
		return nil
	}
	b := r.b[:n]
	r.b = r.b[n:]
	return b
}
func (r *rankingReader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}
func (r *rankingReader) str() string {
	return r.strLimit(255)
}
func (r *rankingReader) strLimit(max int) string {
	b := r.take(2)
	if b == nil {
		return ""
	}
	n := int(binary.LittleEndian.Uint16(b))
	if n == 0 {
		return ""
	}
	if n > max+1 {
		r.err = quazal.ErrRMC
		return ""
	}
	b = r.take(n)
	if b == nil {
		return ""
	}
	if b[n-1] != 0 || bytes.IndexByte(b[:n-1], 0) >= 0 {
		r.err = quazal.ErrRMC
		return ""
	}
	return string(b[:n-1])
}
func (r *rankingReader) done() bool        { return r.err == nil && len(r.b) == 0 }
func rankingU32(b []byte, v uint32) []byte { return binary.LittleEndian.AppendUint32(b, v) }
func rankingString(b []byte, s string) []byte {
	b = binary.LittleEndian.AppendUint16(b, uint16(len(s)+1))
	b = append(b, s...)
	return append(b, 0)
}
func rankingAny(b []byte, data quazal.AnyData) []byte {
	b = rankingString(b, data.Type)
	b = rankingU32(b, uint32(len(data.Payload)))
	return append(b, data.Payload...)
}

func validateRanking(data quazal.AnyData, creating bool) error {
	if data.Type != "Ranking" || len(data.Payload) > 1024 {
		return quazal.ErrRMC
	}
	r := rankingReader{b: data.Payload}
	n := r.u32()
	id, owner, host := r.u32(), r.u32(), r.u32()
	limits := r.u32() // min/max participants are two packed u16 values.
	policy, argument, flags, state := r.u32(), r.u32(), r.u32(), r.u32()
	description, name := r.str(), r.str()
	children := r.u32()
	if !r.done() || uint64(n) != uint64(len(data.Payload)-4) || host != 0 || limits != 0 || policy != 2 || argument != 0x20 || flags != 1 || state != 0 || description != "Nonladder" || name != "NonladderRanking" || children != 0 {
		return quazal.ErrRMC
	}
	if creating && (id != 0 || owner != 0) || !creating && (id != 1 || owner < 1000) {
		return quazal.ErrRMC
	}
	return nil
}

func (s *rankingStore) handle(pid uint32, request quazal.Request) ([]byte, error) {
	if request.Protocol == 21 && request.Method == 24 {
		r := rankingReader{b: request.Args}
		r.u32() // result offset
		count := r.u32()
		if !r.done() || count > 100 {
			return nil, quazal.ErrRMC
		}
		// FindInvitations: no gathering invitations have been created.
		return rankingU32(nil, 0), nil
	}
	if request.Protocol != 21 || (request.Method != 1 && request.Method != 18) {
		return nil, errors.New("unsupported protocol or method")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := rankingReader{b: request.Args}
	if request.Method == 18 {
		description := r.str()
		offset, count := r.u32(), r.u32()
		if !r.done() || count > 100 {
			return nil, quazal.ErrRMC
		}
		// Reply is a count followed by AnyData records; no qResult prefix.
		if description != "Nonladder" || offset != 0 || count == 0 || s.record == nil {
			return rankingU32(nil, 0), nil
		}
		return rankingAny(rankingU32(nil, 1), *s.record), nil
	}
	data := quazal.AnyData{Type: r.str()}
	n := r.u32()
	if n > 1024 {
		return nil, quazal.ErrRMC
	}
	data.Payload = bytes.Clone(r.take(int(n)))
	if !r.done() || validateRanking(data, true) != nil || pid < 1000 {
		return nil, quazal.ErrRMC
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
	return rankingU32(nil, 1), nil
}

func (s *rankingStore) save(data quazal.AnyData) error {
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
