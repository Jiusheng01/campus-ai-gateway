package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/proxy"
	"github.com/Jiusheng01/campus-ai-gateway/internal/ratelimit"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/google/uuid"
)

type Server struct {
	cfg   Config
	pg    *storage.Postgres
	redis *storage.Redis
	limit *ratelimit.Limiter
	proxy http.Handler
	mux   *http.ServeMux
}

type requestEnvelope struct {
	Model               string `json:"model"`
	Stream              bool   `json:"stream"`
	MaxTokens           int64  `json:"max_tokens"`
	MaxCompletionTokens int64  `json:"max_completion_tokens"`
	MaxOutputTokens     int64  `json:"max_output_tokens"`
}

func NewServer(cfg Config) (*Server, error) {
	ctx := context.Background()
	pg, err := storage.OpenPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	rd, err := storage.OpenRedis(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		pg.Close()
		return nil, err
	}
	rp, err := proxy.NewThinkWatchProxy(cfg.ThinkWatchURL, cfg.ThinkWatchKey)
	if err != nil {
		pg.Close()
		_ = rd.Close()
		return nil, err
	}

	s := &Server{
		cfg:   cfg,
		pg:    pg,
		redis: rd,
		limit: ratelimit.New(rd.Client),
		proxy: rp,
		mux:   http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

func (s *Server) Close() {
	s.pg.Close()
	_ = s.redis.Close()
}

func (s *Server) Handler() http.Handler {
	return requestIDMiddleware(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	s.mux.Handle("/v1/", s.authMiddleware(http.HandlerFunc(s.handleV1)))
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		w.Header().Set("X-Request-Id", id)
		r.Header.Set("X-Request-Id", id)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			httpx.Error(w, http.StatusUnauthorized, "invalid_api_key", "Missing bearer token", reqID)
			return
		}

		plain := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		k, err := s.pg.FindAPIKeyByDigest(r.Context(), security.Digest(s.cfg.KeyHMACSecret, plain))
		if err != nil || k.Status != 1 {
			httpx.Error(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API key", reqID)
			return
		}
		if k.ExpireAt != nil && time.Now().After(*k.ExpireAt) {
			httpx.Error(w, http.StatusUnauthorized, "api_key_expired", "API key expired", reqID)
			return
		}
		if len(k.AllowedIPs) > 0 && !ipAllowed(clientIP(r), k.AllowedIPs) {
			httpx.Error(w, http.StatusForbidden, "ip_not_allowed", "Client IP is not allowed", reqID)
			return
		}

		allowed := make(map[string]struct{}, len(k.AllowedModels))
		for _, m := range k.AllowedModels {
			allowed[m] = struct{}{}
		}
		id := model.Identity{
			TenantID:       k.TenantID,
			APIKeyID:       k.ID,
			AllowedModels:  allowed,
			AllowedIPs:     k.AllowedIPs,
			RPMLimit:       k.RPMLimit,
			TPMLimit:       k.TPMLimit,
			MaxConcurrency: k.MaxConcurrency,
			RouteGroup:     k.RouteGroup,
		}

		s.pg.TouchAPIKey(r.Context(), k.ID)
		r.Header.Del("Authorization")
		next.ServeHTTP(w, r.WithContext(httpx.WithIdentity(r.Context(), id)))
	})
}

func (s *Server) handleV1(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	id, ok := httpx.IdentityFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Gateway identity is unavailable", reqID)
		return
	}

	allowed, err := s.limit.AllowRPM(r.Context(), id.APIKeyID, id.RPMLimit)
	if err != nil || !allowed {
		httpx.Error(w, http.StatusTooManyRequests, "rate_limit_exceeded", "Request rate limit exceeded", reqID)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		if len(id.AllowedModels) > 0 {
			writeAllowedModels(w, id.AllowedModels)
			return
		}
		s.proxy.ServeHTTP(w, r)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Unable to read request body", reqID)
		return
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))

	var envelope requestEnvelope
	if len(body) > 0 {
		if err := json.Unmarshal(body, &envelope); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
			return
		}
	}
	if requiresModel(r.URL.Path) && envelope.Model == "" {
		httpx.Error(w, http.StatusBadRequest, "model_required", "Model is required", reqID)
		return
	}
	if envelope.Model != "" && len(id.AllowedModels) > 0 {
		if _, ok := id.AllowedModels[envelope.Model]; !ok {
			httpx.Error(w, http.StatusForbidden, "model_not_allowed", "Model is not allowed for this API key", reqID)
			return
		}
	}

	reserveAmount := estimateTPM(body, envelope)
	reservation, allowed, err := s.limit.ReserveTPM(r.Context(), id.APIKeyID, reserveAmount, id.TPMLimit)
	if err != nil || !allowed {
		httpx.Error(w, http.StatusTooManyRequests, "token_limit_exceeded", "Token rate limit exceeded", reqID)
		return
	}

	release, allowed, err := s.limit.AcquireConcurrency(r.Context(), id.APIKeyID, id.MaxConcurrency)
	if err != nil || !allowed {
		_ = reservation.Refund(r.Context())
		httpx.Error(w, http.StatusTooManyRequests, "concurrency_limit_exceeded", "Concurrency limit exceeded", reqID)
		return
	}
	defer release()

	s.proxy.ServeHTTP(w, r)
}

func requiresModel(path string) bool {
	switch path {
	case "/v1/chat/completions", "/v1/responses", "/v1/embeddings":
		return true
	default:
		return false
	}
}

func estimateTPM(body []byte, envelope requestEnvelope) int64 {
	if len(body) == 0 {
		return 0
	}
	outputBudget := envelope.MaxTokens
	if envelope.MaxCompletionTokens > outputBudget {
		outputBudget = envelope.MaxCompletionTokens
	}
	if envelope.MaxOutputTokens > outputBudget {
		outputBudget = envelope.MaxOutputTokens
	}
	estimatedInput := int64(len(body)/4 + 1)
	return estimatedInput + outputBudget
}

func writeAllowedModels(w http.ResponseWriter, allowed map[string]struct{}) {
	models := make([]string, 0, len(allowed))
	for name := range allowed {
		models = append(models, name)
	}
	sort.Strings(models)

	data := make([]map[string]any, 0, len(models))
	for _, name := range models {
		data = append(data, map[string]any{
			"id":       name,
			"object":   "model",
			"owned_by": "campus-ai-gateway",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
	})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func ipAllowed(ip string, allowed []string) bool {
	parsed := net.ParseIP(ip)
	for _, rule := range allowed {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		if strings.Contains(rule, "/") {
			_, cidr, err := net.ParseCIDR(rule)
			if err == nil && parsed != nil && cidr.Contains(parsed) {
				return true
			}
		} else if rule == ip {
			return true
		}
	}
	return false
}
