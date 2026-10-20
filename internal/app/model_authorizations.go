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

func modelIDFromAuthorizationsPath(path string) (string, bool) {
	return modelIDFromPath(path, "/authorizations")
}

// handleModelAuthorizations is super_admin-only. The RBAC allow-list excludes
// every other role before this handler is reached.
func (s *Server) handleModelAuthorizations(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	modelID, ok := modelIDFromAuthorizationsPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	principal, _ := adminPrincipalFrom(r.Context())
	if !principal.Roles[RoleSuperAdmin] {
		httpx.Error(w, http.StatusForbidden, "forbidden", "Model authorization requires super_admin", reqID)
		return
	}

	switch r.Method {
	case http.MethodGet:
		alias, items, err := s.pg.ListModelAuthorizations(r.Context(), modelID)
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load model authorizations", reqID)
			return
		}
		if items == nil {
			items = []model.ModelAuthorization{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"alias": alias, "data": items})
	case http.MethodPut:
		s.setModelAuthorizations(w, r, modelID, reqID)
	default:
		w.Header().Set("Allow", "GET, PUT")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) setModelAuthorizations(w http.ResponseWriter, r *http.Request, modelID, reqID string) {
	var in struct {
		APIKeyIDs []string `json:"api_key_ids"`
		Allow     *bool    `json:"allow"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must contain only api_key_ids and allow", reqID)
		return
	}
	if in.Allow == nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "allow is required", reqID)
		return
	}
	if len(in.APIKeyIDs) == 0 || len(in.APIKeyIDs) > 100 {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "api_key_ids must contain between 1 and 100 keys", reqID)
		return
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(in.APIKeyIDs))
	invalid := make([]model.AuthorizationResult, 0)
	for _, raw := range in.APIKeyIDs {
		id := strings.TrimSpace(raw)
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err := uuid.Parse(id); err != nil {
			// The contract is per-item: one malformed id does not abort the
			// grants that are valid.
			invalid = append(invalid, model.AuthorizationResult{APIKeyID: id, Outcome: "not_found"})
			continue
		}
		ids = append(ids, id)
	}
	alias, results, err := s.pg.SetModelAuthorization(r.Context(), modelID, ids, *in.Allow)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to update model authorizations", reqID)
		return
	}
	results = append(results, invalid...)
	writeJSON(w, http.StatusOK, map[string]any{"alias": alias, "results": results})
}
