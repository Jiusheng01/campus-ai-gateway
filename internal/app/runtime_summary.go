package app

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/jackc/pgx/v5"
)

func modelIDFromRuntimeSummaryPath(path string) (string, bool) {
	return modelIDFromPath(path, "/runtime-summary")
}

func runtimeWindow(r *http.Request) (time.Time, time.Time, bool) {
	hours := 24
	if raw := r.URL.Query().Get("hours"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 168 {
			return time.Time{}, time.Time{}, false
		}
		hours = n
	}
	to := time.Now().UTC()
	return to.Add(-time.Duration(hours) * time.Hour), to, true
}

func (s *Server) handleModelRuntimeSummary(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id, ok := modelIDFromRuntimeSummaryPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	from, to, ok := runtimeWindow(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "invalid_window", "hours must be between 1 and 168", reqID)
		return
	}
	out, err := s.pg.ModelRuntimeSummary(r.Context(), id, from, to)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the model runtime summary", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleProviderRuntimeSummary(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id, ok := providerIDFromProviderPath(r.URL.Path, "/runtime-summary")
	if !ok {
		http.NotFound(w, r)
		return
	}
	from, to, ok := runtimeWindow(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "invalid_window", "hours must be between 1 and 168", reqID)
		return
	}
	out, err := s.pg.ProviderRuntimeSummary(r.Context(), id, from, to)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "provider_not_found", "Provider was not found", reqID)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the channel runtime summary", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
