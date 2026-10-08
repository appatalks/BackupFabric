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

	"github.com/appatalks/backupfabric/internal/catalog"
	"github.com/appatalks/backupfabric/internal/model"
	"github.com/appatalks/backupfabric/internal/orchestrator"
	"github.com/appatalks/backupfabric/internal/provider"
)

func TestApplianceCollectionWorkflow(t *testing.T) {
	c := openTestCatalog(t)
	p := provider.NewDevelopmentProvider()
	server := httptest.NewServer(New(
		c, orchestrator.New(c, p), p.Name(), slog.New(slog.NewTextHandler(io.Discard, nil)),
	))
	defer server.Close()

	appliance := post[model.Appliance](t, server.URL+"/api/v1/appliances", map[string]string{
		"name": "production-a", "hostname": "ghe-a.example.com", "volume_id": "vol-a",
	})
	if appliance.ID == "" {
		t.Fatal("expected generated appliance ID")
	}

	backup := post[model.Backup](t,
		server.URL+"/api/v1/appliances/"+appliance.ID+"/collections",
		model.CollectionRequest{NativeTimestamp: "20261001T010203"},
	)
	if backup.State != "collected" {
		t.Fatalf("state = %q, want collected", backup.State)
	}
	if backup.ProviderID == "" {
		t.Fatal("expected provider snapshot ID")
	}

	response, err := http.Get(server.URL + "/api/v1/backups")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var backups []model.Backup
	if err := json.NewDecoder(response.Body).Decode(&backups); err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 || backups[0].ApplianceID != appliance.ID {
		t.Fatalf("unexpected backups: %#v", backups)
	}
}

func TestRejectsDuplicateVolumeAndInvalidTimestamp(t *testing.T) {
	c := openTestCatalog(t)
	p := provider.NewDevelopmentProvider()
	handler := New(c, orchestrator.New(c, p), p.Name(), slog.Default())

	first := perform(handler, http.MethodPost, "/api/v1/appliances", map[string]string{
		"name": "one", "hostname": "one.example.com", "volume_id": "vol-shared",
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first registration = %d: %s", first.Code, first.Body.String())
	}

	second := perform(handler, http.MethodPost, "/api/v1/appliances", map[string]string{
		"name": "two", "hostname": "two.example.com", "volume_id": "vol-shared",
	})
	if second.Code != http.StatusConflict {
		t.Fatalf("duplicate volume = %d, want %d", second.Code, http.StatusConflict)
	}

	var appliance model.Appliance
	if err := json.Unmarshal(first.Body.Bytes(), &appliance); err != nil {
		t.Fatal(err)
	}
	result := perform(handler, http.MethodPost,
		"/api/v1/appliances/"+appliance.ID+"/collections",
		model.CollectionRequest{NativeTimestamp: "../escape"},
	)
	if result.Code != http.StatusBadRequest {
		t.Fatalf("invalid timestamp = %d, want %d", result.Code, http.StatusBadRequest)
	}
}

func TestEmptyListsAreJSONArrays(t *testing.T) {
	c := openTestCatalog(t)
	p := provider.NewDevelopmentProvider()
	handler := New(c, orchestrator.New(c, p), p.Name(), slog.Default())

	for _, path := range []string{"/api/v1/appliances", "/api/v1/backups"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Host = "127.0.0.1"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if body := recorder.Body.String(); body != "[]\n" {
			t.Fatalf("%s body = %q, want JSON array", path, body)
		}
	}
}

func openTestCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func post[T any](t *testing.T, url string, input any) T {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(url, "application/json", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("POST %s returned %s: %s", url, response.Status, body)
	}
	var output T
	if err := json.NewDecoder(response.Body).Decode(&output); err != nil {
		t.Fatal(err)
	}
	return output
}

func perform(handler http.Handler, method, path string, input any) *httptest.ResponseRecorder {
	encoded, _ := json.Marshal(input)
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Host = "127.0.0.1"
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
