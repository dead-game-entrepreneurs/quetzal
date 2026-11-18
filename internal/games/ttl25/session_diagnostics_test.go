package ttl25

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/protocol/prudp"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

func TestRetailMapFirstBrowseKeepsAllFilters(t *testing.T) {
	s := newSessions()
	peer := prudp.Peer{ID: 1, LocalPort: 30121}
	if _, err := s.register(peer, 1000, sessionArgs("Synthetic room", 0)); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		"WHERE TTLSessions.levelName = 'Test Level' AND Gatherings.type = 'TTLSession' AND TTLSessions.isPrivate = 0",
		"WHERE TTLSessions.isPrivate = 0 AND TTLSessions.levelName = 'Test Level' AND Gatherings.type = 'TTLSession'",
		publicSessionQuery + " AND TTLSessions.levelName = 'Test Level'",
	}
	want, err := s.search(sessionSearch(queries[0], 0, 25))
	if err != nil || !bytes.Equal(want[:4], []byte{1, 0, 0, 0}) {
		t.Fatal("retail map-first search did not find the matching room", err)
	}
	for _, query := range queries[1:] {
		got, err := s.search(sessionSearch(query, 0, 25))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("clause order changed browse results: %v", err)
		}
	}
	wrongMap := "WHERE TTLSessions.levelName = 'Different Level' AND Gatherings.type = 'TTLSession' AND TTLSessions.isPrivate = 0"
	if got, err := s.search(sessionSearch(wrongMap, 0, 25)); err != nil || !bytes.Equal(got, []byte{0, 0, 0, 0}) {
		t.Fatal("map filter was lost", err)
	}
	for _, query := range []string{
		"WHERE TTLSessions.levelName = 'Test Level'",
		"WHERE Gatherings.type = 'TTLRound' AND TTLSessions.isPrivate = 0",
		publicSessionQuery + " AND Gatherings.type = 'TTLSession'",
		"WHERE TTLSessions.levelName = 'Test Level' OR Gatherings.type = 'TTLSession'",
		"WHERE TTLSessions.levelName = 'Test Level'; DELETE FROM Gatherings AND Gatherings.type = 'TTLSession'",
	} {
		if _, err := s.search(sessionSearch(query, 0, 25)); err == nil {
			t.Fatalf("accepted unsupported query shape %q", query)
		}
	}
}

func TestBrowseDiagnosticsExplainResultsWithoutFreeText(t *testing.T) {
	var output bytes.Buffer
	s := newSessions()
	s.log = slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for i, private := range []byte{0, 1} {
		if _, err := s.register(prudp.Peer{ID: uint64(i + 1), LocalPort: 30121}, uint32(1000+i), sessionArgs("private-player-text@example.test", private)); err != nil {
			t.Fatal(err)
		}
	}
	peer := prudp.Peer{ID: 50, LocalPort: 30121}
	query := "WHERE TTLSessions.levelName = 'Test Level' AND Gatherings.type = 'TTLSession' AND TTLSessions.isPrivate = 0"
	request := quazal.Request{Protocol: 101, Method: 2, CallID: 99, Args: sessionSearch(query, 0, 25)}
	if _, err := s.searchFor(peer, 1050, request); err != nil {
		t.Fatal(err)
	}
	if _, err := s.searchFor(peer, 1050, quazal.Request{Protocol: 101, Method: 2, CallID: 100, Args: sessionSearch("invalid-private-query@example.test", 0, 25)}); err == nil {
		t.Fatal("invalid query accepted")
	}
	for _, value := range []string{"private-player-text@example.test", "Test Level", query, "invalid-private-query@example.test"} {
		if bytes.Contains(output.Bytes(), []byte(value)) {
			t.Fatalf("diagnostics leaked free text %q", value)
		}
	}
	found, rejected := false, false
	decoder := json.NewDecoder(&output)
	for decoder.More() {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		switch event["msg"] {
		case "matchmaking search completed":
			found = true
			if event["pid"] != float64(1050) || event["peer"] != float64(50) || event["call_id"] != float64(99) || event["total_rooms"] != float64(2) || event["matched_count"] != float64(1) {
				t.Fatal("browse correlation or counts missing", event)
			}
			ids := event["returned_room_ids"].([]any)
			if len(ids) != 1 || ids[0] != float64(10000) || event["excluded_by_filter"].(map[string]any)["isPrivate"] != float64(1) {
				t.Fatal("returned IDs or exclusion reason missing", event)
			}
		case "matchmaking search rejected":
			rejected = event["query_hash"] != "" && event["call_id"] == float64(100)
		}
	}
	if !found || !rejected {
		t.Fatal("successful/rejected search diagnostics missing")
	}
}

func TestBrowseCandidateDiagnosticsAreBounded(t *testing.T) {
	var output bytes.Buffer
	s := newSessions()
	s.log = slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for i := range 40 {
		if _, err := s.register(prudp.Peer{ID: uint64(i + 1), LocalPort: 30121}, uint32(1000+i), sessionArgs(fmt.Sprint("fixture", i), 0)); err != nil {
			t.Fatal(err)
		}
	}
	output.Reset()
	if _, err := s.search(sessionSearch(publicSessionQuery, 0, 25)); err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(output.Bytes(), []byte(`"msg":"matchmaking browse candidate"`)); got != 32 {
		t.Fatalf("candidate detail log count %d, want 32", got)
	}
}
