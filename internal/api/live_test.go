package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/appatalks/backupfabric/internal/live"
)

func TestNativeReconciliationAPI(t *testing.T) {
	catalog := openTestCatalog(t)
	store, err := catalog.LiveStore()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := live.NewService(store, filepath.Join(root, "archives"), filepath.Join(root, "secrets"), logger, live.ExecRunner{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	server := &Server{logger: logger}
	mux := http.NewServeMux()
	server.liveRoutes(mux, service)
	for _, input := range []struct {
		body, host, contentType string
		status                  int
	}{
		{"{}", "127.0.0.1:8080", "application/json", http.StatusOK},
		{"{}", "127.0.0.1:8080", "application/json", http.StatusOK},
		{"{", "127.0.0.1:8080", "application/json", http.StatusBadRequest},
		{"{\"unexpected\":true}", "127.0.0.1:8080", "application/json", http.StatusBadRequest},
		{"{}", "untrusted.example", "application/json", http.StatusForbidden},
		{"{}", "127.0.0.1:8080", "text/plain", http.StatusUnsupportedMediaType},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/live/reconcile", bytes.NewBufferString(input.body))
		request.Host = input.host
		request.Header.Set("Content-Type", input.contentType)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != input.status {
			t.Fatalf("unexpected reconciliation response: %+v %d %s", input, response.Code, response.Body.String())
		}
		if input.status == http.StatusOK {
			var result live.NativeReconciliation
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Observed != 0 || result.Problems == nil {
				t.Fatalf("unexpected empty scan result: %+v %v", result, err)
			}
		}
	}
}

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
