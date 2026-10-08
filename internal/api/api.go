package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/appatalks/backupfabric/internal/catalog"
	"github.com/appatalks/backupfabric/internal/identity"
	"github.com/appatalks/backupfabric/internal/live"
	"github.com/appatalks/backupfabric/internal/model"
	"github.com/appatalks/backupfabric/internal/orchestrator"
	"github.com/appatalks/backupfabric/internal/ui"
)

const Version = "0.1.0-dev"

var (
	namePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	volumePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`)
)

type Server struct {
	catalog      *catalog.Catalog
	orchestrator *orchestrator.Orchestrator
	providerName string
	logger       *slog.Logger
}

func New(
	catalog *catalog.Catalog,
	orchestrator *orchestrator.Orchestrator,
	providerName string,
	logger *slog.Logger,
	liveServices ...*live.Service,
) http.Handler {
	server := &Server{
		catalog:      catalog,
		orchestrator: orchestrator,
		providerName: providerName,
		logger:       logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", server.health)
	mux.HandleFunc("GET /api/v1/appliances", server.listAppliances)
	mux.HandleFunc("POST /api/v1/appliances", server.createAppliance)
	mux.HandleFunc("POST /api/v1/appliances/{id}/collections", server.collect)
	mux.HandleFunc("GET /api/v1/backups", server.listBackups)
	var liveService *live.Service
	if len(liveServices) > 0 {
		liveService = liveServices[0]
	}
	server.liveRoutes(mux, liveService)
	mux.Handle("/", ui.Handler())
	return requestLog(logger, securityHeaders(apiAccess(mux)))
}

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, model.Health{
		Status:   "ok",
		Version:  Version,
		Provider: s.providerName,
	})
}

func (s *Server) listAppliances(writer http.ResponseWriter, request *http.Request) {
	appliances, err := s.catalog.ListAppliances(request.Context())
	if err != nil {
		s.internalError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, appliances)
}

func (s *Server) createAppliance(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Name        string `json:"name"`
		Hostname    string `json:"hostname"`
		VolumeID    string `json:"volume_id"`
		ApplianceID string `json:"appliance_uuid"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Hostname = strings.ToLower(strings.TrimSpace(input.Hostname))
	input.VolumeID = strings.TrimSpace(input.VolumeID)
	input.ApplianceID = strings.TrimSpace(input.ApplianceID)
	if !namePattern.MatchString(input.Name) {
		writeProblem(writer, http.StatusBadRequest, "invalid appliance name")
		return
	}
	if !hostnamePattern.MatchString(input.Hostname) {
		writeProblem(writer, http.StatusBadRequest, "invalid hostname")
		return
	}
	if !volumePattern.MatchString(input.VolumeID) {
		writeProblem(writer, http.StatusBadRequest, "invalid volume identifier")
		return
	}

	id, err := identity.New("app")
	if err != nil {
		s.internalError(writer, request, err)
		return
	}
	appliance := model.Appliance{
		ID:          id,
		Name:        input.Name,
		Hostname:    input.Hostname,
		VolumeID:    input.VolumeID,
		ApplianceID: input.ApplianceID,
		State:       "enabled",
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.catalog.CreateAppliance(request.Context(), appliance); err != nil {
		if catalog.IsConflict(err) {
			writeProblem(writer, http.StatusConflict, "name, hostname, or volume is already registered")
			return
		}
		s.internalError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, appliance)
}

func (s *Server) collect(writer http.ResponseWriter, request *http.Request) {
	var input model.CollectionRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(writer, http.StatusBadRequest, err.Error())
		return
	}
	backup, err := s.orchestrator.Collect(
		request.Context(), request.PathValue("id"), input.NativeTimestamp,
	)
	if err != nil {
		switch {
		case errors.Is(err, catalog.ErrNotFound):
			writeProblem(writer, http.StatusNotFound, "appliance not found")
		case catalog.IsConflict(err):
			writeProblem(writer, http.StatusConflict, "snapshot timestamp is already cataloged")
		default:
			writeProblem(writer, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(writer, http.StatusCreated, backup)
}

func (s *Server) listBackups(writer http.ResponseWriter, request *http.Request) {
	backups, err := s.catalog.ListBackups(request.Context())
	if err != nil {
		s.internalError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, backups)
}

func (s *Server) internalError(writer http.ResponseWriter, request *http.Request, err error) {
	s.logger.Error("request failed", "method", request.Method, "path", request.URL.Path, "error", err)
	writeProblem(writer, http.StatusInternalServerError, "internal server error")
}

func decodeJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}

func writeProblem(writer http.ResponseWriter, status int, detail string) {
	writeJSON(writer, status, map[string]any{
		"status": status,
		"error":  detail,
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(writer, request)
	})
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(writer, request)
		logger.Info("http request",
			"method", request.Method,
			"path", request.URL.Path,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}
