package ttl25

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func rankingCreate() []byte {
	var b []byte
	for _, v := range []uint32{0, 0, 0, 0, 2, 0x20, 1, 0} {
		b = rankingU32(b, v)
	}
	b = rankingString(b, "Nonladder")
	b = rankingString(b, "NonladderRanking")
	b = rankingU32(b, 0)
	b = append(rankingU32(nil, uint32(len(b))), b...)
	return rankingAny(nil, quazal.AnyData{Type: "Ranking", Payload: b})
}
func rankingLookup(offset, count uint32) quazal.Request {
	b := rankingString(nil, "Nonladder")
	b = rankingU32(b, offset)
	b = rankingU32(b, count)
	return quazal.Request{Protocol: 21, Method: 18, Args: b}
}
func TestRankingBootstrapPersistsAndIsShared(t *testing.T) {
	// Retail startup request: public shared metadata, no account fields.
	retail, err := hex.DecodeString("080052616e6b696e6700470000004300000000000000000000000000000000000000020000002000000001000000000000000a004e6f6e6c61646465720011004e6f6e6c616464657252616e6b696e670000000000")
	if err != nil || !bytes.Equal(retail, rankingCreate()) {
		t.Fatal("retail ranking layout changed")
	}
	path := filepath.Join(t.TempDir(), "ranking.json")
	s, err := openRanking(path)
	if err != nil {
		t.Fatal(err)
	}
	query := rankingLookup(0, 25)
	got, err := s.handle(1000, query)
	if err != nil || !bytes.Equal(got, []byte{0, 0, 0, 0}) {
		t.Fatalf("empty lookup: %x %v", got, err)
	}
	create := quazal.Request{Protocol: 21, Method: 1, Args: rankingCreate()}
	for _, pid := range []uint32{1000, 1001} {
		got, err = s.handle(pid, create)
		if err != nil || !bytes.Equal(got, []byte{1, 0, 0, 0}) {
			t.Fatalf("registration: %x %v", got, err)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("ranking state permissions", err)
	}
	s, err = openRanking(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.handle(1001, query)
	if err != nil {
		t.Fatal(err)
	}
	r := rankingReader{b: got}
	if r.u32() != 1 || r.str() != "Ranking" {
		t.Fatal("lookup response shape")
	}
	n := r.u32()
	data := quazal.AnyData{Type: "Ranking", Payload: r.take(int(n))}
	if !r.done() || validateRanking(data, false) != nil {
		t.Fatal("stored Ranking shape")
	}
	if binary.LittleEndian.Uint32(data.Payload[8:]) != 1000 {
		t.Fatal("duplicate changed original owner")
	}
	for _, q := range []quazal.Request{rankingLookup(1, 25), rankingLookup(0, 0)} {
		got, err = s.handle(1001, q)
		if err != nil || !bytes.Equal(got, []byte{0, 0, 0, 0}) {
			t.Fatal("pagination", err)
		}
	}
}
func TestRankingRejectsMalformedAndDoesNotPublishFailedWrite(t *testing.T) {
	s, _ := openRanking(filepath.Join(t.TempDir(), "missing", "ranking.json"))
	create := quazal.Request{Protocol: 21, Method: 1, Args: rankingCreate()}
	for n := 0; n < len(create.Args); n++ {
		bad := create
		bad.Args = bad.Args[:n]
		if _, err := s.handle(1000, bad); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	if _, err := s.handle(1000, create); err == nil || s.record != nil {
		t.Fatal("failed write published record")
	}
	if _, err := s.handle(1000, rankingLookup(0, 101)); err == nil {
		t.Fatal("unbounded result count")
	}
	bad := create
	bad.Args = append(bytes.Clone(bad.Args), 0)
	if _, err := s.handle(1000, bad); err == nil {
		t.Fatal("trailing data")
	}
	bad = create
	bad.Args = bytes.Clone(bad.Args)
	bad.Args[0] = 255
	if _, err := s.handle(1000, bad); err == nil {
		t.Fatal("invalid type length")
	}
}
