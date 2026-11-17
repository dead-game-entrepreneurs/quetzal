package ttl25

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPatchNoUpdateResponse(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, host := range []string{"ttl1.patch.eidos.com", "ttl2.patch.eidos.com", "ttl3.patch.eidos.com"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			r := httptest.NewRequest(method, "http://"+host+"/TTL/final-version.txt", nil)
			w := httptest.NewRecorder()
			PatchHandler(log).ServeHTTP(w, r)
			if w.Code != 404 || w.Header().Get("Content-Length") != "0" || w.Body.Len() != 0 {
				t.Fatalf("%s %s: response is not an empty 404: %v", method, host, w)
			}
		}
	}
}
