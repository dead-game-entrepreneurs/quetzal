package ttl25

import (
	"bytes"
	"encoding/binary"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

var rankingSpec = quazal.RankingSpec{Description: "Nonladder", Name: "NonladderRanking"}

type rankingStore struct{ *quazal.RankingStore }

func openRanking(path string) (*rankingStore, error) {
	store, err := quazal.OpenRanking(path, rankingSpec)
	return &rankingStore{store}, err
}
func (s *rankingStore) handle(pid uint32, request quazal.Request) ([]byte, error) {
	return s.RankingStore.Handle(pid, request)
}
func validateRanking(data quazal.AnyData, creating bool) error {
	return quazal.ValidateRanking(data, creating, rankingSpec)
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
