package app

import (
	"context"
	"encoding/csv"
	"encoding/json"
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

func (s *Server) adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")
		principal, err := s.authenticateAdmin(r)
		if err != nil {
			status := http.StatusUnauthorized
			code := "invalid_admin_auth"
			if strings.Contains(err.Error(), "disabled") || strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "unsupported") {
				status = http.StatusServiceUnavailable
				code = "admin_api_disabled"
			}
			httpx.Error(w, status, code, err.Error(), reqID)
			return
		}
		if !principal.can(r) {
			httpx.Error(w, http.StatusForbidden, "admin_forbidden", "Role is not allowed to perform this admin operation", reqID)
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
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = s.pg.InsertAdminAudit(ctx, model.AdminAuditLog{
				RequestID: reqID,
				Actor: principal.User,
				Action: r.Method,
				Resource: r.URL.Path,
				StatusCode: status,
				ClientIP: clientIP(r),
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
	case strings.HasPrefix(r.URL.Path, "/admin/tenants/"):
		s.handleTenantByID(w, r)
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
	writeJSON(w, http.StatusOK, map[string]any{"user": p.User, "roles": roles, "auth_mode": s.cfg.AdminAuthMode})
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
		"gateway":    true,
		"postgres":   dbOK,
		"redis":      redisOK,
		"thinkwatch": thinkwatchOK,
		"stats":      stats,
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
	switch r.Method {
	case http.MethodGet:
		items, err := s.pg.ListTenants(r.Context())
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
		out, err := s.pg.CreateTenant(r.Context(), in)
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
		Limit: limit,
		Offset: offset,
		Model: strings.TrimSpace(r.URL.Query().Get("model")),
		Provider: strings.TrimSpace(r.URL.Query().Get("provider")),
		Status: status,
		RequestID: strings.TrimSpace(r.URL.Query().Get("request_id")),
		Failover: failover,
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
	_ = cw.Write([]string{"created_at","request_id","tenant_id","requested_model","actual_model","provider","upstream_model","route_priority","attempts","failover","tokens","latency_ms","status_code","error_code"})
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

func validateTenant(v *model.Tenant) string {
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
