package api

import (
	"context"
	"database/sql"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/appatalks/backupfabric/internal/live"
)

func (s *Server) liveRoutes(mux *http.ServeMux, service *live.Service) {
	mux.HandleFunc("GET /api/v1/live/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":     service != nil,
			"native_push": service != nil && service.NativeEnabled(),
			"warning":     "Experimental workflows. Native-push routes are administrator-provisioned per source on port 122. Arrival and checksums do not confer restore qualification. Native restore execution is manual.",
		})
	})
	if service == nil {
		return
	}
	handle := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, liveAccess(handler))
	}
	handle("GET /api/v1/live/endpoints", func(w http.ResponseWriter, r *http.Request) {
		value, err := service.Store.Endpoints(r.Context())
		s.liveResult(w, r, value, err, http.StatusOK)
	})
	handle("PUT /api/v1/live/endpoints/{id}", func(w http.ResponseWriter, r *http.Request) {
		var e live.Endpoint
		if err := decodeJSON(r, &e); err != nil {
			writeProblem(w, http.StatusBadRequest, err.Error())
			return
		}
		if e.ApplianceID != r.PathValue("id") {
			writeProblem(w, http.StatusBadRequest, "endpoint ID does not match URL")
			return
		}
		appliance, err := s.catalog.Appliance(r.Context(), e.ApplianceID)
		if err != nil {
			s.liveResult(w, r, nil, err, http.StatusOK)
			return
		}
		if !strings.EqualFold(e.Host, appliance.Hostname) {
			writeProblem(w, http.StatusBadRequest, "SSH host must match the registered appliance hostname")
			return
		}
		err = service.SaveEndpoint(r.Context(), e)
		s.liveResult(w, r, e, err, http.StatusOK)
	})
	handle("GET /api/v1/live/settings", func(w http.ResponseWriter, r *http.Request) {
		value, err := service.Store.Settings(r.Context())
		s.liveResult(w, r, value, err, http.StatusOK)
	})
	handle("PUT /api/v1/live/settings", func(w http.ResponseWriter, r *http.Request) {
		var value live.Settings
		if err := decodeJSON(r, &value); err != nil {
			writeProblem(w, http.StatusBadRequest, err.Error())
			return
		}
		err := service.Store.SaveSettings(r.Context(), value)
		s.liveResult(w, r, value, err, http.StatusOK)
	})
	handle("POST /api/v1/live/endpoints/{id}/preflight", func(w http.ResponseWriter, r *http.Request) {
		value, err := service.Preflight(r.Context(), r.PathValue("id"))
		s.liveResult(w, r, value, err, http.StatusOK)
	})
	handle("GET /api/v1/live/jobs", func(w http.ResponseWriter, r *http.Request) {
		value, err := service.Store.Jobs(r.Context())
		s.liveResult(w, r, value, err, http.StatusOK)
	})
	handle("GET /api/v1/live/snapshots", func(w http.ResponseWriter, r *http.Request) {
		value, err := service.NativeSnapshots(r.Context())
		s.liveResult(w, r, value, err, http.StatusOK)
	})
	handle("POST /api/v1/live/reconcile", func(w http.ResponseWriter, r *http.Request) {
		var input struct{}
		if err := decodeJSON(r, &input); err != nil {
			writeProblem(w, http.StatusBadRequest, err.Error())
			return
		}
		value, err := service.ReconcileNativeSnapshots(r.Context())
		s.liveResult(w, r, value, err, http.StatusOK)
	})
	handle("POST /api/v1/live/jobs", func(w http.ResponseWriter, r *http.Request) {
		var value live.Request
		if err := decodeJSON(r, &value); err != nil {
			writeProblem(w, http.StatusBadRequest, err.Error())
			return
		}
		job, err := service.Start(r.Context(), value)
		s.liveResult(w, r, job, err, http.StatusAccepted)
	})
}

func (s *Server) liveResult(w http.ResponseWriter, r *http.Request, value any, err error, status int) {
	if err != nil {
		s.logger.Error("live API request rejected", "path", r.URL.Path, "error", err)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeProblem(w, http.StatusNotFound, "live endpoint or operation not found")
		case errors.Is(err, context.DeadlineExceeded):
			writeProblem(w, http.StatusGatewayTimeout, "operation timed out")
		default:
			writeProblem(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, status, value)
}

func liveAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if value, _, err := net.SplitHostPort(host); err == nil {
			host = value
		}

		ip := net.ParseIP(strings.Trim(host, "[]"))
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			writeProblem(w, http.StatusForbidden, "live API requires a loopback Host header")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			value, err := url.Parse(origin)
			if err != nil || value.Host != r.Host || value.Scheme != "http" {
				writeProblem(w, http.StatusForbidden, "cross-origin live requests are forbidden")
				return
			}
		}
		if r.Method != http.MethodGet {
			contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || contentType != "application/json" {
				writeProblem(w, http.StatusUnsupportedMediaType, "live operations require application/json")
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func apiAccess(next http.Handler) http.Handler {
	guarded := liveAccess(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			guarded.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
