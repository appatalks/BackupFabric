package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiveAccessRejectsCrossOriginAndRebinding(t *testing.T) {
	handler := liveAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct {
		host, origin, contentType string
		status                    int
	}{
		{"127.0.0.1:8080", "", "application/json", http.StatusNoContent},
		{"localhost:8080", "http://localhost:8080", "application/json", http.StatusNoContent},
		{"attacker.example", "", "application/json", http.StatusForbidden},
		{"127.0.0.1:8080", "http://attacker.example", "application/json", http.StatusForbidden},
		{"127.0.0.1:8080", "", "text/plain", http.StatusUnsupportedMediaType},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/live/jobs", nil)
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Content-Type", tc.contentType)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%+v: got %d", tc, rec.Code)
		}
	}
}
