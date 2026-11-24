package snowblind

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func encode(f func(*quazal.Writer)) []byte {
	var w quazal.Writer
	f(&w)
	b, err := w.Bytes()
	if err != nil {
		panic(err)
	}
	return b
}

func TestStartupRankingPersistsAndRejectsOtherTitleRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ranking.json")
	ranking, err := quazal.OpenRanking(path, rankingSpec)
	if err != nil {
		t.Fatal(err)
	}
	s := &startup{ranking: ranking}
	call := func(protocol byte, method uint32, args []byte) []byte {
		t.Helper()
		b, err := s.handle(prudp.Peer{}, 1000, quazal.Request{Protocol: protocol, Method: method, Args: args})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	query := encode(func(w *quazal.Writer) { w.String("BO3Stats"); w.U32(0); w.U32(25) })
	if b := call(101, 1, nil); !bytes.Equal(b, []byte{0, 0, 0, 0}) {
		t.Fatal("tune defaults", b)
	}
	if b := call(21, 18, query); !bytes.Equal(b, []byte{0, 0, 0, 0}) {
		t.Fatal("empty ranking", b)
	}
	if b := call(26, 1, []byte{1, 0, 0, 0}); !bytes.Equal(b, []byte{0}) {
		t.Fatal("unregistered competition")
	}
	body := encode(func(w *quazal.Writer) {
		w.Gathering(quazal.Gathering{Policy: 2, PolicyArgument: 0x20, Flags: 1, Description: "BO3Stats"})
		w.String("BO3Stats")
		w.U32(0)
	})
	any := quazal.AnyData{Type: "Ranking", Payload: encode(func(w *quazal.Writer) { w.Buffer(body) })}
	create := encode(func(w *quazal.Writer) { w.Any(any) })
	fixture, err := os.ReadFile("testdata/ranking-retail.json")
	if err != nil {
		t.Fatal(err)
	}
	var retail struct {
		Args string `json:"args_hex"`
	}
	if err = json.Unmarshal(fixture, &retail); err != nil {
		t.Fatal(err)
	}
	captured, err := hex.DecodeString(retail.Args)
	if err != nil || !bytes.Equal(create, captured) {
		t.Fatal("retail Ranking bootstrap layout changed")
	}

	for n := range len(create) {
		if _, err := s.handle(prudp.Peer{}, 1000, quazal.Request{Protocol: 21, Method: 1, Args: create[:n]}); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	if b := call(21, 1, create); !bytes.Equal(b, []byte{1, 0, 0, 0}) {
		t.Fatal("ranking ID", b)
	}
	if b := call(26, 1, []byte{1, 0, 0, 0}); !bytes.Equal(b, []byte{1}) {
		t.Fatal("competition registration", b)
	}
	if _, err := quazal.OpenRanking(path, quazal.RankingSpec{Description: "Nonladder", Name: "NonladderRanking"}); err == nil {
		t.Fatal("accepted TTL metadata")
	}
	ranking, err = quazal.OpenRanking(path, rankingSpec)
	if err != nil {
		t.Fatal(err)
	}
	s.ranking = ranking
	r := quazal.NewReader(call(21, 18, query))
	if r.U32() != 1 {
		t.Fatal("missing persisted ranking")
	}
	stored := r.Any(1024)
	if !r.Done() || quazal.ValidateRanking(stored, false, rankingSpec) != nil {
		t.Fatal("stored ranking shape")
	}
	for _, request := range []quazal.Request{{Protocol: 101, Method: 1, Args: []byte{0}}, {Protocol: 26, Method: 1, Args: []byte{1}}, {Protocol: 103, Method: 1}, {Protocol: 101, Method: 4}, {Protocol: 21, Method: 1, Args: create}} {
		pid := uint32(1000)
		if request.Protocol == 21 {
			pid = quazal.GuestPID
		}
		if _, err := s.handle(prudp.Peer{}, pid, request); err == nil {
			t.Fatalf("accepted unsupported or unauthorized request %+v", request)
		}
	}
}

func TestPatchManifestMatchesNativeFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/startup-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Manifest   string
		States     []int
		PatchError int  `json:"patch_error"`
		Download   bool `json:"download_requested"`
	}
	if err := json.Unmarshal(fixture, &native); err != nil {
		t.Fatal(err)
	}
	if native.Manifest != noUpdateManifest || native.PatchError != 0 || native.Download {
		t.Fatal("HTTP response differs from the native no-update trace")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		r := httptest.NewRequest(method, "http://sb2.patch.eidos.com/psb1a/scea.txt", nil)
		w := httptest.NewRecorder()
		patchHandler(log).ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Result())
		}
		if method == http.MethodGet && w.Body.String() != noUpdateManifest {
			t.Fatal("manifest")
		}
		if method == http.MethodHead && w.Body.Len() != 0 {
			t.Fatal("HEAD has body")
		}
	}
	for _, path := range []string{"/psb1a/disabled", "/psb1a/scea0.txt", "/TTL/final-version.txt"} {
		w := httptest.NewRecorder()
		patchHandler(log).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 404 {
			t.Fatalf("unexpected patch content for %s", path)
		}
	}
}
