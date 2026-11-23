package ttl25

import (
	"encoding/json"
	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal/peerrelay"
	"net/http"
)

func relayDiagnostics(next http.Handler, r *nativeRelay) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /connectivity", func(w http.ResponseWriter, request *http.Request) {
		var core *peerrelay.Relay
		if r != nil {
			core = r.core
		}
		state := core.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(state)
	})
	mux.Handle("/", next)
	return mux
}
