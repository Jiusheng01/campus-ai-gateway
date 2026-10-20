package app

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type adminAuditResponseWriter struct {
	http.ResponseWriter
	status int
}

// Unwrap lets http.ResponseController reach the live net/http writer. Without
// it, POST handlers wrapped for audit capture cannot install per-request read
// deadlines and an authenticated client can hold the handler on a slow body.
func (w *adminAuditResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
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
			writeAdminError(w, reqID, ErrAdminForbidden)
			return
		}
		r = r.WithContext(withAdminPrincipal(r.Context(), principal))

		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
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

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/admin/status":
		s.handleAdminStatus(w, r)
	case r.URL.Path == "/admin/status/trend":
		s.handleAdminTrend(w, r)
	case r.URL.Path == "/admin/whoami":
		s.handleAdminWhoAmI(w, r)
	case r.URL.Path == "/admin/onboarding":
		s.handleOnboarding(w, r)
	case r.URL.Path == "/admin/providers":
		s.handleProviders(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/providers/") && strings.HasSuffix(r.URL.Path, "/upstream-models"):
		s.handleProviderUpstreamModels(w, r)
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
	case r.URL.Path == "/admin/tenants" || strings.HasPrefix(r.URL.Path, "/admin/tenants/"):
		// The suffix routes are matched before the node route, because
		// "/admin/tenants/{id}/usage" would otherwise be read as a tenant whose
		// id happens to contain a slash.
		switch tenantPathKind(r.URL.Path) {
		case tenantPathCollection:
			s.handleTenants(w, r)
		case tenantPathAllocation:
			s.handleTenantAllocation(w, r)
		case tenantPathUsage:
			s.handleTenantUsage(w, r)
		case tenantPathNode:
			s.handleTenantByID(w, r)
		default:
			http.NotFound(w, r)
		}
	case r.URL.Path == "/admin/model-quotas":
		s.handleModelQuotas(w, r)
	case r.URL.Path == "/admin/api-keys":
		s.handleAPIKeys(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/api-keys/"):
		s.handleAPIKeyByID(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/request-logs/") && strings.HasSuffix(r.URL.Path, "/attempts"):
		s.handleRequestLogAttempts(w, r)
	case r.URL.Path == "/admin/request-logs":
		s.handleRequestLogs(w, r)
	case r.URL.Path == "/admin/request-logs/usage":
		s.handleRequestLogsUsage(w, r)
	case r.URL.Path == "/admin/request-logs/summary":
		s.handleRequestLogsSummary(w, r)
	case r.URL.Path == "/admin/request-logs/facets":
		s.handleRequestLogFacets(w, r)
	case r.URL.Path == "/admin/request-logs/export":
		s.handleRequestLogsExport(w, r)
	case r.URL.Path == "/admin/quota-rebuild":
		s.handleQuotaRebuild(w, r)
	case r.URL.Path == "/admin/admin-audit":
		s.handleAdminAuditLogs(w, r)
	case r.URL.Path == "/admin/security-events":
		s.handleSecurityEvents(w, r)
	default:
		http.NotFound(w, r)
	}
}

// Kinds of /admin/tenants URL, so the suffix routes win over the node route.
const (
	tenantPathCollection = "collection"
	tenantPathNode       = "node"
	tenantPathUsage      = "usage"
	tenantPathAllocation = "allocation"
)

// tenantPathKind classifies a /admin/tenants path.
//
// It returns "" for anything that is neither a node nor one of the two known
// suffixes. That matters: a tenant id is a uuid and never contains a slash, so
// "/admin/tenants/{id}/{anything else}" is a URL nothing serves, and answering
// 404 is more honest than handing the whole tail to the node handler as an id.
func tenantPathKind(path string) string {
	if path == "/admin/tenants" {
		return tenantPathCollection
	}
	rest := strings.TrimPrefix(path, "/admin/tenants/")
	if rest == "" {
		return ""
	}
	if !strings.Contains(rest, "/") {
		return tenantPathNode
	}
	switch {
	case strings.HasSuffix(path, "/allocation"):
		return tenantPathAllocation
	case strings.HasSuffix(path, "/usage"):
		return tenantPathUsage
	default:
		return ""
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
		// The console needs to know whether it is looking at the whole gateway
		// or one subtree: the tenant tree, the key list and the quota editor all
		// render differently, and a scoped administrator must not be offered a
		// "create root tenant" button the API will refuse.
		"scope_tenant_id": p.ScopeTenantID,
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

	// data_plane is authoritative and always present: it names the one data
	// plane this gateway runs.
	out := map[string]any{
		"gateway":        true,
		"postgres":       dbOK,
		"redis":          redisOK,
		"data_plane":     s.cfg.DataPlaneMode(),
		"stats":          stats,
		"provider_stats": providerStats,
	}
	// The per-provider circuits are only meaningful for the direct plane, which
	// is the only one that breaks them. Exposing them lets an operator see why a
	// channel is not being used instead of guessing.
	if s.breaker != nil {
		out["circuits"] = s.breaker.circuits()
	}
	writeJSON(w, http.StatusOK, out)
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
		s.invalidateRouteTable()
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
	s.invalidateRouteTable()
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
		if errors.Is(err, storage.ErrModelAliasTaken) {
			httpx.Error(w, http.StatusConflict, "model_alias_taken", "alias is already in use; choose another public model name", reqID)
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Model could not be created", reqID)
			return
		}
		s.invalidateRouteTable()
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
	switch {
	case errors.Is(err, storage.ErrModelAliasImmutable):
		httpx.Error(w, http.StatusConflict, "model_alias_immutable", "alias is the public model name and cannot be changed; create a new model instead", reqID)
		return
	case errors.Is(err, storage.ErrModelNotFound):
		httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
		return
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Model could not be updated", reqID)
		return
	}
	s.invalidateRouteTable()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	switch r.Method {
	case http.MethodGet:
		// A scoped administrator sees their own subtree and nothing above or
		// beside it. The narrowing happens in the query, not after the rows
		// are read, so the other departments never leave the database.
		var (
			items []model.Tenant
			err   error
		)
		if scope := adminScopeTenantID(r); scope != "" {
			items, err = s.pg.ListTenantsInSubtree(r.Context(), scope)
		} else {
			items, err = s.pg.ListTenants(r.Context())
		}
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
		// A scoped administrator creates tenants underneath themselves. The
		// parent is required rather than defaulted: "create a root tenant" is
		// not something this role can ask for, and silently attaching the new
		// node to their own tenant would produce a tree the operator did not
		// describe.
		if scope := adminScopeTenantID(r); scope != "" {
			parent := model.NormalizeOptionalID(in.ParentID)
			if parent == nil {
				httpx.Error(w, http.StatusBadRequest, "parent_required",
					"parent_id is required when creating a tenant inside a scope", reqID)
				return
			}
			if !s.requireTenantInScope(w, r, *parent, reqID) {
				return
			}
		}
		out, err := s.pg.CreateTenant(r.Context(), in)
		if err != nil {
			switch {
			case errors.Is(err, storage.ErrTenantParentNotFound):
				httpx.Error(w, http.StatusBadRequest, "parent_tenant_not_found", "The parent tenant does not exist", reqID)
			case errors.Is(err, storage.ErrTenantCodeTaken):
				httpx.Error(w, http.StatusConflict, "tenant_code_taken", "That tenant code is already in use", reqID)
			default:
				log.Printf("tenant create: %s failed: %v", in.Code, err)
				httpx.Error(w, http.StatusInternalServerError, "database_error", "Tenant could not be created", reqID)
			}
			return
		}
		writeJSON(w, http.StatusCreated, out)
	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
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
	if !s.requireTenantInScope(w, r, id, reqID) {
		return
	}
	stats, err := s.pg.TenantUsageStats(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant usage could not be loaded", reqID)
		return
	}
	// The drawer draws a bar per limit, so it needs the value that actually
	// applies rather than the tenant's own declaration. Under a hierarchy most
	// tenants declare nothing and inherit, and dividing by the declared column
	// would leave the bar empty for a tenant that is in fact capped.
	if limits, err := s.pg.EffectiveTenantLimits(r.Context(), id); err == nil {
		stats.Effective = &limits
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
	// A scoped administrator may not rewrite the tenant they are scoped to:
	// that row holds the ceiling someone above them set, and raising your own
	// limit is the one edit that makes the hierarchy decorative. Descendants
	// remain editable — delegating a department's quota is the job.
	if !s.requireTenantWritable(w, r, id, reqID) {
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
		switch {
		case errors.Is(err, storage.ErrTenantNotFound):
			httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found", reqID)
		case errors.Is(err, storage.ErrTenantCodeTaken):
			// A 404 here would send the operator hunting for a tenant that is
			// right in front of them; the collision is with the code field.
			httpx.Error(w, http.StatusConflict, "tenant_code_taken", "That tenant code is already in use", reqID)
		default:
			log.Printf("tenant update: %s failed: %v", id, err)
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Tenant could not be updated", reqID)
		}
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	switch r.Method {
	case http.MethodGet:
		var (
			items []model.APIKeyAdmin
			err   error
		)
		if scope := adminScopeTenantID(r); scope != "" {
			items, err = s.pg.ListAPIKeysInSubtree(r.Context(), scope)
		} else {
			items, err = s.pg.ListAPIKeys(r.Context())
		}
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
		// The key is issued for a tenant in the caller's subtree. A key minted
		// for a tenant outside it would be a credential the issuer cannot see,
		// revoke or reason about.
		if !s.requireTenantInScope(w, r, in.TenantID, reqID) {
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
	id := strings.TrimPrefix(r.URL.Path, "/admin/api-keys/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut:
		s.updateAPIKey(w, r, id, reqID)
	case http.MethodDelete:
		s.revokeAPIKey(w, r, id, reqID)
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) updateAPIKey(w http.ResponseWriter, r *http.Request, id, reqID string) {
	if !s.requireAPIKeyInScope(w, r, id, reqID) {
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
	// Both ends of the move are checked: the key must already belong to the
	// caller's subtree, and the tenant it is being moved to must be in it too.
	// Without the second check a scoped administrator could take a key they may
	// edit and hand it to a department they do not administer.
	if !s.requireTenantInScope(w, r, in.TenantID, reqID) {
		return
	}
	out, err := s.pg.UpdateAPIKey(r.Context(), id, in)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "api_key_not_found", "API key was not found or could not be updated", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// revokeAPIKey destroys a key's credential.
//
// DELETE means revoke here, not "remove the row". gateway_request_log records
// the key that served each request by id, so deleting the row would leave every
// historical request pointing at an identifier nothing can resolve — the audit
// trail would survive but stop being readable, and the usage column on the key
// list would have nothing to aggregate.
//
// So the row stays and two things change: the key is disabled, and its stored
// digest is replaced with a freshly generated one. The second part is what
// makes this a revocation rather than a disable. Without it the column would
// still hold the digest of a credential that has already been handed out, and
// re-enabling the key — an ordinary edit anyone with key access can make —
// would bring the same credential back into service. With it, the only way to
// use this key again is to create a new one.
//
// The operation is idempotent: revoking twice re-rotates the digest, which is
// harmless, and answers 200 both times.
func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request, id, reqID string) {
	if _, err := uuid.Parse(id); err != nil {
		// Not a UUID cannot name a row, so it is the same answer as a key that
		// does not exist — and it must not reach the database, where the cast
		// would fail the whole statement.
		httpx.Error(w, http.StatusNotFound, "api_key_not_found", "API key was not found", reqID)
		return
	}
	if !s.requireAPIKeyInScope(w, r, id, reqID) {
		return
	}
	_, _, digest, err := security.NewAPIKey(s.cfg.KeyHMACSecret)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "key_generation_failed", "Unable to generate a replacement credential", reqID)
		return
	}
	if err := s.pg.RevokeAPIKey(r.Context(), id, digest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "api_key_not_found", "API key was not found", reqID)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "database_error", "API key could not be revoked", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "status": 2})
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

func requestLogFilterFrom(r *http.Request, defaultLimit int) (storage.RequestLogFilter, error) {
	q := r.URL.Query()
	limit := defaultLimit
	if raw := q.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 5000 {
			limit = n
		}
	}
	offset := 0
	if raw := q.Get("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			offset = n
		}
	}
	status := 0
	if raw := q.Get("status"); raw != "" {
		status, _ = strconv.Atoi(raw)
	}
	// The zero value of Failover means "did not fail over", so the "any" case
	// has to be stated explicitly. It is spelled out here rather than left to
	// the struct's zero value because getting it wrong silently hides every
	// failover row from the table.
	failover := -1
	switch q.Get("failover") {
	case "true", "1":
		failover = 1
	case "false", "0":
		failover = 0
	}
	from, err := parseTimeParam(q.Get("from"))
	if err != nil {
		return storage.RequestLogFilter{}, err
	}
	to, err := parseTimeParam(q.Get("to"))
	if err != nil {
		return storage.RequestLogFilter{}, err
	}
	return storage.RequestLogFilter{
		Limit:     limit,
		Offset:    offset,
		Model:     strings.TrimSpace(q.Get("model")),
		Provider:  strings.TrimSpace(q.Get("provider")),
		Status:    status,
		RequestID: strings.TrimSpace(q.Get("request_id")),
		Failover:  failover,
		TenantID:  strings.TrimSpace(q.Get("tenant_id")),
		APIKeyID:  strings.TrimSpace(q.Get("api_key_id")),
		From:      from,
		To:        to,
	}, nil
}

// parseTimeParam reads one end of a time range.
//
// Only RFC 3339 is accepted, and that is deliberate: it is the one form that
// carries the offset. A bare local timestamp would have to be interpreted in a
// zone the server does not know, and guessing wrong shifts the window by hours
// without anything failing — the operator would see a plausible table of the
// wrong rows. The console converts its local pickers to instants before sending
// them for the same reason.
//
// An empty value means "unbounded on that side". A malformed one is an error
// rather than a silent omission, because a dropped time filter looks exactly
// like a filter that matched everything.
func parseTimeParam(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("time filters must be RFC 3339 instants, got %q", raw)
	}
	return t, nil
}

func (s *Server) handleRequestLogs(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	f, err := requestLogFilterFrom(r, 100)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_filter", err.Error(), reqID)
		return
	}
	items, err := s.pg.SearchRequestLogs(r.Context(), f)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list request logs", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "limit": f.Limit, "offset": f.Offset})
}

// handleRequestLogAttempts returns one request's Routing Attempt chain.
//
// It is a separate call from the log list on purpose: a list of fifty requests
// would otherwise carry every attempt of every row. The console asks for the
// chain when an operator opens a detail drawer.
func (s *Server) handleRequestLogAttempts(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/request-logs/"), "/attempts")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	items, err := s.pg.RequestAttempts(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load request attempts", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

// handleRequestLogsSummary aggregates the rows the table is currently filtered
// to.
//
// It is a separate endpoint from the table because the two answer different
// questions and are paginated differently: the table is fifty rows of detail,
// the summary is the whole filtered set in eight numbers. Computing the summary
// over the returned page would make "total requests" mean "requests on this
// page", which is the one reading an operator would never intend.
//
// It takes exactly the same query parameters as the table, from the same
// parser, so the two cannot drift into describing different sets of rows.
func (s *Server) handleRequestLogsSummary(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	f, err := requestLogFilterFrom(r, 0)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_filter", err.Error(), reqID)
		return
	}
	summary, err := s.pg.SummarizeRequestLogs(r.Context(), f)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to summarise request logs", reqID)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// handleRequestLogsUsage answers the operator's three usage questions in one
// call: how much traffic the filter covers, how much of the tenant's quota that
// is, and which API keys are consuming it.
//
// It takes exactly the same query parameters as the detail page and the summary
// (requestLogFilterFrom), and every aggregate runs over the same predicate, so
// the numbers cannot describe a different set of rows than the list beneath
// them.
//
// The quota field reflects a single tenant's limits. Which tenant that is
// follows the least-surprising reading: the explicit tenant filter if there is
// one, otherwise the tenant the filtered rows belong to — and when the filter
// spans more than one tenant there is no single denominator, so every quota
// dimension (and every by-key quota share) is null rather than a made-up
// percentage. Within those limits, used is Postgres-derived day/month usage and
// limit is the tenant's configured limit; an unset limit (<= 0) is null, never
// 0%.
func (s *Server) handleRequestLogsUsage(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	f, err := requestLogFilterFrom(r, 0)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_filter", err.Error(), reqID)
		return
	}

	out := model.RequestLogUsage{}

	summary, err := s.pg.SummarizeRequestLogs(r.Context(), f)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to aggregate request logs", reqID)
		return
	}
	out.Totals.RequestLogSummary = summary

	byKey, err := s.pg.GroupRequestLogsByKey(r.Context(), f)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to group request logs by key", reqID)
		return
	}
	out.ByKey = byKey

	// Quota is only expressible against a single tenant's limits.
	tenantID, err := s.resolveUsageTenant(r.Context(), f)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load tenant quota", reqID)
		return
	}
	if tenantID != "" {
		// The denominator has to be the tenant's EFFECTIVE limit, not its own
		// declaration: under a hierarchy a tenant usually declares nothing and
		// inherits from an ancestor, so reading the column directly would show
		// "used 40,000 / limit —" for a tenant that is actually capped at
		// 50,000 by its school.
		limits, err := s.pg.EffectiveTenantLimits(r.Context(), tenantID)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load tenant quota", reqID)
			return
		}
		stats, err := s.pg.TenantUsageStats(r.Context(), tenantID)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load tenant quota", reqID)
			return
		}
		out.Totals.Quota = model.RequestLogQuota{
			DailyToken:     quotaUsage(stats.DailyTokens, limits.DailyTokenLimit),
			MonthlyToken:   quotaUsage(stats.MonthlyTokens, limits.MonthlyTokenLimit),
			MonthlyRequest: quotaUsage(stats.MonthlyRequests, limits.MonthlyRequestLimit),
		}
		fillQuotaShares(out.ByKey, limits.MonthlyTokenLimit)
	}

	writeJSON(w, http.StatusOK, out)
}

// resolveUsageTenant names the single tenant a quota can be quoted against, or
// returns "" when the filter spans tenants (or matches none).
func (s *Server) resolveUsageTenant(ctx context.Context, f storage.RequestLogFilter) (string, error) {
	if f.TenantID != "" {
		return f.TenantID, nil
	}
	ids, err := s.pg.DistinctRequestTenants(ctx, f)
	if err != nil {
		return "", err
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	return "", nil
}

// quotaUsage derives the share of a limit a used amount represents, or nil when
// the tenant has no limit for that dimension (limit <= 0) — the "not set" case
// must not render as 0%.
func quotaUsage(used, limit int64) *model.QuotaDimension {
	if limit <= 0 {
		return nil
	}
	return &model.QuotaDimension{
		Used:  used,
		Limit: limit,
		Pct:   usagePct(used, limit),
	}
}

// fillQuotaShares sets each key's QuotaShare from its tokens against the
// tenant's monthly token limit. It is only called in the single-tenant case; a
// mix of tenants leaves every share null because there is no one denominator.
func fillQuotaShares(keys []model.APIKeyUsage, monthlyLimit int64) {
	if monthlyLimit <= 0 {
		return
	}
	for i := range keys {
		pct := usagePct(keys[i].Tokens, monthlyLimit)
		keys[i].QuotaShare = &pct
	}
}

func usagePct(used, limit int64) float64 {
	return float64(used) * 100 / float64(limit)
}

// handleRequestLogFacets answers the option lists for the log filters.
//
// It is derived from the log rather than from the tenant and key tables so that
// the role which may read logs — an auditor — can also filter them, without
// being granted the tenant and key management APIs it deliberately does not
// have.
func (s *Server) handleRequestLogFacets(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	facets, err := s.pg.RequestLogFacets(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list request log filters", reqID)
		return
	}
	writeJSON(w, http.StatusOK, facets)
}

// handleAdminTrend answers the dashboard's traffic chart.
//
// The window defaults to the last 24 hours in hourly buckets, which is the view
// an operator opens the console to see. A caller that wants a different window
// states both ends; the range is half-open so that two adjacent windows never
// count the boundary row twice.
func (s *Server) handleAdminTrend(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	q := r.URL.Query()

	unit, ok := trendUnit(q.Get("bucket"))
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "invalid_bucket", "bucket must be hour or day", reqID)
		return
	}

	to, err := parseTimeParam(q.Get("to"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_filter", err.Error(), reqID)
		return
	}
	if to.IsZero() {
		to = time.Now()
	}
	from, err := parseTimeParam(q.Get("from"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_filter", err.Error(), reqID)
		return
	}
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}
	if !from.Before(to) {
		httpx.Error(w, http.StatusBadRequest, "invalid_range", "from must be earlier than to", reqID)
		return
	}
	if to.Sub(from) > time.Duration(maxTrendBuckets)*trendStep(unit) {
		httpx.Error(w, http.StatusBadRequest, "range_too_wide",
			fmt.Sprintf("the range would need more than %d %s buckets", maxTrendBuckets, unit), reqID)
		return
	}

	offset := parseTrendOffset(q.Get("tz_offset"))
	rows, err := s.pg.RequestLogSeries(r.Context(), from, to, unit, offset, trendLabelFormat(unit))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the traffic trend", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":   fillTrendBuckets(rows, from, to, unit, offset),
		"bucket": unit,
		"from":   from.UTC(),
		"to":     to.UTC(),
	})
}

func (s *Server) handleRequestLogsExport(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	f, err := requestLogFilterFrom(r, 5000)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_filter", err.Error(), reqID)
		return
	}
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

// reservedCredentialEnvPrefixes names the environment-variable families the
// gateway process itself owns.
//
// A channel credential is an outbound secret the operator is already entitled
// to use. The gateway's own keys are not: `credential_env` is a *name*, and the
// data plane resolves it at request time with os.Getenv and sends the value to
// whatever base_url the same operator supplied. Without this guard, "may edit
// channels" silently means "may read every secret in the process environment",
// because a channel pointing at an attacker-controlled base_url with
// credential_env=ADMIN_SESSION_SECRET simply echoes the signing key back in the
// connectivity test. That is a full management-plane takeover, and it was
// reachable by gateway_admin, which is not supposed to be able to touch
// administrators at all.
//
// The list is prefixes rather than exact names on purpose: a new ADMIN_*
// variable added later must be protected without anyone remembering to update
// this function.
// The proxy entries are separate because the direct data plane builds its
// transport with http.ProxyFromEnvironment, which reads HTTPS_PROXY, NO_PROXY
// and ALL_PROXY as well as HTTP_PROXY. Those values routinely embed
// "user:password@proxy", and "HTTPS_" is not covered by "HTTP_".
var reservedCredentialEnvPrefixes = []string{
	"ADMIN_", "API_KEY_", "AWS_", "AZURE_", "DATABASE_", "DATA_PLANE",
	"DOCKER_", "GATEWAY_", "GCP_", "GOOGLE_", "HTTP_", "HTTPS_", "KUBERNETES_",
	"NO_PROXY", "ALL_PROXY", "PG", "PUBLIC_", "REDIS_", "TEST_",
	"TRUSTED_", "TWCORE_",
}

// credentialEnvNamePattern is the shape a channel credential may take: an
// ordinary upper-case environment variable name. Rejecting everything else also
// rules out values that are not variable names at all, such as a literal key
// pasted into the field — which would put a secret in the database, the one
// thing the credential_env indirection exists to prevent.
var credentialEnvNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,127}$`)

// validateCredentialEnv rejects a credential_env that names something the
// gateway owns. It returns an operator-facing reason, or "" when acceptable.
func validateCredentialEnv(name string) string {
	if !credentialEnvNamePattern.MatchString(name) {
		return "credential_env must be an upper-case environment variable name (A-Z, 0-9, _)"
	}
	for _, prefix := range reservedCredentialEnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return "credential_env must not name a gateway-owned variable (" + prefix + "*); use a dedicated variable for this channel"
		}
	}
	return ""
}

// validateProvider validates a channel.
//
// The gateway speaks the OpenAI wire format only: the in-process data plane
// sends the OpenAI chat body it received straight to the provider, so a channel
// marked anthropic or gemini would be sent a body it cannot read and fail at
// request time. Refusing it when it is saved is a configuration error instead
// of an outage.
func validateProvider(v *model.Provider) string {
	v.Name = strings.TrimSpace(v.Name)
	v.BaseURL = strings.TrimSpace(v.BaseURL)
	v.Protocol = strings.TrimSpace(v.Protocol)
	v.CredentialEnv = strings.TrimSpace(v.CredentialEnv)
	if v.Name == "" || v.BaseURL == "" || v.CredentialEnv == "" {
		return "name, base_url and credential_env are required"
	}
	if reason := validateCredentialEnv(v.CredentialEnv); reason != "" {
		return reason
	}
	switch v.Protocol {
	case "":
		v.Protocol = "openai-chat"
	case "openai-chat":
	default:
		return "unsupported protocol; the gateway only speaks the OpenAI chat format"
	}
	if v.Status == 0 {
		v.Status = 1
	}
	if v.Status != 1 && v.Status != 2 {
		return "status must be 1 (enabled) or 2 (disabled)"
	}
	return ""
}

// validateModel checks the catalog entry only. Provider and upstream model are
// deliberately absent: a model with no route is valid and simply not exposed to
// business traffic until a route is added.
func validateModel(v *model.ModelConfig) string {
	v.Alias = strings.TrimSpace(v.Alias)
	v.DisplayName = strings.TrimSpace(v.DisplayName)
	if v.Alias == "" {
		return "alias is required"
	}
	if v.DisplayName == "" {
		v.DisplayName = v.Alias
	}
	if v.Status == 0 {
		v.Status = 1
	}
	if v.Status != 1 && v.Status != 2 {
		return "status must be 1 (enabled) or 2 (disabled)"
	}
	return ""
}

func validateTenant(v *model.Tenant) string {
	v.Code = strings.TrimSpace(v.Code)
	v.Name = strings.TrimSpace(v.Name)
	// The messages name the field that is actually missing. "code and name are
	// required" for an operator who only forgot the code is the kind of message
	// that sends them looking at the wrong input.
	if v.Code == "" && v.Name == "" {
		return "tenant code and name are required"
	}
	if v.Code == "" {
		return "tenant code is required"
	}
	if v.Name == "" {
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
	// The column is free text so a deployment could add a level, but the console
	// only knows how to render these five. Accepting an unknown value here would
	// store a tenant the UI cannot display.
	switch v.Kind {
	case model.TenantKindSchool, model.TenantKindDept, model.TenantKindTeacher,
		model.TenantKindStudent, model.TenantKindTenant:
	default:
		return "kind must be one of school, dept, teacher, student, tenant"
	}
	if v.MonthlyBudgetMicros < 0 {
		return "monthly_budget_micros cannot be negative"
	}
	// nil means "inherit", which is not a value to validate. Only a declared
	// value can be negative, and a negative cap is meaningless: it would reject
	// every request, including one that spends no tokens.
	//
	// The field name is part of the message because the console shows it
	// verbatim: "a quota cannot be negative" leaves the operator to guess which
	// of seven inputs is wrong.
	for _, limit := range []struct {
		field string
		value *int64
	}{
		{"daily_token_limit", v.DailyTokenLimit},
		{"monthly_token_limit", v.MonthlyTokenLimit},
		{"monthly_request_limit", v.MonthlyRequestLimit},
		{"rpm_limit", v.RPMLimit},
		{"tpm_limit", v.TPMLimit},
		{"max_concurrency", v.MaxConcurrency},
	} {
		if limit.value != nil && *limit.value < 0 {
			return limit.field + " cannot be negative"
		}
	}
	// nil stays nil (inherit); an empty but non-nil list stays empty (deny every
	// model). Collapsing the two here would silently invert the access rule.
	v.AllowedModels = model.NormalizeModelList(v.AllowedModels)
	return ""
}

// normalizeAllowedModels validates and canonicalises a key's model allow-list.
//
// An empty list is a decision, not an omission: it means the key may call no
// model. The wildcard is the opposite decision and has to be spelled out, so it
// may not be mixed with named models — a list that says both "everything" and
// "these two" has no single reading, and picking one of the two readings is
// exactly how the code and the console came to disagree.
func normalizeAllowedModels(list *[]string) string {
	if list == nil || *list == nil {
		*list = []string{}
		return ""
	}
	seen := make(map[string]struct{}, len(*list))
	out := make([]string, 0, len(*list))
	wildcard := false
	for _, raw := range *list {
		name := strings.TrimSpace(raw)
		if name == "" {
			return "allowed_models must not contain an empty entry"
		}
		if name == model.AllModelsWildcard {
			wildcard = true
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if wildcard {
		if len(out) > 0 {
			return `allowed_models must be either the "*" wildcard or a list of models, not both`
		}
		*list = []string{model.AllModelsWildcard}
		return ""
	}
	sort.Strings(out)
	*list = out
	return ""
}

func validateAPIKey(v *model.APIKeyAdmin) string {
	v.Name = strings.TrimSpace(v.Name)
	v.TenantID = strings.TrimSpace(v.TenantID)
	v.RouteGroup = strings.TrimSpace(v.RouteGroup)
	if v.Name == "" || v.TenantID == "" {
		return "name and tenant_id are required"
	}
	if reason := normalizeAllowedModels(&v.AllowedModels); reason != "" {
		return reason
	}
	if v.Status == 0 {
		v.Status = 1
	}
	if v.Status != 1 && v.Status != 2 {
		return "status must be 1 (enabled) or 2 (disabled)"
	}
	// Rate limits are the tenant's, not the key's (see ADR-0015). The key is a
	// credential, so there is nothing here to validate or default: a key with no
	// limit of its own is the normal case, and rewriting a value the operator
	// sent would be inventing a constraint they did not ask for.
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
