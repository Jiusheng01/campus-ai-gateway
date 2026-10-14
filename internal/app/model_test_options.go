package app

import (
	"net/http"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
)

// modelTestOptionView is the narrow projection of a model that the connectivity
// test actually needs.
//
// The test picks a model and hands its alias to ThinkWatch, so this carries the
// identifier, the alias and whether the model is enabled — the three fields the
// drawer reads — and nothing else.
//
// It exists because "run a connectivity test" and "administer model routes" are
// different capabilities. An operator may do the first and not the second, and
// the alternative to this endpoint was to hand them the whole model
// configuration, which would also expose the upstream model name and the
// supplier's identity to a role that has no business editing either. A
// dedicated view also means the response cannot drift into leaking a new field
// the day ModelConfig grows one: nothing is serialised implicitly here.
type modelTestOptionView struct {
	ID     string `json:"id"`
	Alias  string `json:"alias"`
	Status int    `json:"status"`
}

// handleModelTestOptions serves GET /admin/model-test-options.
//
// Reaching this handler is already restricted to super_admin, gateway_admin and
// operator by the RBAC allow-list in adminPrincipal.
func (s *Server) handleModelTestOptions(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	items, err := s.pg.ListModels(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list models", reqID)
		return
	}

	views := make([]modelTestOptionView, 0, len(items))
	for _, m := range items {
		views = append(views, modelTestOptionView{ID: m.ID, Alias: m.Alias, Status: m.Status})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": views})
}
