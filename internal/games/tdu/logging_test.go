package tdu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"testing"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/games/tdu/nativeproxy"
)

func TestNativeDebugLogsCorrelateDistinctFailuresWithoutPayloads(t *testing.T) {
	var output bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rejected := errors.New("unsupported test operation")
	handlers := map[uint16]nativeproxy.DataHandler{9001: func(context.Context, nativeproxy.SessionInfo, netip.AddrPort, []byte) ([]byte, error) {
		return nil, rejected
	}}
	batches := map[uint16]nativeproxy.BatchDataHandler{9002: func(context.Context, nativeproxy.SessionInfo, netip.AddrPort, []byte) ([][]byte, error) {
		return nil, rejected
	}}
	traceNativeServices(log, handlers, batches)
	info := nativeproxy.SessionInfo{Remote: netip.MustParseAddrPort("192.0.2.50:4567"), ID: 7, Generation: 200, TransportActivated: true}
	for i, port := range []uint16{9001, 9002, 9002} {
		packet, err := (ednet.Frame{Kind: ednet.Call, RequestID: uint32(100 + i), Timeout: 1000, Action: append([]byte{0xbe, byte(i)}, []byte("private-resource-content")...)}).MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		dest := netip.AddrPortFrom(netip.MustParseAddr("192.0.2.10"), port)
		if handle := handlers[port]; handle != nil {
			_, err = handle(context.Background(), info, dest, packet)
		} else {
			_, err = batches[port](context.Background(), info, dest, packet)
		}
		if !errors.Is(err, rejected) {
			t.Fatal("handler error changed", err)
		}
	}
	if strings.Contains(output.String(), "private-resource-content") {
		t.Fatal("payload was logged")
	}
	decoder := json.NewDecoder(&output)
	for i, port := range []float64{9001, 9002, 9002} {
		var entry map[string]any
		if err := decoder.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		if entry["logical_port"] != port || entry["generation"] != float64(200) || entry["request_id"] != float64(100+i) || entry["envelope"] != "0x509a" || entry["action"] != fmt.Sprintf("0xbe%02x", i) || entry["msg"] != "native service request rejected" {
			t.Fatalf("uncorrelated failure: %v", entry)
		}
	}
}
