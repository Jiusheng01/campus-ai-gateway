package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
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

// handlePublishModel serves POST /admin/models/publish: a candidate becomes an
// external alias with its first route, atomically and idempotently.
func (s *Server) handlePublishModel(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	var in struct {
		ProviderModelID string `json:"provider_model_id"`
		Alias           string `json:"alias"`
		DisplayName     string `json:"display_name"`
		Description     string `json:"description"`
		AppendToExisting bool  `json:"append_to_existing"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON with only provider_model_id, alias, display_name, description and append_to_existing", reqID)
		return
	}
	in.ProviderModelID = strings.TrimSpace(in.ProviderModelID)
	in.Alias = strings.TrimSpace(in.Alias)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Description = strings.TrimSpace(in.Description)
	if in.ProviderModelID == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "provider_model_id is required", reqID)
		return
	}
	if _, err := uuid.Parse(in.ProviderModelID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "provider_model_id must be a valid UUID", reqID)
		return
	}
	if in.Alias == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_model", "alias is required", reqID)
		return
	}
	if len([]rune(in.Alias)) > 128 || len([]rune(in.DisplayName)) > 128 || len([]rune(in.Description)) > 1024 {
		httpx.Error(w, http.StatusBadRequest, "invalid_model", "alias/display_name must be at most 128 characters and description at most 1024", reqID)
		return
	}

	out, err := s.pg.PublishProviderModel(r.Context(), in.ProviderModelID, in.Alias, in.DisplayName, in.Description, in.AppendToExisting)
	switch {
	case errors.Is(err, storage.ErrAliasExists):
		httpx.Error(w, http.StatusConflict, "alias_exists",
			"The alias already exists with a different binding; confirm appending it as a backup line", reqID)
		return
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "provider_model_not_found", "Provider model was not found or is not enabled", reqID)
		return
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to publish the model", reqID)
		return
	}
	status := http.StatusOK
	if out.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, out)
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
