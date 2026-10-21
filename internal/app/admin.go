package app

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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

		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			aw := &adminAuditResponseWriter{ResponseWriter: w}
			next.ServeHTTP(aw, r)
			status := aw.status
			if status == 0 {
				status = http.StatusOK
			}
			if status < http.StatusBadRequest && routingWriteAffectsRoutes(r.Method, r.URL.Path) {
				s.invalidateRouteCache()
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

// routingWriteAffectsRoutes narrows cache invalidation to writes that can
// change the routing table. A diagnostic test is a POST under a channel path
// but does not change routing configuration.
func routingWriteAffectsRoutes(method, path string) bool {
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch && method != http.MethodDelete {
		return false
	}
	if strings.HasSuffix(path, "/test") {
		return false
	}
	return strings.HasPrefix(path, "/admin/providers") ||
		path == "/admin/models" ||
		strings.HasPrefix(path, "/admin/models/") ||
		strings.HasPrefix(path, "/admin/model-routes/") ||
		strings.HasPrefix(path, "/admin/model-catalog")
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/admin/status":
		s.handleAdminStatus(w, r)
	case r.URL.Path == "/admin/status/trend":
		s.handleAdminTrend(w, r)
	case r.URL.Path == "/admin/whoami":
		s.handleAdminWhoAmI(w, r)
	case r.URL.Path == "/admin/providers":
		s.handleProviders(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/providers/") && strings.HasSuffix(r.URL.Path, "/impact"):
		s.handleProviderImpact(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/providers/") && strings.HasSuffix(r.URL.Path, "/runtime-summary"):
		s.handleProviderRuntimeSummary(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/providers/") && strings.HasSuffix(r.URL.Path, "/test"):
		s.handleProviderTest(w, r)
	// A channel's model list is addressed under the channel. Keep these before
	// the single-channel match below so /models is not read as a provider id.
	case strings.HasPrefix(r.URL.Path, "/admin/providers/") && strings.HasSuffix(r.URL.Path, "/models/refresh"):
		s.handleProviderModelsRefresh(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/providers/") && strings.HasSuffix(r.URL.Path, "/models"):
		s.handleProviderModels(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/providers/") && strings.Contains(r.URL.Path, "/models/"):
		s.handleProviderModelByID(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/providers/"):
		s.handleProviderByID(w, r)
	// The Model Catalog is the entry point over Provider Models. It is a
	// management view, so it lives under /admin and is never tenant-reachable.
	case r.URL.Path == "/admin/model-catalog/open":
		s.handleOpenCatalogModel(w, r)
	case r.URL.Path == "/admin/model-catalog":
		s.handleModelCatalog(w, r)
	case r.URL.Path == "/admin/routes":
		s.handleAllModelRoutes(w, r)
	case r.URL.Path == "/admin/models/publish":
		s.handlePublishModel(w, r)
	case r.URL.Path == "/admin/models":
		s.handleModels(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/models/") && strings.HasSuffix(r.URL.Path, "/impact"):
		s.handleModelImpact(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/models/") && strings.HasSuffix(r.URL.Path, "/runtime-summary"):
		s.handleModelRuntimeSummary(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/models/") && strings.HasSuffix(r.URL.Path, "/authorizations"):
		s.handleModelAuthorizations(w, r)
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
	case strings.HasPrefix(r.URL.Path, "/admin/tenants/"):
		s.handleTenantByID(w, r)
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
	configCounts, err := s.pg.ConfigCounts(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load configuration counts", reqID)
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
		"config_counts":  configCounts,
		"route_cache":    s.routeCacheState(),
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
		q := r.URL.Query()
		filtered := q.Get("name") != "" || q.Get("provider_type") != "" || q.Get("protocol") != "" ||
			q.Get("status") != "" || q.Get("limit") != "" || q.Get("offset") != ""
		var (
			items []model.Provider
			total int
			err   error
		)
		if filtered {
			status, _ := strconv.Atoi(q.Get("status"))
			limit, _ := strconv.Atoi(q.Get("limit"))
			offset, _ := strconv.Atoi(q.Get("offset"))
			items, total, err = s.pg.ListProvidersPage(r.Context(), storage.ProviderFilter{
				Name:         q.Get("name"),
				ProviderType: q.Get("provider_type"),
				Protocol:     q.Get("protocol"),
				Status:       status,
				Limit:        limit,
				Offset:       offset,
			})
		} else {
			items, err = s.pg.ListProviders(r.Context())
			total = len(items)
		}
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list providers", reqID)
			return
		}
		views := make([]model.Provider, len(items))
		for i := range items {
			views[i] = s.providerView(items[i])
		}
		out := map[string]any{"data": views, "total": total}
		if filtered {
			out["limit"] = q.Get("limit")
			out["offset"] = q.Get("offset")
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		var in model.Provider
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
			return
		}
		if err := s.validateProvider(&in); err != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_provider", err, reqID)
			return
		}
		if err := s.sealProviderCredential(&in); err != nil {
			log.Printf("providers: seal credential for new channel: %v", err)
			httpx.Error(w, http.StatusInternalServerError, "credential_seal_failed", "Unable to encrypt the channel credential", reqID)
			return
		}
		// The seed is part of the create, not a follow-up: a newly created
		// channel already knows the common models its supplier offers.
		out, err := s.pg.CreateProviderWithPresetModels(r.Context(), in, presetProviderModels(in.Name, in.BaseURL))
		if err != nil {
			httpx.Error(w, http.StatusConflict, "provider_create_failed", "Provider could not be created", reqID)
			return
		}
		writeJSON(w, http.StatusCreated, s.providerView(out))
	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) handleProviderByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	id := strings.TrimPrefix(r.URL.Path, "/admin/providers/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		s.deleteProvider(w, r, id, reqID)
		return
	case http.MethodPatch:
		s.updateProviderStatus(w, r, id, reqID)
		return
	case http.MethodPut:
	default:
		w.Header().Set("Allow", "PUT, PATCH, DELETE")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	var in model.Provider
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	if err := s.validateProviderForUpdate(&in); err != "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_provider", err, reqID)
		return
	}
	if err := s.sealProviderCredential(&in); err != nil {
		log.Printf("providers: seal credential for channel %s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "credential_seal_failed", "Unable to encrypt the channel credential", reqID)
		return
	}
	out, err := s.pg.UpdateProvider(r.Context(), id, in)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "provider_not_found", "Provider was not found or could not be updated", reqID)
		return
	}
	writeJSON(w, http.StatusOK, s.providerView(out))
}

// updateProviderStatus is the status-only write behind the bounded batch
// enable/disable action. It deliberately accepts no other field, so a stale
// list cannot overwrite a concurrent URL or credential edit.
func (s *Server) updateProviderStatus(w http.ResponseWriter, r *http.Request, id, reqID string) {
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "provider_not_found", "Provider was not found", reqID)
		return
	}
	var in struct {
		Status int `json:"status"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must contain only status", reqID)
		return
	}
	if in.Status != 1 && in.Status != 2 {
		httpx.Error(w, http.StatusBadRequest, "invalid_provider", "status must be 1 (enabled) or 2 (disabled)", reqID)
		return
	}
	out, err := s.pg.UpdateProviderStatus(r.Context(), id, in.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "provider_not_found", "Provider was not found", reqID)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Provider status could not be updated", reqID)
		return
	}
	writeJSON(w, http.StatusOK, s.providerView(out))
}

// deleteProvider hard-deletes a channel and reports what the cascade removed.
//
// The response is 200 with counts rather than 204 because the counts are the
// confirmation: they say how many Routes and Virtual Models actually went away,
// which can differ from what the console previewed moments earlier. A provider
// that does not exist — including an id that cannot name a row — is 404, the
// same answer as an id that was already deleted.
func (s *Server) deleteProvider(w http.ResponseWriter, r *http.Request, id, reqID string) {
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "provider_not_found", "Provider was not found", reqID)
		return
	}
	out, err := s.pg.DeleteProvider(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "provider_not_found", "Provider was not found", reqID)
		return
	}
	if err != nil {
		log.Printf("providers: delete channel %s: %v", id, err)
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Provider could not be deleted", reqID)
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
		if err := validateModel(&in, true); err != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_model", err, reqID)
			return
		}
		// The legacy create is a publish by binding: record the candidate if
		// needed, then use the same transaction/validation service as publish
		// and catalog open. It does not silently append to an existing alias.
		candidate, _, err := s.pg.AddProviderModel(r.Context(), in.ProviderID, in.UpstreamModel)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "model_create_failed", "The channel does not accept this upstream model", reqID)
			return
		}
		res, err := s.pg.PublishProviderModel(r.Context(), candidate.ID, in.Alias, in.DisplayName, in.Description, false)
		switch {
		case errors.Is(err, storage.ErrAliasExists):
			httpx.Error(w, http.StatusConflict, "model_create_failed", "The alias already exists with a different binding", reqID)
			return
		case errors.Is(err, pgx.ErrNoRows):
			httpx.Error(w, http.StatusNotFound, "provider_not_found", "The channel was not found or is not enabled", reqID)
			return
		case err != nil:
			httpx.Error(w, http.StatusConflict, "model_create_failed", "Model could not be created", reqID)
			return
		}
		status := http.StatusCreated
		if !res.Created {
			status = http.StatusOK
		}
		writeJSON(w, status, res.Model)
	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) handleModelByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	id := strings.TrimPrefix(r.URL.Path, "/admin/models/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.getModelDetail(w, r, id, reqID)
		return
	case http.MethodPut:
	default:
		w.Header().Set("Allow", "GET, PUT")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	var body struct {
		Alias           string `json:"alias"`
		DisplayName     string `json:"display_name"`
		Description     string `json:"description"`
		Status          int    `json:"status"`
		ConfigVersion   int64  `json:"config_version"`
		ExpectedVersion *int64 `json:"expected_version"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON with alias, display_name, description, status and expected_version", reqID)
		return
	}
	in := model.ModelConfig{
		Alias: body.Alias, DisplayName: body.DisplayName, Description: body.Description, Status: body.Status,
	}
	if err := validateModel(&in, false); err != "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_model", err, reqID)
		return
	}
	expectedVersion := body.ConfigVersion
	if body.ExpectedVersion != nil {
		expectedVersion = *body.ExpectedVersion
	}
	existing, err := s.pg.ModelByID(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
		return
	}
	if strings.TrimSpace(in.Alias) != "" && strings.TrimSpace(in.Alias) != existing.Alias {
		httpx.Error(w, http.StatusBadRequest, "alias_immutable",
			"A published model alias is immutable; publish a new alias instead", reqID)
		return
	}
	in.Alias = existing.Alias
	out, err := s.pg.UpdateModelExpected(r.Context(), id, in, expectedVersion)
	if errors.Is(err, storage.ErrConfigVersionConflict) {
		current, _ := s.pg.ModelByID(r.Context(), id)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{
				"code":            "config_version_conflict",
				"message":         "The model changed since it was loaded; current version is " + strconv.FormatInt(current.ConfigVersion, 10),
				"request_id":      reqID,
				"current_version": current.ConfigVersion,
			},
		})
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Model could not be updated", reqID)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// getModelDetail is the aggregate read behind the model detail page: identity,
// all routes and the current configuration version. It performs no upstream
// call and makes no health claim.
func (s *Server) getModelDetail(w http.ResponseWriter, r *http.Request, id, reqID string) {
	m, err := s.pg.ModelByID(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
		return
	}
	routes, err := s.pg.ListRouteViews(r.Context(), storage.RouteViewFilter{ModelID: id, Limit: 500})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load model routes", reqID)
		return
	}
	if routes == nil {
		routes = []model.RouteView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"model":   m,
		"routes":  routes,
		"version": m.ConfigVersion,
	})
}

func (s *Server) handleTenants(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	switch r.Method {
	case http.MethodGet:
		items, err := s.pg.ListTenants(r.Context())
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list tenants", reqID)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items})
	case http.MethodPost:
		var in tenantWriteRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
			return
		}
		v := in.tenant()
		// A create that omits the field gets the column default through
		// validateTenant; an explicit [] survives as "no models".
		if err := validateTenant(&v); err != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_tenant", err, reqID)
			return
		}
		out, err := s.pg.CreateTenant(r.Context(), v)
		if err != nil {
			httpx.Error(w, http.StatusConflict, "tenant_create_failed", "Tenant could not be created", reqID)
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
	stats, err := s.pg.TenantUsageStats(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant usage could not be loaded", reqID)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleTenantByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	id := strings.TrimPrefix(r.URL.Path, "/admin/tenants/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var in tenantWriteRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
			return
		}
		v := in.tenant()
		// An omitted field on an edit means "leave the stored allowlist alone".
		// It must not fall through to the create default: that would turn a
		// rename into a silent widening back to the wildcard. An explicit []
		// still reaches the validator as the deny-all decision.
		if in.AllowedModels != nil {
			if reason := normalizeTenantAllowedModels(in.AllowedModels); reason != "" {
				httpx.Error(w, http.StatusBadRequest, "invalid_tenant", reason, reqID)
				return
			}
			v.AllowedModels = *in.AllowedModels
		}
		if err := validateTenantFields(&v); err != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_tenant", err, reqID)
			return
		}
		out, err := s.pg.UpdateTenant(r.Context(), id, v, in.AllowedModels)
		if err != nil {
			httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found or could not be updated", reqID)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodDelete:
		s.deleteTenant(w, r, id, reqID)
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

// deleteTenant removes a tenant together with the API Keys issued under it.
//
// The keys have to go first: gateway_api_key.tenant_id is a NOT NULL foreign
// key, so the tenant row cannot be removed while any credential still points at
// it. The response reports how many were removed so the console can say what
// the confirmation actually deleted rather than only that it succeeded.
func (s *Server) deleteTenant(w http.ResponseWriter, r *http.Request, id, reqID string) {
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found", reqID)
		return
	}
	deletedKeys, err := s.pg.DeleteTenant(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "tenant_not_found", "Tenant was not found", reqID)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Tenant could not be deleted", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "deleted_keys": deletedKeys})
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
	id := strings.TrimPrefix(r.URL.Path, "/admin/api-keys/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut:
		s.updateAPIKey(w, r, id, reqID)
	case http.MethodDelete:
		// DELETE without a qualifier keeps its original meaning: revoke the
		// credential but keep the row so request history stays resolvable.
		// `?purge=1` is the separate, harder operation — removing the row.
		if isPurgeRequest(r) {
			s.deleteAPIKey(w, r, id, reqID)
			return
		}
		s.revokeAPIKey(w, r, id, reqID)
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) updateAPIKey(w http.ResponseWriter, r *http.Request, id, reqID string) {
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

// isPurgeRequest tells the two DELETE meanings apart. Revocation is the
// default because it is the older contract and the safer mistake: a client that
// forgets the qualifier loses the credential but keeps the history readable.
func isPurgeRequest(r *http.Request) bool {
	v := r.URL.Query().Get("purge")
	return v == "1" || strings.EqualFold(v, "true")
}

// deleteAPIKey removes the key row outright. It is a separate method from
// revokeAPIKey on purpose: the two have different consequences for the record
// trail, and a caller that means the reversible one must not get this by
// accident.
func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request, id, reqID string) {
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "api_key_not_found", "API key was not found", reqID)
		return
	}
	if err := s.pg.DeleteAPIKey(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "api_key_not_found", "API key was not found", reqID)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "database_error", "API key could not be deleted", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "deleted": true})
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
		tenant, err := s.pg.TenantByID(r.Context(), tenantID)
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
			DailyToken:     quotaUsage(stats.DailyTokens, tenant.DailyTokenLimit),
			MonthlyToken:   quotaUsage(stats.MonthlyTokens, tenant.MonthlyTokenLimit),
			MonthlyRequest: quotaUsage(stats.MonthlyRequests, tenant.MonthlyRequestLimit),
		}
		fillQuotaShares(out.ByKey, tenant.MonthlyTokenLimit)
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
	rows, err := s.pg.RequestLogSeries(r.Context(), "", from, to, unit, offset, trendLabelFormat(unit))
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
// this function. CREDENTIAL_ is the same argument for the sealing key: naming
// CREDENTIAL_ENCRYPTION_KEY here would make the gateway send the one key that
// opens every inline Credential to the channel's configured base URL.
// The proxy entries are separate because those variables routinely embed
// "user:password@proxy", and "HTTPS_" is not covered by "HTTP_". The Provider
// transport deliberately ignores the environment proxy (ADR-0012), but naming
// one of these as a channel credential would still send the proxy credential
// upstream as a Bearer token.
var reservedCredentialEnvPrefixes = []string{
	"ADMIN_", "API_KEY_", "AWS_", "AZURE_", "CREDENTIAL_", "DATABASE_", "DATA_PLANE",
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

// validateProvider validates a channel write that must name a credential (a
// create). It is a Server method because the deployment's
// PROVIDER_HTTP_ALLOWLIST is what decides whether a plaintext target is an
// allowed exception; a Server without a configuration fails closed.
//
// The gateway speaks the OpenAI wire format only: the in-process data plane
// sends the OpenAI chat body it received straight to the provider, so a channel
// marked anthropic or gemini would be sent a body it cannot read and fail at
// request time. Refusing it when it is saved is a configuration error instead
// of an outage.
func (s *Server) validateProvider(v *model.Provider) string {
	return validateProviderInput(v, true, s.providerHTTPAllowlist())
}

// validateProviderForUpdate permits a write with no credential at all, which
// means "keep the stored one" — the console sends that when an operator edits
// the base URL and leaves the key field blank.
func (s *Server) validateProviderForUpdate(v *model.Provider) string {
	return validateProviderInput(v, false, s.providerHTTPAllowlist())
}

// providerHTTPAllowlist compiles the configured entries once. A Server built
// without NewServer (a unit test) compiles from its Config literal on demand;
// malformed entries are already refused by LoadConfig, and a direct literal
// with a malformed entry fails closed to "nothing allowed".
func (s *Server) providerHTTPAllowlist() *hostAllowlist {
	if s.httpAllowlist != nil {
		return s.httpAllowlist
	}
	allowlist, _ := parseHostAllowlist(s.cfg.ProviderHTTPAllowlist)
	return allowlist
}

func validateProviderInput(v *model.Provider, requireCredential bool, allowlist *hostAllowlist) string {
	v.Name = strings.TrimSpace(v.Name)
	v.BaseURL = strings.TrimSpace(v.BaseURL)
	v.Protocol = strings.TrimSpace(v.Protocol)
	v.ProviderType = strings.TrimSpace(v.ProviderType)
	v.Description = strings.TrimSpace(v.Description)
	v.CredentialEnv = strings.TrimSpace(v.CredentialEnv)
	v.CredentialSecret = strings.TrimSpace(v.CredentialSecret)
	if v.Name == "" {
		return "name is required"
	}
	if len([]rune(v.ProviderType)) > 32 {
		return "provider_type must be at most 32 characters"
	}
	if len([]rune(v.Description)) > 512 {
		return "description must be at most 512 characters"
	}
	if v.BaseURL == "" {
		return "base_url is required"
	}
	if reason := validateProviderBaseURL(v.BaseURL, allowlist); reason != "" {
		return reason
	}
	// Two sources on one write is ambiguous: it is not clear whether the operator
	// wants to replace an inline key with a variable reference or the reverse.
	if v.CredentialSecret != "" && v.CredentialEnv != "" {
		return "credential_secret and credential_env are mutually exclusive"
	}
	// The namespace guard belongs to the env source only. An inline key is a
	// secret, not a variable name, and is never resolved through os.Getenv.
	if v.CredentialEnv != "" {
		if reason := validateCredentialEnv(v.CredentialEnv); reason != "" {
			return reason
		}
	}
	if requireCredential && v.CredentialSecret == "" && v.CredentialEnv == "" {
		return "credential_secret or credential_env is required"
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

// sealProviderCredential turns the write-only input into the credential fields
// storage persists. An inline key never travels further than this function in
// plaintext: it is sealed here, and the input field is cleared so a response
// built from the same value cannot echo it.
//
// A write with neither credential_secret nor credential_env clears those
// fields, which storage reads as "keep the existing credential" on an update.
func (s *Server) sealProviderCredential(v *model.Provider) error {
	v.CredentialSecret = strings.TrimSpace(v.CredentialSecret)
	v.CredentialEnv = strings.TrimSpace(v.CredentialEnv)
	if v.CredentialSecret != "" {
		sealed, err := security.Seal(s.sealingKey(), []byte(v.CredentialSecret))
		if err != nil {
			return err
		}
		v.CredentialSource = model.CredentialSourceInline
		v.CredentialSecretRef = ""
		v.CredentialCiphertext = sealed
		v.CredentialSecret = ""
		return nil
	}
	if v.CredentialEnv != "" {
		v.CredentialSource = model.CredentialSourceEnv
		v.CredentialSecretRef = v.CredentialEnv
		v.CredentialCiphertext = ""
		return nil
	}
	v.CredentialSource = ""
	v.CredentialSecretRef = ""
	v.CredentialCiphertext = ""
	return nil
}

// providerView builds the read shape of a Provider. It is the only place the
// management API turns a stored credential into client-visible data, so it can
// guarantee the rule once: source, configured, last four — never the secret
// (ADR-0017). An env value is read from the process environment because the
// console is entitled to know the reference is live; only its tail is returned.
func (s *Server) providerView(p model.Provider) model.Provider {
	p.CredentialSecret = ""
	source := p.CredentialSource
	ref := p.CredentialSecretRef
	if source == "" && strings.TrimSpace(p.CredentialEnv) != "" {
		source = model.CredentialSourceEnv
		ref = p.CredentialEnv
	}

	summary := &model.CredentialSummary{SourceType: source}
	switch source {
	case model.CredentialSourceEnv:
		p.CredentialEnv = ref
		value := strings.TrimSpace(os.Getenv(ref))
		summary.Configured = value != ""
		summary.Last4 = credentialLast4(value)
	case model.CredentialSourceInline:
		p.CredentialEnv = ""
		summary.Configured = strings.TrimSpace(p.CredentialCiphertext) != ""
		if plaintext, err := security.Open(s.sealingKey(), p.CredentialCiphertext); err == nil {
			summary.Last4 = credentialLast4(strings.TrimSpace(string(plaintext)))
		}
	default:
		p.CredentialEnv = ""
	}
	p.Credential = summary
	return p
}

// credentialLast4 is the entire disclosure a read response makes about a secret.
// A value shorter than eight characters is masked: returning its tail would
// return the whole secret, which is the one thing the summary must never do.
// An absent value stays empty, because "not configured" is not a disclosure.
func credentialLast4(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) < 8 {
		return "****"
	}
	return string(runes[len(runes)-4:])
}

// validateModel checks a model write. requireBinding is true for a create,
// where the channel and upstream model name the model's first Route and a model
// without one could never serve traffic; both halves are required together.
//
// An update never carries a binding: it changes the alias and lifecycle state
// only, and routing is edited through the Route API. Refusing a binding here
// keeps the API from accepting a field it would silently ignore.
func validateModel(v *model.ModelConfig, requireBinding bool) string {
	v.Alias = strings.TrimSpace(v.Alias)
	v.DisplayName = strings.TrimSpace(v.DisplayName)
	v.Description = strings.TrimSpace(v.Description)
	v.ProviderID = strings.TrimSpace(v.ProviderID)
	v.UpstreamModel = strings.TrimSpace(v.UpstreamModel)
	if v.Alias == "" {
		return "alias is required"
	}
	if len([]rune(v.Alias)) > 128 {
		return "alias must be at most 128 characters"
	}
	if len([]rune(v.DisplayName)) > 128 {
		return "display_name must be at most 128 characters"
	}
	if len([]rune(v.Description)) > 1024 {
		return "description must be at most 1024 characters"
	}
	if requireBinding {
		if v.ProviderID == "" || v.UpstreamModel == "" {
			return "provider_id and upstream_model are required to create the model's first route"
		}
	} else if v.ProviderID != "" || v.UpstreamModel != "" {
		return "a model edit changes only alias and status; edit a Route to change routing"
	}
	if v.Status == 0 {
		v.Status = 1
	}
	if v.Status != 1 && v.Status != 2 {
		return "status must be 1 (enabled) or 2 (disabled)"
	}
	return ""
}

// tenantWriteRequest is the decode boundary for the two Tenant writes.
//
// It exists to tell an omitted allowed_models from an explicit []. The Tenant
// struct cannot: both decode to a nil slice, but on an edit they mean opposite
// things — "keep the stored allowlist" versus "deny every model". The outer
// pointer field is shallower than the embedded Tenant's same-named field, so
// encoding/json selects it and a present key always leaves a non-nil pointer
// (even for []); an absent or null key leaves it nil.
type tenantWriteRequest struct {
	model.Tenant
	AllowedModels *[]string `json:"allowed_models"`
}

// tenant projects the request onto the storage type. An omitted allowlist
// stays nil here; each endpoint decides what nil means for its operation.
func (in tenantWriteRequest) tenant() model.Tenant {
	v := in.Tenant
	if in.AllowedModels != nil {
		v.AllowedModels = *in.AllowedModels
	}
	return v
}

func validateTenant(v *model.Tenant) string {
	if reason := normalizeTenantAllowedModels(&v.AllowedModels); reason != "" {
		return reason
	}
	return validateTenantFields(v)
}

// validateTenantFields checks everything except the model allowlist. Update
// calls this directly when the field was omitted, so validation cannot smuggle
// the create-time wildcard default back into an edit.
func validateTenantFields(v *model.Tenant) string {
	v.Code = strings.TrimSpace(v.Code)
	v.Name = strings.TrimSpace(v.Name)
	if v.Code == "" || v.Name == "" {
		return "code and name are required"
	}
	if v.Status == 0 {
		v.Status = 1
	}
	if v.Status != 1 && v.Status != 2 {
		return "status must be 1 (enabled) or 2 (disabled)"
	}
	if v.DailyTokenLimit < 0 || v.MonthlyTokenLimit < 0 || v.MonthlyRequestLimit < 0 || v.MonthlyBudgetMicros < 0 {
		return "tenant quota values cannot be negative"
	}
	if v.RPMLimit < 0 || v.TPMLimit < 0 || v.MaxConcurrency < 0 {
		return "tenant rate limit values cannot be negative"
	}
	return ""
}

// normalizeTenantAllowedModels applies the Tenant default before sharing the
// key path's rules.
//
// A nil list is an omitted field, not the empty-list decision, so it becomes
// the wildcard: that is the stored column default and the meaning every
// pre-existing Tenant already has. An explicit [] stays empty and keeps
// denying every model. Everything else — a lone wildcard, a sorted deduplicated
// list, rejection of blank entries and of wildcard-plus-names — is the same
// normalisation a key performs, so the two scopes cannot drift apart.
func normalizeTenantAllowedModels(list *[]string) string {
	if list == nil || *list == nil {
		*list = []string{model.AllModelsWildcard}
		return ""
	}
	return normalizeAllowedModels(list)
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
