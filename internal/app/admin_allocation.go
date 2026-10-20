package app

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/google/uuid"
)

// Allocation endpoints.
//
// Two shapes of the same question live here:
//
//   - /admin/tenants/{id}/allocation writes a tenant's own budget: the six
//     three-state limits plus the model allowlist. It is a full replacement, so
//     an omitted field means "inherit".
//   - /admin/model-quotas writes one (subject, model) cap. The subject is a
//     tenant or the campus-wide pool.
//
// Both are validated here rather than in storage: a negative limit is a request
// error, and storage should not be deciding request semantics.

// maxModelAliasLength bounds a model alias. The column is VARCHAR(128); checking
// here means an over-long value is a 400 that names the field rather than a
// storage error.
const maxModelAliasLength = 128

// validateTenantAllocation checks a budget replacement.
//
// Three states, and the validator must not collapse any of them: nil adds no cap
// at this level (caps above it still apply), 0 means explicitly unlimited, a
// positive value is the cap. Only negative numbers are wrong, and the message
// names the field so an operator with seven numbers in front of them knows which
// one.
func validateTenantAllocation(a *model.TenantAllocation) string {
	for _, limit := range []struct {
		field string
		value *int64
	}{
		{"daily_token_limit", a.DailyTokenLimit},
		{"monthly_token_limit", a.MonthlyTokenLimit},
		{"monthly_request_limit", a.MonthlyRequestLimit},
		{"monthly_budget_micros", a.MonthlyBudgetMicros},
		{"rpm_limit", a.RPMLimit},
		{"tpm_limit", a.TPMLimit},
		{"max_concurrency", a.MaxConcurrency},
	} {
		if limit.value != nil && *limit.value < 0 {
			return limit.field + " cannot be negative"
		}
	}
	// nil stays nil (inherit), an explicit empty list stays empty (deny every
	// model). Normalising trims and de-duplicates without touching either.
	a.AllowedModels = model.NormalizeModelList(a.AllowedModels)
	for _, alias := range a.AllowedModels {
		if len(alias) > maxModelAliasLength {
			return "allowed_models contains an alias longer than " + strconv.Itoa(maxModelAliasLength)
		}
	}
	return ""
}

// validateModelQuotaAllocation checks one (subject, model) cap.
//
// The subject is deliberately not defaulted: a nil tenant_id means the campus
// pool, and guessing it would turn "this department" into "everyone".
func validateModelQuotaAllocation(a *model.ModelQuotaAllocation) string {
	alias := strings.TrimSpace(a.ModelAlias)
	if alias == "" {
		return "model_alias is required"
	}
	if len(alias) > maxModelAliasLength {
		return "model_alias is longer than " + strconv.Itoa(maxModelAliasLength)
	}
	a.ModelAlias = alias

	if a.TenantID != nil {
		trimmed := strings.TrimSpace(*a.TenantID)
		if trimmed == "" {
			// An empty string is how a client says "the campus pool". It has to
			// become nil explicitly, because the storage layer distinguishes
			// NULL from a real id and a stray "" would fail the uuid cast.
			a.TenantID = nil
		} else {
			if _, err := uuid.Parse(trimmed); err != nil {
				return "tenant_id is not a valid uuid"
			}
			a.TenantID = &trimmed
		}
	}

	if a.DailyTokenLimit != nil && *a.DailyTokenLimit < 0 {
		return "daily_token_limit cannot be negative"
	}
	if a.MonthlyTokenLimit != nil && *a.MonthlyTokenLimit < 0 {
		return "monthly_token_limit cannot be negative"
	}
	// A row with both limits left to inherit caps nothing, yet it would sit in
	// the table looking like a configured quota. Deleting the row is how a
	// subject goes back to inheriting, so an empty write has no meaning to
	// express; rejecting it keeps the table honest.
	if a.DailyTokenLimit == nil && a.MonthlyTokenLimit == nil {
		return "set at least one limit; a row that inherits both caps nothing (delete it to inherit)"
	}
	return ""
}

// --- handlers --------------------------------------------------------------

// handleTenantAllocation serves PUT /admin/tenants/{id}/allocation.
func (s *Server) handleTenantAllocation(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/tenants/"), "/allocation")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	// Budgets are the one edit a scoped administrator may not make to their own
	// tenant: the ceiling above them was set by someone else, and raising your
	// own limit is what makes a hierarchy decorative. Descendants are fair game.
	if !s.requireTenantWritable(w, r, id, reqID) {
		return
	}

	var in model.TenantAllocation
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	if msg := validateTenantAllocation(&in); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_allocation", msg, reqID)
		return
	}

	out, err := s.pg.AllocateTenantQuota(r.Context(), id, in)
	if err != nil {
		if errors.Is(err, storage.ErrTenantNotFound) {
			httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found", reqID)
			return
		}
		// The client gets a generic message, but the operator needs the reason:
		// a rejected constraint and an unreachable database look identical from
		// outside, and only one of them is worth retrying.
		log.Printf("tenant allocation: %s failed: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Allocation could not be written", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleModelQuotas serves /admin/model-quotas.
//
//	GET    list every cap the caller may see
//	PUT    replace one (subject, model) cap
//	DELETE remove one row, which is how a subject goes back to inheriting
func (s *Server) handleModelQuotas(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	switch r.Method {
	case http.MethodGet:
		s.listModelQuotas(w, r, reqID)
	case http.MethodPut:
		s.putModelQuota(w, r, reqID)
	case http.MethodDelete:
		s.deleteModelQuota(w, r, reqID)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) listModelQuotas(w http.ResponseWriter, r *http.Request, reqID string) {
	var scope *string
	if v := adminScopeTenantID(r); v != "" {
		scope = &v
	}
	items, err := s.pg.ListModelQuotas(r.Context(), scope)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list model quotas", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) putModelQuota(w http.ResponseWriter, r *http.Request, reqID string) {
	var in model.ModelQuotaAllocation
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	if msg := validateModelQuotaAllocation(&in); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_model_quota", msg, reqID)
		return
	}
	if !s.modelQuotaWritable(w, r, in.TenantID, reqID) {
		return
	}

	out, err := s.pg.UpsertModelQuota(r.Context(), in)
	if err != nil {
		if errors.Is(err, storage.ErrTenantNotFound) {
			httpx.Error(w, http.StatusBadRequest, "tenant_not_found", "The referenced tenant does not exist", reqID)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Model quota could not be written", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteModelQuota(w http.ResponseWriter, r *http.Request, reqID string) {
	q := r.URL.Query()
	alias := strings.TrimSpace(q.Get("model_alias"))
	if alias == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_model_quota", "model_alias is required", reqID)
		return
	}
	var tenantID *string
	if raw := strings.TrimSpace(q.Get("tenant_id")); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_model_quota", "tenant_id is not a valid uuid", reqID)
			return
		}
		tenantID = &raw
	}
	if !s.modelQuotaWritable(w, r, tenantID, reqID) {
		return
	}

	removed, err := s.pg.DeleteModelQuota(r.Context(), tenantID, alias)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Model quota could not be removed", reqID)
		return
	}
	// A missing row is not a failure — it is already in the state the caller
	// asked for — but reporting it lets the UI say "there was nothing to
	// remove" instead of claiming a change that never happened.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": removed, "model_alias": alias})
}

// modelQuotaSubjectReachable reports whether a model-quota subject can belong to
// the caller's writable scope at all.
//
// A nil tenant is the campus-wide pool, which belongs to no subtree, so only a
// global administrator may write it. The scope tenant itself is excluded too:
// its row is a ceiling someone above set, and replacing or deleting that row
// would raise the effective value back to whatever it inherits. Strict
// descendants are writable, which is what delegating a department's model
// budgets means.
//
// The database-backed subtree test happens after this; what this pins down are
// the two subjects an id comparison can already reject.
func modelQuotaSubjectReachable(scope string, tenantID *string) bool {
	if scope == "" {
		return true
	}
	return tenantID != nil && *tenantID != scope
}

// modelQuotaWritable enforces the scope on a model-quota write.
//
// The campus pool belongs to nobody's subtree, and an administrator's own
// tenant holds the ceiling granted by their parent. Both are out of reach; every
// other row must still pass the subtree check, which is the same test the
// tenant list and the allocation endpoint use.
func (s *Server) modelQuotaWritable(w http.ResponseWriter, r *http.Request, tenantID *string, reqID string) bool {
	scope := adminScopeTenantID(r)
	if !modelQuotaSubjectReachable(scope, tenantID) {
		writeAdminError(w, reqID, ErrAdminTenantOutOfScope)
		return false
	}
	if scope == "" {
		return true
	}
	return s.requireTenantWritable(w, r, *tenantID, reqID)
}
