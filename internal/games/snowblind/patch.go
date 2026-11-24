package snowblind

import (
	"log/slog"
	"net/http"
	"strconv"
)

// The native 0x21e710 manifest parser treats line one as a download URL and
// line two as a decimal checksum. Zero matches the absent local patch checksum.
// tools/verify_snowblind_startup.py verifies the no-forced-download decision.
const noUpdateManifest = "http://sb1.patch.eidos.com/psb1a/disabled\r\n0\r\n"

func patchHandler(log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/psb1a/scea.txt" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.NotFound(w, r)
			return
		}
		log.Info("patch_check", "game", "snowblind", "remote", r.RemoteAddr, "host", r.Host, "path", r.URL.Path, "status", http.StatusOK)
		w.Header().Set("Content-Type", "text/plain; charset=us-ascii")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(noUpdateManifest)))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(noUpdateManifest))
		}
	})
}
