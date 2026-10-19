package app

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

type adminAuditResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *adminAuditResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *adminAuditResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

// adminMiddleware authenticates the caller, enforces RBAC and records an audit
// row for state-changing requests.
//
// The error classification is driven by sentinel errors via adminErrorStatus.
// Nothing here inspects error text, so an authentication failure can no longer
// be misreported as "service unavailable".
func (s *Server) adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")
		principal, err := s.authenticateAdmin(r)
		if err != nil {
			if errors.Is(err, ErrAdminInternal) {
				log.Printf("admin auth: internal failure on %s: %v", r.URL.Path, err)
			}
			writeAdminError(w, reqID, err)
			return
		}
		if !principal.can(r) {
			// A refused privileged request is exactly the kind of event an audit
			// log exists for -- someone with valid credentials reaching for
			// something they do not hold. Leaving it untraced while tracing the
			// finer-grained allocation denials would be backwards, so the
			// refusal is recorded here rather than only returned.
			s.recordAdminForbidden(r, principal)
			writeAdminError(w, reqID, ErrAdminForbidden)
			return
		}
		r = r.WithContext(withAdminPrincipal(r.Context(), principal))

		if r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			aw := &adminAuditResponseWriter{ResponseWriter: w}
			next.ServeHTTP(aw, r)
			status := aw.status
			if status == 0 {
				status = http.StatusOK
			}
			if s.pg == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = s.pg.InsertAdminAudit(ctx, model.AdminAuditLog{
				RequestID:  reqID,
				Actor:      principal.User,
				Action:     r.Method,
				Resource:   r.URL.Path,
				StatusCode: status,
				ClientIP:   s.clientIPFrom(r),
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// recordAdminForbidden audits an RBAC refusal. Best effort, like every other
// audit write: a failure to record must never turn a 403 into a 500, and must
// never let the request through.
//
// The roles and the scope go into the resource string because AdminAuditLog has
// no free-form detail column; "which roles did the refused account hold" is the
// question an operator asks first when reading these rows.
func (s *Server) recordAdminForbidden(r *http.Request, p adminPrincipal) {
	roles := make([]string, 0, len(p.Roles))
	for role := range p.Roles {
		roles = append(roles, role)
	}
	sort.Strings(roles)

	resource := r.URL.Path + " roles=" + strings.Join(roles, ",")
	if p.ScopeTenantID != nil {
		resource += " scope_tenant_id=" + *p.ScopeTenantID
	}
	s.auditAdminAuthResource(r, "admin_forbidden:"+r.Method, resource, http.StatusForbidden, p.User)
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/admin/status":
		s.handleAdminStatus(w, r)
	case r.URL.Path == "/admin/whoami":
		s.handleAdminWhoAmI(w, r)
	case r.URL.Path == "/admin/providers":
		s.handleProviders(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/providers/"):
		s.handleProviderByID(w, r)
	case r.URL.Path == "/admin/models":
		s.handleModels(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/models/") && strings.HasSuffix(r.URL.Path, "/routes"):
		s.handleModelRoutes(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/model-routes/"):
		s.handleModelRouteByID(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/models/"):
		s.handleModelByID(w, r)
	case r.URL.Path == "/admin/tenants":
		s.handleTenants(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/tenants/") && strings.HasSuffix(r.URL.Path, "/usage"):
		s.handleTenantUsage(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/tenants/") && strings.HasSuffix(r.URL.Path, "/move"):
		s.handleTenantMove(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/tenants/") && strings.HasSuffix(r.URL.Path, "/quota-allocations"):
		s.handleTenantAllocation(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/tenants/"):
		s.handleTenantByID(w, r)
	case r.URL.Path == "/admin/model-quotas":
		s.handleModelQuotas(w, r)
	case r.URL.Path == "/admin/api-keys":
		s.handleAPIKeys(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/api-keys/"):
		s.handleAPIKeyByID(w, r)
	case r.URL.Path == "/admin/request-logs":
		s.handleRequestLogs(w, r)
	case r.URL.Path == "/admin/request-logs/export":
		s.handleRequestLogsExport(w, r)
	case r.URL.Path == "/admin/admin-audit":
		s.handleAdminAuditLogs(w, r)
	case r.URL.Path == "/admin/security-events":
		s.handleSecurityEvents(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleAdminWhoAmI(w http.ResponseWriter, r *http.Request) {
	writeAuthNoStore(w)
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	p, ok := adminPrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid_admin_auth", "Admin identity is unavailable", reqID)
		return
	}
	roles := make([]string, 0, len(p.Roles))
	for role := range p.Roles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":       p.User,
		"username":   p.Username,
		"user_id":    p.UserID,
		"session_id": p.SessionID,
		"roles":      roles,
		"auth_mode":  p.AuthMode,
	})
}

func (s *Server) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	dbOK := s.pg.Pool.Ping(r.Context()) == nil
	redisOK := s.redis.Client.Ping(r.Context()).Err() == nil
	stats, err := s.pg.DashboardStats(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load dashboard stats", reqID)
		return
	}
	providerStats, err := s.pg.ProviderStats(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load provider stats", reqID)
		return
	}

	thinkwatchOK := false
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(s.cfg.ThinkWatchURL, "/")+"/v1/models", nil)
	if err == nil {
		if s.cfg.ThinkWatchKey != "" {
			req.Header.Set("Authorization", "Bearer "+s.cfg.ThinkWatchKey)
		}
		if resp, err := client.Do(req); err == nil {
			thinkwatchOK = resp.StatusCode >= 200 && resp.StatusCode < 500
			_ = resp.Body.Close()
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"gateway":        true,
		"postgres":       dbOK,
		"redis":          redisOK,
		"thinkwatch":     thinkwatchOK,
		"stats":          stats,
		"provider_stats": providerStats,
	})
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	switch r.Method {
	case http.MethodGet:
		items, err := s.pg.ListProviders(r.Context())
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list providers", reqID)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items})
	case http.MethodPost:
		var in model.Provider
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
			return
		}
		if err := validateProvider(&in); err != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_provider", err, reqID)
			return
		}
		out, err := s.pg.CreateProvider(r.Context(), in)
		if err != nil {
			httpx.Error(w, http.StatusConflict, "provider_create_failed", "Provider could not be created", reqID)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) handleProviderByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/providers/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	var in model.Provider
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	if err := validateProvider(&in); err != "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_provider", err, reqID)
		return
	}
	out, err := s.pg.UpdateProvider(r.Context(), id, in)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "provider_not_found", "Provider was not found or could not be updated", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	switch r.Method {
	case http.MethodGet:
		items, err := s.pg.ListModels(r.Context())
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list models", reqID)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items})
	case http.MethodPost:
		var in model.ModelConfig
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
			return
		}
		if err := validateModel(&in); err != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_model", err, reqID)
			return
		}
		out, err := s.pg.CreateModel(r.Context(), in)
		if err != nil {
			httpx.Error(w, http.StatusConflict, "model_create_failed", "Model could not be created", reqID)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) handleModelByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/models/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	var in model.ModelConfig
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	if err := validateModel(&in); err != "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_model", err, reqID)
		return
	}
	out, err := s.pg.UpdateModel(r.Context(), id, in)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found or could not be updated", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	p, ok := adminPrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid_admin_auth", "Admin identity is unavailable", reqID)
		return
	}

	switch r.Method {
	case http.MethodGet:
		items, err := s.listTenantsFor(r, p)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list tenants", reqID)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items})
	case http.MethodPost:
		var in model.Tenant
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
			return
		}
		if err := validateTenant(&in); err != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_tenant", err, reqID)
			return
		}

		// A new node must land inside the caller's scope. The check is on the
		// parent, not on the tenant being created -- the child does not exist
		// yet, and its position is what determines whose subtree it joins.
		parentID := model.NormalizeOptionalID(in.ParentID)
		if parentID == nil {
			if p.ScopeTenantID != nil {
				httpx.Error(w, http.StatusForbidden, "tenant_out_of_scope", "Only an unscoped administrator can create a root tenant", reqID)
				return
			}
		} else {
			allowed, err := s.tenantWritable(r.Context(), p, *parentID)
			if err != nil {
				httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to resolve tenant scope", reqID)
				return
			}
			if !allowed {
				httpx.Error(w, http.StatusForbidden, "tenant_out_of_scope", "The parent tenant is outside your scope", reqID)
				return
			}
		}
		in.ParentID = parentID

		out, err := s.pg.CreateTenant(r.Context(), in)
		switch {
		case errors.Is(err, storage.ErrTenantParentNotFound):
			httpx.Error(w, http.StatusBadRequest, "parent_tenant_not_found", "The parent tenant does not exist", reqID)
			return
		case err != nil:
			httpx.Error(w, http.StatusConflict, "tenant_create_failed", "Tenant could not be created", reqID)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

// listTenantsFor returns the tenants the principal may see. A scoped
// administrator gets their own subtree rather than the whole tree, so the list
// matches what they can act on instead of offering rows that would fail on
// write.
func (s *Server) listTenantsFor(r *http.Request, p adminPrincipal) ([]model.Tenant, error) {
	if p.ScopeTenantID == nil {
		return s.pg.ListTenants(r.Context())
	}
	return s.pg.ListTenantsInSubtree(r.Context(), *p.ScopeTenantID)
}

func (s *Server) handleTenantUsage(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/tenants/"), "/usage")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	p, ok := adminPrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid_admin_auth", "Admin identity is unavailable", reqID)
		return
	}
	if allowed, err := s.tenantReadable(r.Context(), p, id); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to resolve tenant scope", reqID)
		return
	} else if !allowed {
		httpx.Error(w, http.StatusForbidden, "tenant_out_of_scope", "This tenant is outside your scope", reqID)
		return
	}
	stats, err := s.pg.TenantUsageStats(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant usage could not be loaded", reqID)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleTenantByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/tenants/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	p, ok := adminPrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid_admin_auth", "Admin identity is unavailable", reqID)
		return
	}
	if allowed, err := s.tenantWritable(r.Context(), p, id); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to resolve tenant scope", reqID)
		return
	} else if !allowed {
		httpx.Error(w, http.StatusForbidden, "tenant_out_of_scope", "This tenant is outside your scope", reqID)
		return
	}
	var in model.Tenant
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	if err := validateTenant(&in); err != "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_tenant", err, reqID)
		return
	}
	out, err := s.pg.UpdateTenant(r.Context(), id, in)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found or could not be updated", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleTenantMove reparents a tenant. The subtree moves with it: every
// descendant's materialized path is rewritten in the same statement.
//
// A null (or absent) parent_id promotes the tenant to a root.
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
	p, ok := adminPrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid_admin_auth", "Admin identity is unavailable", reqID)
		return
	}
	if allowed, err := s.tenantWritable(r.Context(), p, id); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to resolve tenant scope", reqID)
		return
	} else if !allowed {
		httpx.Error(w, http.StatusForbidden, "tenant_out_of_scope", "This tenant is outside your scope", reqID)
		return
	}

	var in struct {
		ParentID *string `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}

	// Moving a tenant out of the caller's subtree is a way of losing control of
	// it, and moving one in is a way of gaining it. Both directions have to be
	// inside the scope.
	newParent := model.NormalizeOptionalID(in.ParentID)
	if newParent == nil {
		if p.ScopeTenantID != nil {
			httpx.Error(w, http.StatusForbidden, "tenant_out_of_scope", "Only an unscoped administrator can promote a tenant to a root", reqID)
			return
		}
	} else if allowed, err := s.tenantWritable(r.Context(), p, *newParent); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to resolve tenant scope", reqID)
		return
	} else if !allowed {
		httpx.Error(w, http.StatusForbidden, "tenant_out_of_scope", "The target parent tenant is outside your scope", reqID)
		return
	}

	out, err := s.pg.MoveTenant(r.Context(), id, newParent)
	switch {
	case errors.Is(err, storage.ErrTenantNotFound):
		httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found", reqID)
		return
	case errors.Is(err, storage.ErrTenantParentNotFound):
		httpx.Error(w, http.StatusBadRequest, "parent_tenant_not_found", "The target parent tenant does not exist", reqID)
		return
	case errors.Is(err, storage.ErrTenantCycle):
		httpx.Error(w, http.StatusConflict, "tenant_cycle", "A tenant cannot be moved under one of its own descendants", reqID)
		return
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Tenant could not be moved", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleTenantAllocation is the delegation endpoint: it sets what one level of
// the hierarchy hands down to another.
//
// This is the operation the whole hierarchy exists for. A school allocates to a
// department, a department to its teachers, a teacher to their students -- and
// each caller is confined to their own subtree, so the chain of delegation is
// enforced by the same scope check that guards every other tenant write.
//
// The body is a full replacement, not a patch: an omitted field means "inherit
// from the nearest ancestor that sets one", which is the same thing as sending
// an explicit null. That matches the storage model, where NULL is inheritance.
func (s *Server) handleTenantAllocation(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/tenants/"), "/quota-allocations")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	p, ok := adminPrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid_admin_auth", "Admin identity is unavailable", reqID)
		return
	}

	// The read path only needs read scope: an administrator may inspect their
	// own node even though they may not raise its budget.
	if r.Method == http.MethodGet {
		if allowed, err := s.tenantReadable(r.Context(), p, id); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to resolve tenant scope", reqID)
			return
		} else if !allowed {
			httpx.Error(w, http.StatusForbidden, "tenant_out_of_scope", "This tenant is outside your scope", reqID)
			return
		}
		chain, err := s.pg.FindTenantChain(r.Context(), id)
		switch {
		case errors.Is(err, storage.ErrTenantNotFound):
			httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found", reqID)
			return
		case err != nil:
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the tenant hierarchy", reqID)
			return
		}
		// The model axis is reported alongside the tenant one: an operator
		// looking at a tenant's allocation needs to see, in the same view, both
		// how many tokens it holds and how much of each model it may spend.
		effectiveModels, err := s.pg.EffectiveModelQuotas(r.Context(), storage.ResolveQuotas(chain))
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to resolve the model quotas", reqID)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tenant":           chain[len(chain)-1],
			"effective":        storage.ResolveEffectiveLimits(chain),
			"effective_models": effectiveModels,
		})
		return
	}

	var in model.TenantAllocation
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	normalizeAllocation(&in)

	// 1) May this caller write here at all? The scope check comes first so an
	// out-of-scope tenant is never even loaded, let alone probed for its
	// remaining budget.
	if allowed, err := s.tenantWritable(r.Context(), p, id); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to resolve tenant scope", reqID)
		return
	} else if !allowed {
		s.auditAdminAuthResource(r, "tenant_quota_allocated_denied", "tenant:"+id, http.StatusForbidden, p.Username)
		httpx.Error(w, http.StatusForbidden, "tenant_out_of_scope", "This tenant is outside your scope", reqID)
		return
	}

	// 2) You cannot hand out more than you hold.
	chain, err := s.pg.FindTenantChain(r.Context(), id)
	switch {
	case errors.Is(err, storage.ErrTenantNotFound):
		httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found", reqID)
		return
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the tenant hierarchy", reqID)
		return
	}
	if msg := validateAllocationAgainstParent(chain, in); msg != "" {
		s.auditAdminAuthResource(r, "tenant_quota_allocated_rejected", "tenant:"+id, http.StatusBadRequest, p.Username)
		httpx.Error(w, http.StatusBadRequest, "allocation_exceeds_parent", msg, reqID)
		return
	}

	out, err := s.pg.AllocateTenantQuota(r.Context(), id, in)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Allocation could not be saved", reqID)
		return
	}

	// Report what the tenant now actually gets, not just what was written. The
	// two differ whenever a field was left null to inherit, and that difference
	// is exactly what an administrator needs to see. The chain is reloaded
	// rather than patched in memory so both axes come from one source.
	effective, err := s.pg.EffectiveTenantLimits(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Allocation was saved but could not be read back", reqID)
		return
	}

	// The model axis is reported alongside the tenant one so the UI can refresh
	// both from a single response after a save.
	effectiveModels, err := s.pg.EffectiveModelQuotas(r.Context(), storage.ResolveQuotas(chain))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Allocation was saved but the model quotas could not be read back", reqID)
		return
	}

	s.auditAdminAuthResource(r, "tenant_quota_allocated", "tenant:"+id, http.StatusOK, p.Username)
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":           out,
		"effective":        effective,
		"effective_models": effectiveModels,
	})
}

// normalizeModelList trims and de-duplicates a model allowlist so that a list
// like ["gpt-4o", " gpt-4o "] does not become a two-element set that fails to
// match. A nil list stays nil: that is "inherit", and it must not be confused
// with the empty list, which is "deny every model".
func normalizeModelList(models []string) []string {
	if models == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	return out
}

// normalizeAllocation applies the allowlist rule to a tenant allocation.
func normalizeAllocation(a *model.TenantAllocation) {
	a.AllowedModels = normalizeModelList(a.AllowedModels)
}

// validateAllocationAgainstParent enforces the one rule that keeps the
// hierarchy coherent: a level may not hold more than the level above it.
//
// Without it a department could be granted 5,000,000 tokens under a school that
// holds 1,000,000. The limiter walks every level, so the excess would never
// actually be spendable -- but the numbers an operator reads would be wrong,
// and "I allocated it and it does not work" is precisely the kind of silent
// inconsistency that makes people stop trusting a quota system.
func validateAllocationAgainstParent(chain []model.Tenant, a model.TenantAllocation) string {
	if len(chain) < 2 {
		// A root has no parent to defer to; anything it sets is its own cap.
		return ""
	}
	parentChain := chain[:len(chain)-1]
	parentQuotas := storage.ResolveQuotas(parentChain)
	ceiling := parentQuotas[len(parentQuotas)-1]

	dimensions := []struct {
		name      string
		requested *int64
		available int64
	}{
		{"daily_token_limit", a.DailyTokenLimit, ceiling.DailyTokenLimit},
		{"monthly_token_limit", a.MonthlyTokenLimit, ceiling.MonthlyTokenLimit},
		{"monthly_request_limit", a.MonthlyRequestLimit, ceiling.MonthlyRequestLimit},
		{"monthly_budget_micros", a.MonthlyBudgetMicros, ceiling.MonthlyBudgetMicros},
	}
	for _, d := range dimensions {
		if d.requested == nil {
			continue // inheriting can never exceed anything
		}
		if *d.requested < 0 {
			return d.name + " cannot be negative"
		}
		// available == 0 means the parent is uncapped, so there is no ceiling
		// to exceed. This is the same "0 means unlimited" convention the
		// limiter uses.
		if d.available > 0 && *d.requested > d.available {
			return fmt.Sprintf(
				"%s (%d) exceeds what the parent level holds (%d)",
				d.name, *d.requested, d.available,
			)
		}
	}
	if a.SoftTokenLimit != nil && *a.SoftTokenLimit < 0 {
		return "soft_token_limit cannot be negative"
	}

	// Access is the other axis, and it is a subset rule rather than a numeric
	// one: a child may narrow its parent's allowlist but never widen it.
	if a.AllowedModels != nil {
		parentAccess, parentRestricted := storage.ResolveAccess(parentChain)
		if parentRestricted {
			for _, m := range a.AllowedModels {
				if _, ok := parentAccess[m]; !ok {
					return fmt.Sprintf("model %q is not available at the parent level", m)
				}
			}
		}
	}
	return ""
}

// maxQuotaChainKeys bounds how many Redis keys a single request may touch.
//
// The tenant axis costs two keys per level (a daily and a monthly bucket) and
// the model axis costs two more per level plus two for the shared system pool.
// The bound is enforced when a model quota is written, because that is what
// pushes the count up: a hierarchy too deep to afford per-model quotas simply
// cannot have them until it is flattened. The 4-level design this gateway is
// built around (school -> department -> teacher -> student) peaks at 18.
const maxQuotaChainKeys = 24

// modelQuotaChainKeys is the worst-case key count for a chain of the given
// depth, with every level configured for the model being checked.
func modelQuotaChainKeys(depth int) int {
	return 2*depth + 2*(depth+1)
}

func modelQuotaResource(q model.ModelQuota) string {
	if q.TenantID == nil {
		return "model_pool:" + q.ModelAlias
	}
	return "model_quota:" + *q.TenantID + ":" + q.ModelAlias
}

// modelQuotaCheck carries everything validateModelQuotaAllocation needs.
//
// It is a struct rather than a list of positional arguments because two of the
// inputs are int64 ceilings: swapping them at a call site would compile and
// then compare a daily limit against a monthly one.
type modelQuotaCheck struct {
	Allocation model.ModelQuotaAllocation

	// CeilingDaily / CeilingMonthly are the caps already in force for this
	// model one level up. 0 means that dimension has no ceiling.
	CeilingDaily   int64
	CeilingMonthly int64

	// Access is the target tenant's effective model allowlist, and
	// AccessRestricted reports whether an allowlist applies at all.
	Access           map[string]struct{}
	AccessRestricted bool

	// ChainDepth is the number of levels in the target tenant's chain.
	ChainDepth int
}

// systemPool reports whether the row being written is the campus-wide pool.
//
// It is derived from the allocation rather than stored as its own field. A
// separate flag could disagree with the tenant id it is supposed to describe,
// and the two possible disagreements are both silent: a pool treated as a tenant
// would be checked against a ceiling that does not exist, and a tenant treated
// as a pool would skip every rule. Deriving it makes that impossible.
func (c modelQuotaCheck) systemPool() bool {
	return model.NormalizeOptionalID(c.Allocation.TenantID) == nil
}

// validateModelQuotaAllocation enforces the rules that keep the model quota
// hierarchy coherent. It is a pure function so every rule is testable without a
// database, which matters because the interesting cases (an allowlist that
// excludes the model, a ceiling that is inherited rather than set) are awkward
// to arrange through SQL.
func validateModelQuotaAllocation(c modelQuotaCheck) string {
	a := c.Allocation
	alias := strings.TrimSpace(a.ModelAlias)
	switch {
	case alias == "":
		return "model alias is required"
	case len(alias) > 128:
		return "model alias is too long (max 128 characters)"
	}

	for _, d := range []struct {
		name  string
		value *int64
	}{
		{"daily_token_limit", a.DailyTokenLimit},
		{"monthly_token_limit", a.MonthlyTokenLimit},
	} {
		if d.value != nil && *d.value < 0 {
			return d.name + " cannot be negative"
		}
	}

	// Everything below is about where the row sits in the tree, so none of it
	// applies to the campus-wide pool.
	if c.systemPool() {
		return ""
	}

	// A quota for a model the tenant may not call is a number nobody can ever
	// reach. Rejecting it turns a silent no-op into a message.
	if c.AccessRestricted {
		if _, ok := c.Access[alias]; !ok {
			return fmt.Sprintf("model %q is not allowed for this tenant", alias)
		}
	}

	for _, d := range []struct {
		name      string
		requested *int64
		available int64
	}{
		{"daily_token_limit", a.DailyTokenLimit, c.CeilingDaily},
		{"monthly_token_limit", a.MonthlyTokenLimit, c.CeilingMonthly},
	} {
		if d.requested == nil {
			continue // inheriting can never exceed anything
		}
		// available == 0 means the level above is uncapped for this model, so
		// there is nothing to exceed. This is the same "0 means unlimited"
		// convention the tenant axis uses.
		if d.available > 0 && *d.requested > d.available {
			return fmt.Sprintf(
				"%s (%d) exceeds what the parent level holds for model %q (%d)",
				d.name, *d.requested, alias, d.available,
			)
		}
	}

	if keys := modelQuotaChainKeys(c.ChainDepth); keys > maxQuotaChainKeys {
		return fmt.Sprintf(
			"a %d-level hierarchy needs %d quota keys per request, over the %d key budget",
			c.ChainDepth, keys, maxQuotaChainKeys,
		)
	}
	return ""
}

// handleModelQuotas manages the per-model quota axis: one pool per model, held
// either by the whole system (the campus-wide pool, which is the root of the
// model chain) or by a single tenant.
//
// It is a separate endpoint from /quota-allocations rather than a field on it
// because the shapes differ: a tenant allocation is one row of numbers, while
// model quotas are a map of model -> numbers whose key set changes as models
// are added and removed. Folding it in would turn a full-replacement update
// into something that also has to express deletions.
func (s *Server) handleModelQuotas(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	p, ok := adminPrincipalFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid_admin_auth", "Admin identity is unavailable", reqID)
		return
	}

	switch r.Method {
	case http.MethodGet:
		items, err := s.pg.ListModelQuotas(r.Context(), p.ScopeTenantID)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list model quotas", reqID)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items})
	case http.MethodPut:
		s.putModelQuota(w, r, p, reqID)
	case http.MethodDelete:
		s.deleteModelQuota(w, r, p, reqID)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

// modelQuotaWritable applies the scope rule shared by PUT and DELETE. The
// campus-wide pool affects every caller, so writing it stays with a super
// administrator; a department administrator configuring it would be setting a
// budget for the whole university. Tenant-scoped rows follow the same subtree
// rule as every other tenant write.
func (s *Server) modelQuotaWritable(r *http.Request, p adminPrincipal, tenantID *string) (bool, string) {
	if tenantID == nil {
		if !p.Roles["super_admin"] {
			return false, "Only a super administrator can change the system-wide model pool"
		}
		return true, ""
	}
	allowed, err := s.tenantWritable(r.Context(), p, *tenantID)
	if err != nil {
		return false, "scope lookup failed"
	}
	return allowed, "This tenant is outside your scope"
}

func (s *Server) putModelQuota(w http.ResponseWriter, r *http.Request, p adminPrincipal, reqID string) {
	var in model.ModelQuotaAllocation
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	// A nil tenant id means the system-wide pool. Getting this normalisation
	// wrong would silently turn "this tenant" into "everyone", which is why it
	// runs before the writability check rather than inside the storage layer.
	in.TenantID = model.NormalizeOptionalID(in.TenantID)

	if ok, msg := s.modelQuotaWritable(r, p, in.TenantID); !ok {
		s.auditAdminAuthResource(r, "model_quota_denied", modelQuotaResource(model.ModelQuota{TenantID: in.TenantID, ModelAlias: in.ModelAlias}), http.StatusForbidden, p.Username)
		httpx.Error(w, http.StatusForbidden, "model_quota_out_of_scope", msg, reqID)
		return
	}

	check := modelQuotaCheck{Allocation: in}
	var chain []model.TenantQuota

	if in.TenantID != nil {
		full, err := s.pg.FindTenantChain(r.Context(), *in.TenantID)
		switch {
		case errors.Is(err, storage.ErrTenantNotFound):
			httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found", reqID)
			return
		case err != nil:
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the tenant hierarchy", reqID)
			return
		}
		// Access and quota are the two axes and they resolve differently, so
		// both are resolved here from the same chain: access by intersection
		// (may this tenant call the model at all?) and the ceiling by
		// inheritance (how much of it may it hold?).
		check.Access, check.AccessRestricted = storage.ResolveAccess(full)
		chain = storage.ResolveQuotas(full)
		check.ChainDepth = len(chain)

		// The ceiling comes from the levels strictly above the target. Passing
		// the whole chain would compare an existing row against itself the next
		// time it is saved, which makes an edit fail once it has been applied.
		parentChain := chain[:len(chain)-1]
		levels, err := s.pg.ResolveModelQuotaLevels(r.Context(), parentChain, in.ModelAlias)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to resolve the model quota ceiling", reqID)
			return
		}
		if n := len(levels); n > 0 {
			check.CeilingDaily = levels[n-1].DailyTokenLimit
			check.CeilingMonthly = levels[n-1].MonthlyTokenLimit
		}
	}

	if msg := validateModelQuotaAllocation(check); msg != "" {
		s.auditAdminAuthResource(r, "model_quota_rejected", modelQuotaResource(model.ModelQuota{TenantID: in.TenantID, ModelAlias: in.ModelAlias}), http.StatusBadRequest, p.Username)
		httpx.Error(w, http.StatusBadRequest, "invalid_model_quota", msg, reqID)
		return
	}

	out, err := s.pg.UpsertModelQuota(r.Context(), in)
	switch {
	case errors.Is(err, storage.ErrTenantNotFound):
		httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found", reqID)
		return
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Model quota could not be saved", reqID)
		return
	}

	// Report what the target now actually gets, not just the row that was
	// written: a limit left null inherits, and that difference is precisely what
	// the administrator needs to see.
	effective := []model.EffectiveModelQuota{}
	if chain != nil {
		effective, err = s.pg.EffectiveModelQuotas(r.Context(), chain)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Model quota was saved but could not be read back", reqID)
			return
		}
	}

	s.auditAdminAuthResource(r, "model_quota_allocated", modelQuotaResource(out), http.StatusOK, p.Username)
	writeJSON(w, http.StatusOK, map[string]any{"quota": out, "effective": effective})
}

func (s *Server) deleteModelQuota(w http.ResponseWriter, r *http.Request, p adminPrincipal, reqID string) {
	q := r.URL.Query()
	alias := strings.TrimSpace(q.Get("model_alias"))
	if alias == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "model_alias is required", reqID)
		return
	}
	var tenantID *string
	if v := strings.TrimSpace(q.Get("tenant_id")); v != "" {
		tenantID = &v
	}

	if ok, msg := s.modelQuotaWritable(r, p, tenantID); !ok {
		httpx.Error(w, http.StatusForbidden, "model_quota_out_of_scope", msg, reqID)
		return
	}

	removed, err := s.pg.DeleteModelQuota(r.Context(), tenantID, alias)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Model quota could not be removed", reqID)
		return
	}
	s.auditAdminAuthResource(r, "model_quota_deleted", modelQuotaResource(model.ModelQuota{TenantID: tenantID, ModelAlias: alias}), http.StatusOK, p.Username)
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	switch r.Method {
	case http.MethodGet:
		items, err := s.pg.ListAPIKeys(r.Context())
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list API keys", reqID)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items})
	case http.MethodPost:
		var in model.APIKeyAdmin
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
			return
		}
		if err := validateAPIKey(&in); err != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_api_key_config", err, reqID)
			return
		}
		plain, prefix, digest, err := security.NewAPIKey(s.cfg.KeyHMACSecret)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "key_generation_failed", "Unable to generate API key", reqID)
			return
		}
		in.KeyPrefix = prefix
		out, err := s.pg.CreateAPIKey(r.Context(), in, digest)
		if err != nil {
			httpx.Error(w, http.StatusConflict, "api_key_create_failed", "API key could not be created", reqID)
			return
		}
		writeJSON(w, http.StatusCreated, model.APIKeyCreateResult{APIKeyAdmin: out, APIKey: plain})
	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) handleAPIKeyByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPut {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/api-keys/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	var in model.APIKeyAdmin
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	if err := validateAPIKey(&in); err != "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_api_key_config", err, reqID)
		return
	}
	out, err := s.pg.UpdateAPIKey(r.Context(), id, in)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "api_key_not_found", "API key was not found or could not be updated", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminAuditLogs(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	items, err := s.pg.ListAdminAudit(r.Context(), parseAdminLimit(r))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list admin audit logs", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) handleSecurityEvents(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	items, err := s.pg.ListSecurityEvents(r.Context(), parseAdminLimit(r))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list security events", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func parseAdminLimit(r *http.Request) int {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	return limit
}

func requestLogFilterFrom(r *http.Request, defaultLimit int) storage.RequestLogFilter {
	limit := defaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 5000 {
			limit = n
		}
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			offset = n
		}
	}
	status := 0
	if raw := r.URL.Query().Get("status"); raw != "" {
		status, _ = strconv.Atoi(raw)
	}
	failover := -1
	switch r.URL.Query().Get("failover") {
	case "true", "1":
		failover = 1
	case "false", "0":
		failover = 0
	}
	return storage.RequestLogFilter{
		Limit:     limit,
		Offset:    offset,
		Model:     strings.TrimSpace(r.URL.Query().Get("model")),
		Provider:  strings.TrimSpace(r.URL.Query().Get("provider")),
		Status:    status,
		RequestID: strings.TrimSpace(r.URL.Query().Get("request_id")),
		Failover:  failover,
	}
}

func (s *Server) handleRequestLogs(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	f := requestLogFilterFrom(r, 100)
	items, err := s.pg.SearchRequestLogs(r.Context(), f)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list request logs", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "limit": f.Limit, "offset": f.Offset})
}

func (s *Server) handleRequestLogsExport(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	f := requestLogFilterFrom(r, 5000)
	f.Offset = 0
	items, err := s.pg.SearchRequestLogs(r.Context(), f)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to export request logs", reqID)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=campus-gateway-request-logs.csv")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"created_at", "request_id", "tenant_id", "requested_model", "actual_model", "provider", "upstream_model", "route_priority", "attempts", "failover", "tokens", "latency_ms", "status_code", "error_code"})
	for _, v := range items {
		_ = cw.Write([]string{
			v.CreatedAt.Format(time.RFC3339), v.RequestID, v.TenantID, v.RequestedModel, v.ActualModel,
			v.Provider, v.UpstreamModel, strconv.Itoa(v.RoutePriority), strconv.Itoa(v.Attempts),
			strconv.FormatBool(v.Failover), strconv.FormatInt(v.TotalTokens, 10), strconv.FormatInt(v.LatencyMS, 10),
			strconv.Itoa(v.StatusCode), v.ErrorCode,
		})
	}
	cw.Flush()
}

func validateProvider(v *model.Provider) string {
	v.Name = strings.TrimSpace(v.Name)
	v.BaseURL = strings.TrimSpace(v.BaseURL)
	v.Protocol = strings.TrimSpace(v.Protocol)
	v.CredentialEnv = strings.TrimSpace(v.CredentialEnv)
	if v.Name == "" || v.BaseURL == "" || v.CredentialEnv == "" {
		return "name, base_url and credential_env are required"
	}
	switch v.Protocol {
	case "":
		v.Protocol = "openai-chat"
	case "openai-chat", "openai-responses", "anthropic", "gemini":
	default:
		return "unsupported protocol"
	}
	if v.Status == 0 {
		v.Status = 1
	}
	if v.Status != 1 && v.Status != 2 {
		return "status must be 1 (enabled) or 2 (disabled)"
	}
	return ""
}

func validateModel(v *model.ModelConfig) string {
	v.Alias = strings.TrimSpace(v.Alias)
	v.ProviderID = strings.TrimSpace(v.ProviderID)
	v.UpstreamModel = strings.TrimSpace(v.UpstreamModel)
	if v.Alias == "" || v.ProviderID == "" || v.UpstreamModel == "" {
		return "alias, provider_id and upstream_model are required"
	}
	if v.Status == 0 {
		v.Status = 1
	}
	if v.Status != 1 && v.Status != 2 {
		return "status must be 1 (enabled) or 2 (disabled)"
	}
	return ""
}

// tenantKinds is the set of accepted kind values. The column is free text so a
// deployment can add levels, but the API validates against the known set to
// catch typos before they reach the quota chain.
var tenantKinds = map[string]bool{
	model.TenantKindSchool:  true,
	model.TenantKindDept:    true,
	model.TenantKindTeacher: true,
	model.TenantKindStudent: true,
	model.TenantKindTenant:  true,
}

// negativeQuota returns the name of the first quota field that is explicitly
// set to a negative number. A nil pointer means "inherit" and is not an error.
func negativeQuota(v *model.Tenant) string {
	checks := []struct {
		name  string
		value *int64
	}{
		{"daily_token_limit", v.DailyTokenLimit},
		{"monthly_token_limit", v.MonthlyTokenLimit},
		{"monthly_request_limit", v.MonthlyRequestLimit},
		{"monthly_budget_micros", v.MonthlyBudgetMicros},
		{"soft_token_limit", v.SoftTokenLimit},
	}
	for _, c := range checks {
		if c.value != nil && *c.value < 0 {
			return c.name
		}
	}
	return ""
}

func validateTenant(v *model.Tenant) string {
	v.Code = strings.TrimSpace(v.Code)
	v.Name = strings.TrimSpace(v.Name)
	// Name the offending field. A single "code and name are required" left an
	// operator who had filled in the name with no idea what was actually
	// missing, so the message has to say which field is at fault.
	switch {
	case v.Code == "" && v.Name == "":
		return "tenant code and name are required"
	case v.Code == "":
		return "tenant code is required"
	case v.Name == "":
		return "tenant name is required"
	}
	if v.Status == 0 {
		v.Status = 1
	}
	if v.Status != 1 && v.Status != 2 {
		return "status must be 1 (enabled) or 2 (disabled)"
	}

	v.Kind = strings.TrimSpace(v.Kind)
	if v.Kind == "" {
		v.Kind = model.TenantKindTenant
	}
	if !tenantKinds[v.Kind] {
		return "kind must be one of school, dept, teacher, student, tenant"
	}

	if field := negativeQuota(v); field != "" {
		return field + " cannot be negative (use null to inherit, 0 for unlimited)"
	}
	if v.RPMLimit < 0 || v.TPMLimit < 0 {
		return "rpm_limit and tpm_limit cannot be negative"
	}

	// Drop blanks and duplicates so that a list like ["gpt-4o", " gpt-4o "] does
	// not silently become a two-element set that fails to match. A nil list is
	// preserved as nil -- that is "inherit".
	v.AllowedModels = normalizeModelList(v.AllowedModels)
	return ""
}

func validateAPIKey(v *model.APIKeyAdmin) string {
	v.Name = strings.TrimSpace(v.Name)
	v.TenantID = strings.TrimSpace(v.TenantID)
	v.RouteGroup = strings.TrimSpace(v.RouteGroup)
	if v.Name == "" || v.TenantID == "" {
		return "name and tenant_id are required"
	}
	if v.Status == 0 {
		v.Status = 1
	}
	if v.Status != 1 && v.Status != 2 {
		return "status must be 1 (enabled) or 2 (disabled)"
	}
	if v.RPMLimit == 0 {
		v.RPMLimit = 60
	}
	if v.TPMLimit == 0 {
		v.TPMLimit = 100000
	}
	if v.MaxConcurrency == 0 {
		v.MaxConcurrency = 10
	}
	if v.RPMLimit < 0 || v.TPMLimit < 0 || v.MaxConcurrency < 0 {
		return "limits cannot be negative"
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
