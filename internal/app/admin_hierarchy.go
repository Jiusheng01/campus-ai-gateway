package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/google/uuid"
)

// --- scope checks ----------------------------------------------------------

// scopeAllowsTenant answers "may this principal act on that tenant?".
//
// A principal without a scope is global and may act anywhere. A scoped one is
// confined to its subtree, and includeSelf decides whether its own node counts
// as part of it. Reads pass true (seeing your own department is the point of
// being its administrator); writes pass false, so a scoped administrator cannot
// rewrite the budget or identity of the level it is confined to — that level
// belongs to whoever scoped them.
func (s *Server) scopeAllowsTenant(ctx context.Context, p adminPrincipal, tenantID string, includeSelf bool) (bool, error) {
	if p.ScopeTenantID == "" {
		return true, nil
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return false, nil
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return false, nil
	}
	return s.pg.TenantIsDescendant(ctx, p.ScopeTenantID, tenantID, includeSelf)
}

// requireTenantScope writes the refusal itself and reports whether the caller
// may continue. It exists so a handler cannot forget to answer a failed check:
// returning ok without writing a status would leave the client hanging.
func (s *Server) requireTenantScope(w http.ResponseWriter, r *http.Request, p adminPrincipal, tenantID string, includeSelf bool, reqID string) bool {
	ok, err := s.scopeAllowsTenant(r.Context(), p, tenantID, includeSelf)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to resolve tenant scope", reqID)
		return false
	}
	if !ok {
		// Not 404: the tenant may well exist, and the answer is about the
		// caller's reach rather than the target's existence.
		writeAdminError(w, reqID, ErrAdminForbidden)
		return false
	}
	return true
}

// callerScope returns the authenticated principal's scope. Handlers that build
// a storage filter need it; a missing principal is treated as global only
// because the middleware refuses such a request before any handler runs.
func callerScope(r *http.Request) string {
	if p, ok := adminPrincipalFrom(r.Context()); ok {
		return p.ScopeTenantID
	}
	return ""
}

// --- tenant allocation -----------------------------------------------------

// handleTenantAllocation serves POST /admin/tenants/{id}/allocation.
//
// Allocation is a full replacement of the six enforced limits: an omitted field
// means "inherit", which is the same thing as an explicit null. It is separate
// from the identity edit so a rename can never wipe a budget — the two are
// different decisions and deserve different endpoints.
func (s *Server) handleTenantAllocation(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/tenants/"), "/allocation")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	// The principal is attached by the admin middleware for every /admin path.
	// A zero value here means an unscoped caller, which is how these handlers
	// behaved before scopes existed; they are not reachable without the
	// middleware.
	p, _ := adminPrincipalFrom(r.Context())
	if !s.requireTenantScope(w, r, p, id, false, reqID) {
		return
	}

	var in model.TenantAllocation
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	if reason := validateAllocation(&in); reason != "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_allocation", reason, reqID)
		return
	}

	out, err := s.pg.AllocateTenantQuota(r.Context(), id, in)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found", reqID)
		return
	}
	effective, err := s.pg.EffectiveTenantLimits(r.Context(), id)
	if err != nil {
		// The write committed; reporting it as failed would invite a retry that
		// replaces the budget again. The allocation is returned with an empty
		// effective view and the client can re-read it.
		effective = model.EffectiveLimits{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenant": out, "effective": effective})
}

// validateAllocation rejects negative limits. A nil is "inherit" and always
// valid; a zero is the explicit "unlimited" decision and must survive.
func validateAllocation(a *model.TenantAllocation) string {
	limits := []struct {
		name  string
		value *int64
	}{
		{"daily_token_limit", a.DailyTokenLimit},
		{"monthly_token_limit", a.MonthlyTokenLimit},
		{"monthly_request_limit", a.MonthlyRequestLimit},
		{"monthly_budget_micros", a.MonthlyBudgetMicros},
		{"rpm_limit", a.RPMLimit},
		{"tpm_limit", a.TPMLimit},
		{"max_concurrency", a.MaxConcurrency},
	}
	for _, l := range limits {
		if l.value != nil && *l.value < 0 {
			return l.name + " cannot be negative"
		}
	}
	return ""
}

// --- tenant move -----------------------------------------------------------

type tenantMoveRequest struct {
	// ParentID nil means "make this a root". The body is required so the
	// intent is explicit rather than a missing field.
	ParentID *string `json:"parent_id"`
}

// handleTenantMove serves POST /admin/tenants/{id}/move.
func (s *Server) handleTenantMove(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/tenants/"), "/move")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	// The principal is attached by the admin middleware for every /admin path.
	// A zero value here means an unscoped caller, which is how these handlers
	// behaved before scopes existed; they are not reachable without the
	// middleware.
	p, _ := adminPrincipalFrom(r.Context())
	// Re-parenting the scope node itself would move the subtree out from under
	// the administrator confined to it, so it is a write on the node and
	// follows the same exclude-self rule as the other writes.
	if !s.requireTenantScope(w, r, p, id, false, reqID) {
		return
	}

	var in tenantMoveRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	var parentID *string
	if in.ParentID != nil {
		trimmed := strings.TrimSpace(*in.ParentID)
		if trimmed != "" {
			if _, err := uuid.Parse(trimmed); err != nil {
				httpx.Error(w, http.StatusBadRequest, "invalid_parent", "parent_id must be a tenant id", reqID)
				return
			}
			parentID = &trimmed
		}
	}
	// A scoped administrator may only move a node to a parent it administers,
	// and may not detach it into a root: a root would sit outside every scope.
	if p.ScopeTenantID != "" {
		if parentID == nil {
			writeAdminError(w, reqID, ErrAdminForbidden)
			return
		}
		if !s.requireTenantScope(w, r, p, *parentID, true, reqID) {
			return
		}
	}

	out, err := s.pg.MoveTenant(r.Context(), id, parentID)
	if err != nil {
		writeTenantWriteError(w, reqID, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// --- effective model quotas for one tenant ---------------------------------

// handleTenantModelQuotas serves GET /admin/tenants/{id}/model-quotas.
//
// It reports the effective value per model together with the level that set it,
// which is what makes "I allocated 300, why does it stop at 100?" answerable in
// the console instead of a database client.
func (s *Server) handleTenantModelQuotas(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/tenants/"), "/model-quotas")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	// The principal is attached by the admin middleware for every /admin path.
	// A zero value here means an unscoped caller, which is how these handlers
	// behaved before scopes existed; they are not reachable without the
	// middleware.
	p, _ := adminPrincipalFrom(r.Context())
	if !s.requireTenantScope(w, r, p, id, true, reqID) {
		return
	}

	chain, err := s.pg.FindTenantChain(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found", reqID)
		return
	}
	effective, err := s.pg.EffectiveModelQuotas(r.Context(), storage.ResolveQuotas(chain))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load model quotas", reqID)
		return
	}
	if effective == nil {
		effective = []model.EffectiveModelQuota{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":    chain[len(chain)-1],
		"effective": effective,
	})
}

// --- model quota administration --------------------------------------------

// modelQuotaView adds the value the limiter will actually apply to a configured
// row. The configured numbers alone cannot answer "I set 300 here, why does it
// stop at 100?" — that needs the chain resolved and the level named.
type modelQuotaView struct {
	model.ModelQuota
	Effective *model.EffectiveModelQuota `json:"effective,omitempty"`
}

// handleModelQuotas serves the campus-wide model-pool table.
//
// GET    /admin/model-quotas[?tenant_id=...]
// PUT    /admin/model-quotas          (upsert one subject/model pair)
// DELETE /admin/model-quotas?tenant_id=...&model_alias=...
//
// A nil tenant_id means the campus-wide pool. That pool is above every tenant,
// so only a global principal may write it; a scoped administrator sees it (it
// is the ceiling their numbers inherit from) but cannot edit it.
func (s *Server) handleModelQuotas(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	// The principal is attached by the admin middleware for every /admin path.
	// A zero value here means an unscoped caller, which is how these handlers
	// behaved before scopes existed; they are not reachable without the
	// middleware.
	p, _ := adminPrincipalFrom(r.Context())

	switch r.Method {
	case http.MethodGet:
		var scope *string
		if p.ScopeTenantID != "" {
			scope = &p.ScopeTenantID
		}
		items, err := s.pg.ListModelQuotas(r.Context(), scope)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list model quotas", reqID)
			return
		}
		if tenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id")); tenantID != "" {
			filtered := items[:0]
			for _, q := range items {
				if q.TenantID != nil && *q.TenantID == tenantID {
					filtered = append(filtered, q)
				}
			}
			items = filtered
		}
		// Only the subjects on this page are resolved. Reporting the configured
		// number without the one the limiter applies is how "I set 300 here, why
		// does it stop at 100?" becomes unanswerable from the console.
		ids := make([]string, 0, len(items))
		for _, q := range items {
			if q.TenantID != nil {
				ids = append(ids, *q.TenantID)
			}
		}
		byTenant, err := s.pg.EffectiveModelQuotasForTenants(r.Context(), ids)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to resolve effective model quotas", reqID)
			return
		}
		effective := make(map[string]map[string]model.EffectiveModelQuota, len(byTenant))
		for tenantID, list := range byTenant {
			byAlias := make(map[string]model.EffectiveModelQuota, len(list))
			for _, e := range list {
				byAlias[e.ModelAlias] = e
			}
			effective[tenantID] = byAlias
		}
		views := make([]modelQuotaView, 0, len(items))
		for _, q := range items {
			v := modelQuotaView{ModelQuota: q}
			if q.TenantID != nil {
				if e, ok := effective[*q.TenantID][q.ModelAlias]; ok {
					eff := e
					v.Effective = &eff
				}
			}
			views = append(views, v)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": views})
	case http.MethodPut:
		s.upsertModelQuota(w, r, p, reqID)
	case http.MethodDelete:
		s.deleteModelQuota(w, r, p, reqID)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) upsertModelQuota(w http.ResponseWriter, r *http.Request, p adminPrincipal, reqID string) {
	var in model.ModelQuotaAllocation
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	in.ModelAlias = strings.TrimSpace(in.ModelAlias)
	if in.ModelAlias == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_model_quota", "model_alias is required", reqID)
		return
	}
	for _, v := range []*int64{in.DailyTokenLimit, in.MonthlyTokenLimit} {
		if v != nil && *v < 0 {
			httpx.Error(w, http.StatusBadRequest, "invalid_model_quota", "quota values cannot be negative", reqID)
			return
		}
	}
	if !s.requireModelQuotaScope(w, r, p, in.TenantID, reqID) {
		return
	}
	// A quota names a model alias; without this check a typo would create a cap
	// no request can ever match, and the operator would think the model is
	// limited.
	exists, err := s.pg.ModelAliasExists(r.Context(), in.ModelAlias)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to check model", reqID)
		return
	}
	if !exists {
		httpx.Error(w, http.StatusBadRequest, "unknown_model", "No model with that alias exists", reqID)
		return
	}
	out, err := s.pg.UpsertModelQuota(r.Context(), in)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "model_quota_write_failed", "Model quota could not be written", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteModelQuota(w http.ResponseWriter, r *http.Request, p adminPrincipal, reqID string) {
	q := r.URL.Query()
	alias := strings.TrimSpace(q.Get("model_alias"))
	if alias == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_model_quota", "model_alias is required", reqID)
		return
	}
	var tenantID *string
	if raw := strings.TrimSpace(q.Get("tenant_id")); raw != "" {
		tenantID = &raw
	}
	if !s.requireModelQuotaScope(w, r, p, tenantID, reqID) {
		return
	}
	removed, err := s.pg.DeleteModelQuota(r.Context(), tenantID, alias)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Model quota could not be deleted", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": removed})
}

// requireModelQuotaScope applies the two rules the model-pool table has: the
// campus-wide pool is global-only, and a tenant row must sit inside the
// caller's subtree and below the caller's own node (raising your own ceiling is
// not something a confined administrator may do).
func (s *Server) requireModelQuotaScope(w http.ResponseWriter, r *http.Request, p adminPrincipal, tenantID *string, reqID string) bool {
	trimmed := ""
	if tenantID != nil {
		trimmed = strings.TrimSpace(*tenantID)
	}
	if p.ScopeTenantID == "" {
		return true
	}
	if trimmed == "" {
		writeAdminError(w, reqID, ErrAdminForbidden)
		return false
	}
	if _, err := uuid.Parse(trimmed); err != nil {
		writeAdminError(w, reqID, ErrAdminForbidden)
		return false
	}
	return s.requireTenantScope(w, r, p, trimmed, false, reqID)
}

// writeTenantWriteError maps the storage refusals shared by move and delete.
// The two "still in use" cases are separate codes because the operator's next
// step differs: remove the children, or reassign the administrator.
func writeTenantWriteError(w http.ResponseWriter, reqID string, err error) {
	switch {
	case errors.Is(err, storage.ErrTenantHasChildren):
		httpx.Error(w, http.StatusConflict, "tenant_has_children", "Tenant still has child tenants", reqID)
	case errors.Is(err, storage.ErrTenantInUse):
		httpx.Error(w, http.StatusConflict, "tenant_in_use", "Tenant is still the scope of an administrator", reqID)
	case errors.Is(err, storage.ErrTenantCycle):
		httpx.Error(w, http.StatusConflict, "tenant_cycle", "A tenant cannot be moved inside its own subtree", reqID)
	case errors.Is(err, storage.ErrTenantParentNotFound):
		httpx.Error(w, http.StatusBadRequest, "tenant_parent_not_found", "The target parent does not exist", reqID)
	case errors.Is(err, storage.ErrTenantCodeTaken):
		httpx.Error(w, http.StatusConflict, "tenant_code_taken", "Tenant code is already in use", reqID)
	case errors.Is(err, storage.ErrTenantNotFound):
		httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found", reqID)
	default:
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Tenant could not be updated", reqID)
	}
}
