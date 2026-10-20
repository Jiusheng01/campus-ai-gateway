package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// handleModelCatalog serves GET /admin/model-catalog: every enabled channel's
// Provider Models, grouped by upstream name, with the open state of each.
//
// It is management-only by construction — it is registered under /admin — and
// it deliberately says nothing a tenant sees: the client-facing answer to
// "which models may I call" is still /v1/models, derived from Virtual Models
// and API Key allow-lists (ADR-0018).
func (s *Server) handleModelCatalog(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	items, err := s.pg.ModelCatalog(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the model catalog", reqID)
		return
	}
	if items == nil {
		items = []model.CatalogModel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

// handleOpenCatalogModel serves POST /admin/model-catalog/open.
//
// The body names the Provider Model, not a model name: a Channel's list row is
// the fact being opened, and the gateway resolves its provider and upstream
// name from storage rather than trusting a client-supplied pair that might not
// belong to the same channel. A repeat is a success with both creation flags
// false — the unique binding, not a pre-check, is what makes it idempotent.
func (s *Server) handleOpenCatalogModel(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	var in struct {
		ProviderModelID string `json:"provider_model_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	in.ProviderModelID = strings.TrimSpace(in.ProviderModelID)
	if in.ProviderModelID == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "provider_model_id is required", reqID)
		return
	}
	if _, err := uuid.Parse(in.ProviderModelID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "provider_model_id must be a valid UUID", reqID)
		return
	}

	out, err := s.pg.OpenProviderModel(r.Context(), in.ProviderModelID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "provider_model_not_found", "Provider model was not found", reqID)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to open the model", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
