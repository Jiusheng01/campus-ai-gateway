package app

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
)

func (s *Server) adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")
		if s.cfg.AdminToken == "" {
			httpx.Error(w, http.StatusServiceUnavailable, "admin_api_disabled", "Admin API is disabled", reqID)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			httpx.Error(w, http.StatusUnauthorized, "invalid_admin_token", "Missing admin bearer token", reqID)
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.AdminToken)) != 1 {
			httpx.Error(w, http.StatusUnauthorized, "invalid_admin_token", "Invalid admin token", reqID)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/admin/status":
		s.handleAdminStatus(w, r)
	case r.URL.Path == "/admin/providers":
		s.handleProviders(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/providers/"):
		s.handleProviderByID(w, r)
	case r.URL.Path == "/admin/models":
		s.handleModels(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/models/"):
		s.handleModelByID(w, r)
	case r.URL.Path == "/admin/tenants":
		s.handleTenants(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/tenants/"):
		s.handleTenantByID(w, r)
	case r.URL.Path == "/admin/api-keys":
		s.handleAPIKeys(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/api-keys/"):
		s.handleAPIKeyByID(w, r)
	case r.URL.Path == "/admin/request-logs":
		s.handleRequestLogs(w, r)
	default:
		http.NotFound(w, r)
	}
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

func (s *Server) handleRequestLogs(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	items, err := s.pg.ListRequestLogs(r.Context(), limit)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list request logs", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
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
