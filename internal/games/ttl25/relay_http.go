package ttl25

import (
	"encoding/json"
	"net/http"
)

func relayDiagnostics(next http.Handler, r *nativeRelay) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /connectivity", func(w http.ResponseWriter, request *http.Request) {
		state := struct {
			Mode             string `json:"mode"`
			Candidates       int    `json:"candidates"`
			Active           int    `json:"active"`
			Draining         int    `json:"draining"`
			ProvedMappings   int    `json:"proved_mappings"`
			ForwardedPackets uint64 `json:"forwarded_packets"`
			ForwardedBytes   uint64 `json:"forwarded_bytes"`
		}{Mode: "direct"}
		if r != nil {
			r.mu.Lock()
			state.Mode = r.cfg.Mode
			state.Candidates = len(r.pairs)
			for _, p := range r.pairs {
				if !p.closing.IsZero() {
					state.Draining++
				}
				if p.active {
					state.Active++
				}
				for i, m := range p.mapping {
					if m.proved {
						state.ProvedMappings++
					}
					state.ForwardedPackets += p.packets[i]
					state.ForwardedBytes += p.bytes[i]
				}
			}
			r.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(state)
	})
	mux.Handle("/", next)
	return mux
}
