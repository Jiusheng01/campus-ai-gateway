package app

import (
	"bufio"
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

const auditCaptureLimit = 1 << 20

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

type auditResponseWriter struct {
	http.ResponseWriter
	status  int
	capture bytes.Buffer
}

func (w *auditResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *auditResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.capture.Len() < auditCaptureLimit {
		remaining := auditCaptureLimit - w.capture.Len()
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = w.capture.Write(p[:remaining])
	}
	return w.ResponseWriter.Write(p)
}

func (w *auditResponseWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *auditResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

func (w *auditResponseWriter) Push(target string, opts *http.PushOptions) error {
	p, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return p.Push(target, opts)
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
	s.mux.HandleFunc("/admin-ui", s.serveAdminUI)
	s.mux.HandleFunc("/admin-ui/", s.serveAdminUI)
	s.mux.Handle("/admin/", s.adminMiddleware(http.HandlerFunc(s.handleAdmin)))
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