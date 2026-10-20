package app

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func modelIDFromImpactPath(path string) (string, bool) {
	const suffix = "/impact"
	if !strings.HasPrefix(path, "/admin/models/") || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, "/admin/models/"), suffix)
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", false
	}
	return id, true
}

func (s *Server) handleProviderImpact(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id, ok := providerIDFromProviderPath(r.URL.Path, "/impact")
	if !ok {
		http.NotFound(w, r)
		return
	}
	out, err := s.pg.ProviderImpact(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "provider_not_found", "Provider was not found", reqID)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to compute the channel impact", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleModelImpact(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id, ok := modelIDFromImpactPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	out, err := s.pg.ModelImpact(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to compute the model impact", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
