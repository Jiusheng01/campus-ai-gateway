package app

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
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
	case r.URL.Path == "/admin/providers":
		s.handleProviders(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/providers/"):
		s.handleProviderByID(w, r)
	case r.URL.Path == "/admin/models":
		s.handleModels(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/models/"):
		s.handleModelByID(w, r)
	default:
		http.NotFound(w, r)
	}
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
