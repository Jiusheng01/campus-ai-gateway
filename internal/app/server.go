package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"sort"
	"strconv"
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
	cfg       Config
	pg        *storage.Postgres
	redis     *storage.Redis
	limit     *ratelimit.Limiter
	proxy     http.Handler
	mux       *http.ServeMux
	adminAuth *adminAuthService
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
	if cfg.SessionAuthEnabled() {
		loginLimiter := ratelimit.NewAdminLoginLimiter(rd.Client).
			WithPolicy(cfg.AdminLoginMaxAttempts, cfg.AdminLoginWindow)
		sessionKey, derived := cfg.AdminSessionKey()
		if derived {
			log.Printf("ADMIN_SESSION_SECRET is not configured; derived development key is being used. " +
				"Set ADMIN_SESSION_SECRET (>= 32 bytes, e.g. `openssl rand -base64 48`) in production.")
		}
		s.adminAuth = newAdminAuthService(pg, loginLimiter, sessionKey, cfg)
	}
	s.routes()
	s.bootstrapAdminUser(ctx)
	return s, nil
}

// bootstrapAdminUser creates an initial administrator from ADMIN_BOOTSTRAP_USERNAME
// / ADMIN_BOOTSTRAP_PASSWORD when no account exists yet. It exists so an
// unattended deployment can come up without an operator at the keyboard; the
// interactive /admin/setup flow remains the default. A failure is logged and
// never blocks startup, so a bad bootstrap value cannot take the gateway down.
func (s *Server) bootstrapAdminUser(ctx context.Context) {
	if s.adminAuth == nil {
		return
	}
	username := s.cfg.AdminBootstrapUsername
	password := s.cfg.AdminBootstrapPassword
	if username == "" || password == "" {
		return
	}
	needsSetup, err := s.adminAuth.needsSetup(ctx)
	if err != nil {
		log.Printf("admin bootstrap: cannot count administrators: %v", err)
		return
	}
	if !needsSetup {
		return
	}
	if _, err := s.adminAuth.setup(ctx, username, "Administrator", password); err != nil {
		log.Printf("admin bootstrap: could not create the initial administrator: %v", err)
		return
	}
	log.Printf("admin bootstrap: created the initial administrator %q", username)
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
	s.mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if s.pg.Pool.Ping(ctx) != nil || s.redis.Client.Ping(ctx).Err() != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	s.mux.HandleFunc("/metrics", s.handleMetrics)
	s.mux.Handle("/v1/", s.authMiddleware(http.HandlerFunc(s.handleV1)))
	s.mux.HandleFunc("/admin-ui", s.serveAdminUI)
	s.mux.HandleFunc("/admin-ui/", s.serveAdminUI)

	// Public authentication endpoints. They must be registered with an exact
	// pattern so they win over the catch-all "/admin/" registration below,
	// which is wrapped in adminMiddleware.
	s.mux.HandleFunc("/admin/auth-config", s.handleAdminAuthConfig)
	s.mux.HandleFunc("/admin/login", s.handleAdminLogin)
	s.mux.HandleFunc("/admin/setup", s.handleAdminSetup)
	s.mux.HandleFunc("/admin/auth/refresh", s.handleAdminRefresh)
	s.mux.HandleFunc("/admin/auth/logout", s.handleAdminLogout)
	s.mux.HandleFunc("/admin/logout", s.handleAdminLogout)

	s.mux.Handle("/admin/password", s.adminMiddleware(http.HandlerFunc(s.handleAdminPasswordChange)))
	s.mux.Handle("/admin/sessions", s.adminMiddleware(http.HandlerFunc(s.handleAdminSessions)))
	s.mux.Handle("/admin/sessions/", s.adminMiddleware(http.HandlerFunc(s.handleAdminSessionByID)))
	// Account administration. No role other than super_admin is granted these
	// paths in adminPrincipal.can(), so RBAC alone keeps everyone else out.
	s.mux.Handle("/admin/admin-users", s.adminMiddleware(http.HandlerFunc(s.handleAdminUsers)))
	s.mux.Handle("/admin/admin-users/", s.adminMiddleware(http.HandlerFunc(s.handleAdminUserByID)))
	s.mux.Handle("/admin/thinkwatch/sync", s.adminMiddleware(http.HandlerFunc(s.handleThinkWatchSync)))
	s.mux.Handle("/admin/model-test", s.adminMiddleware(http.HandlerFunc(s.handleModelTest)))
	s.mux.Handle("/admin/", s.adminMiddleware(http.HandlerFunc(s.handleAdmin)))
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	stats, err := s.pg.DashboardStats(ctx)
	if err != nil {
		http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintf(w, "# HELP campus_gateway_requests_today Requests recorded since local database day boundary.\n")
	_, _ = fmt.Fprintf(w, "# TYPE campus_gateway_requests_today gauge\ncampus_gateway_requests_today %d\n", stats.TodayRequests)
	_, _ = fmt.Fprintf(w, "# TYPE campus_gateway_tokens_today gauge\ncampus_gateway_tokens_today %d\n", stats.TodayTokens)
	_, _ = fmt.Fprintf(w, "# TYPE campus_gateway_errors_today gauge\ncampus_gateway_errors_today %d\n", stats.TodayErrors)
	_, _ = fmt.Fprintf(w, "# TYPE campus_gateway_latency_avg_ms gauge\ncampus_gateway_latency_avg_ms %.3f\n", stats.AvgLatencyMS)
	_, _ = fmt.Fprintf(w, "# TYPE campus_gateway_latency_p95_ms gauge\ncampus_gateway_latency_p95_ms %.3f\n", stats.P95LatencyMS)
	_, _ = fmt.Fprintf(w, "# TYPE campus_gateway_failover_today gauge\ncampus_gateway_failover_today %d\n", stats.FailoverCount)
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
			s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, EventType: "invalid_api_key", ClientIP: s.clientIPFrom(r), Detail: "missing bearer token"})
			httpx.Error(w, http.StatusUnauthorized, "invalid_api_key", "Missing bearer token", reqID)
			return
		}

		plain := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		k, err := s.pg.FindAPIKeyByDigest(r.Context(), security.Digest(s.cfg.KeyHMACSecret, plain))
		if err != nil || k.Status != 1 {
			s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, EventType: "invalid_api_key", ClientIP: s.clientIPFrom(r), Detail: "unknown or disabled key"})
			httpx.Error(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API key", reqID)
			return
		}
		if k.ExpireAt != nil && time.Now().After(*k.ExpireAt) {
			s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: k.TenantID, APIKeyID: k.ID, EventType: "api_key_expired", ClientIP: s.clientIPFrom(r)})
			httpx.Error(w, http.StatusUnauthorized, "api_key_expired", "API key expired", reqID)
			return
		}
		if len(k.AllowedIPs) > 0 && !ipAllowed(s.clientIPFrom(r), k.AllowedIPs) {
			s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: k.TenantID, APIKeyID: k.ID, EventType: "ip_forbidden", ClientIP: s.clientIPFrom(r)})
			httpx.Error(w, http.StatusForbidden, "ip_not_allowed", "Client IP is not allowed", reqID)
			return
		}

		// Resolve the tenant hierarchy. The key's tenant is a leaf, and the
		// budgets that actually bind are the ones along the path from the root
		// down to it. The path came back with the key, so this is one
		// primary-key lookup rather than a recursive walk.
		chain, err := s.pg.FindTenantChainByPath(r.Context(), k.TenantPath)
		if err != nil || len(chain) == 0 {
			s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: k.TenantID, APIKeyID: k.ID, EventType: "invalid_api_key", ClientIP: s.clientIPFrom(r), Detail: "tenant hierarchy unavailable"})
			httpx.Error(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API key", reqID)
			return
		}
		tenantChain := storage.ResolveQuotas(chain)

		// Model access is an intersection, not an inheritance: the key's own
		// allowlist narrows the tenant chain's, and a tenant can never widen
		// what an ancestor allows. Either side being absent means "no
		// constraint from that side", which is why the restricted flags are
		// tracked separately from the sets.
		tenantAccess, tenantAccessRestricted := storage.ResolveAccess(chain)
		keyAccess := storage.StringSet(k.AllowedModels)
		effectiveAccess, accessRestricted := storage.ComposeAccess(
			keyAccess, len(k.AllowedModels) > 0,
			tenantAccess, tenantAccessRestricted,
		)

		// The leaf of the chain carries the already-inherited limits, so the
		// per-tenant fields keep their plain int64 shape and no nil-checking
		// leaks into the data plane.
		leaf := tenantChain[len(tenantChain)-1]
		id := model.Identity{
			TenantID:              k.TenantID,
			APIKeyID:              k.ID,
			AllowedModels:         effectiveAccess,
			ModelAccessRestricted: accessRestricted,
			AllowedIPs:            k.AllowedIPs,
			RPMLimit:              k.RPMLimit,
			TPMLimit:              k.TPMLimit,
			MaxConcurrency:        k.MaxConcurrency,
			RouteGroup:            k.RouteGroup,
			DailyTokenLimit:       leaf.DailyTokenLimit,
			MonthlyTokenLimit:     leaf.MonthlyTokenLimit,
			MonthlyRequestLimit:   leaf.MonthlyRequestLimit,
			TenantChain:           tenantChain,
		}

		s.pg.TouchAPIKey(r.Context(), k.ID)
		r.Header.Del("Authorization")
		next.ServeHTTP(w, r.WithContext(httpx.WithIdentity(r.Context(), id)))
	})
}

func (s *Server) handleV1(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	reqID := r.Header.Get("X-Request-Id")
	id, ok := httpx.IdentityFrom(r.Context())
	if !ok {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Gateway identity is unavailable", reqID)
		return
	}

	aw := &auditResponseWriter{ResponseWriter: w}
	var envelope requestEnvelope
	var reservation *ratelimit.TPMReservation
	var tenantReservation *ratelimit.TenantChainReservation
	defer func() {
		actualModel, inputTokens, outputTokens, totalTokens := parseUsage(aw.capture.Bytes(), envelope.Stream)
		status := aw.status
		if status == 0 {
			status = http.StatusOK
		}
		telemetry := parseRouteTelemetry(aw.Header())
		if telemetry.UpstreamModel == "" && actualModel != "" && actualModel != envelope.Model {
			telemetry.UpstreamModel = actualModel
		}
		log := model.RequestLog{
			RequestID:      reqID,
			TenantID:       id.TenantID,
			APIKeyID:       id.APIKeyID,
			ClientIP:       s.clientIPFrom(r),
			RequestedModel: envelope.Model,
			ActualModel:    actualModel,
			Provider:       telemetry.Provider,
			UpstreamModel:  telemetry.UpstreamModel,
			RoutePriority:  telemetry.RoutePriority,
			Attempts:       telemetry.Attempts,
			Failover:       telemetry.Failover(),
			Stream:         envelope.Stream,
			InputTokens:    inputTokens,
			OutputTokens:   outputTokens,
			TotalTokens:    totalTokens,
			LatencyMS:      time.Since(started).Milliseconds(),
			StatusCode:     status,
			ErrorCode:      parseErrorCode(aw.capture.Bytes()),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if totalTokens > 0 {
			if reservation != nil {
				_ = reservation.Reconcile(ctx, totalTokens)
			}
			if tenantReservation != nil {
				_ = tenantReservation.Reconcile(ctx, totalTokens)
			}
		} else {
			// No usage was reported: the upstream failed, a streamed response
			// carried no usage chunk, or the body could not be parsed. Release the
			// reservation rather than leaving it held -- otherwise the tenant's
			// daily/monthly bucket keeps the estimate until the bucket expires
			// (3 days for the daily key, 40 days for the monthly one).
			if reservation != nil {
				_ = reservation.Refund(ctx)
			}
			if tenantReservation != nil {
				_ = tenantReservation.Refund(ctx)
			}
		}
		_ = s.pg.InsertRequestLog(ctx, log)
	}()

	// The monthly request cap is enforced at every level of the tenant chain, so
	// a request counts against the leaf and against each of its ancestors. The
	// level that rejects is named in the event so an operator can tell whose
	// budget ran out.
	allowed, requestRejection, err := s.limit.AllowTenantRequestChain(r.Context(), id.TenantChain)
	if err != nil || !allowed {
		tenantID, detail := "", ""
		if requestRejection != nil {
			tenantID, detail = requestRejection.TenantID, "level="+requestRejection.Label
		}
		s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: tenantID, APIKeyID: id.APIKeyID, EventType: "tenant_request_quota_exceeded", ClientIP: s.clientIPFrom(r), Detail: detail})
		httpx.Error(aw, http.StatusTooManyRequests, "tenant_request_quota_exceeded", "Tenant monthly request quota exceeded", reqID)
		return
	}

	allowed, err = s.limit.AllowRPM(r.Context(), id.APIKeyID, id.RPMLimit)
	if err != nil || !allowed {
		s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: id.TenantID, APIKeyID: id.APIKeyID, EventType: "rpm_exceeded", ClientIP: s.clientIPFrom(r)})
		httpx.Error(aw, http.StatusTooManyRequests, "rate_limit_exceeded", "Request rate limit exceeded", reqID)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		// Restricted means an allowlist applies -- possibly an empty one, which
		// is how "deny everything" is expressed. Returning the upstream catalog
		// in that case would advertise models the caller cannot use.
		if id.ModelAccessRestricted {
			writeAllowedModels(aw, id.AllowedModels)
			return
		}
		s.proxy.ServeHTTP(aw, r)
		return
	}

	maxBody := int64(16 << 20)
	if r.URL.Path == "/v1/audio/transcriptions" {
		maxBody = 64 << 20
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		httpx.Error(aw, http.StatusBadRequest, "invalid_request", "Unable to read request body", reqID)
		return
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))

	if len(body) > 0 {
		parsed, err := parseRequestEnvelope(body, r.Header.Get("Content-Type"))
		if err != nil {
			httpx.Error(aw, http.StatusBadRequest, "invalid_request", "Request body could not be parsed", reqID)
			return
		}
		envelope = parsed
	}
	if requiresModel(r.URL.Path) && envelope.Model == "" {
		httpx.Error(aw, http.StatusBadRequest, "model_required", "Model is required", reqID)
		return
	}
	if envelope.Model != "" && id.ModelAccessRestricted {
		if _, ok := id.AllowedModels[envelope.Model]; !ok {
			s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: id.TenantID, APIKeyID: id.APIKeyID, EventType: "model_forbidden", ClientIP: s.clientIPFrom(r), RequestedModel: envelope.Model})
			httpx.Error(aw, http.StatusForbidden, "model_not_allowed", "Model is not allowed for this API key", reqID)
			return
		}
	}

	// The estimate is reserved against the whole tenant chain, not just the
	// leaf, so a parent's budget caps its entire subtree without any separate
	// rollup step.
	//
	// When the request names a model, the per-model chain joins the same call.
	// The two axes are ANDed, and doing it in one script preserves the
	// guarantee that a rejection writes nothing at all: a request that would
	// drain the campus-wide pool for a model must not leave the tenant's own
	// counters incremented.
	reserveAmount := estimateTPM(body, envelope)

	levels := ratelimit.TenantQuotaLevels(id.TenantChain)
	if envelope.Model != "" {
		modelLevels, err := s.pg.ResolveModelQuotaLevels(r.Context(), id.TenantChain, envelope.Model)
		if err != nil {
			httpx.Error(aw, http.StatusInternalServerError, "database_error", "Unable to resolve the model quota", reqID)
			return
		}
		// The model axis is appended after the tenant axis, so the shared pool
		// sits last in the chain. The script reports the first level that
		// rejects, so when both would refuse the caller hears about their own
		// budget first -- that is the one they can act on, and it keeps the
		// shared pool's state out of a refusal the tenant cannot influence.
		levels = append(levels, ratelimit.ModelQuotaLevels(modelLevels)...)
	}

	var rejection *ratelimit.QuotaRejection
	tenantReservation, allowed, rejection, err = s.limit.ReserveTokenChain(r.Context(), levels, reserveAmount)
	if err != nil || !allowed {
		// The rejection is reported as a model event only when a model pool is
		// what refused. Collapsing the two into one event type would hide the
		// single most useful fact about the refusal -- whether the caller ran
		// out of their own budget or of the shared one.
		eventType := "tenant_token_quota_exceeded"
		message := "Tenant token quota exceeded"
		tenantID := ""
		detail := ""
		if rejection != nil {
			tenantID = rejection.TenantID
			detail = "level=" + rejection.Label
			if rejection.ModelAlias != "" {
				eventType = "model_token_quota_exceeded"
				message = "Model token quota exceeded"
			}
		}
		s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: tenantID, APIKeyID: id.APIKeyID, EventType: eventType, ClientIP: s.clientIPFrom(r), RequestedModel: envelope.Model, Detail: detail})
		httpx.Error(aw, http.StatusTooManyRequests, eventType, message, reqID)
		return
	}
	reservation, allowed, err = s.limit.ReserveTPM(r.Context(), id.APIKeyID, reserveAmount, id.TPMLimit)
	if err != nil || !allowed {
		_ = tenantReservation.Refund(r.Context())
		s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: id.TenantID, APIKeyID: id.APIKeyID, EventType: "tpm_exceeded", ClientIP: s.clientIPFrom(r), RequestedModel: envelope.Model})
		httpx.Error(aw, http.StatusTooManyRequests, "token_limit_exceeded", "Token rate limit exceeded", reqID)
		return
	}

	release, allowed, err := s.limit.AcquireConcurrency(r.Context(), id.APIKeyID, id.MaxConcurrency)
	if err != nil || !allowed {
		_ = reservation.Refund(r.Context())
		_ = tenantReservation.Refund(r.Context())
		s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: id.TenantID, APIKeyID: id.APIKeyID, EventType: "concurrency_exceeded", ClientIP: s.clientIPFrom(r), RequestedModel: envelope.Model})
		httpx.Error(aw, http.StatusTooManyRequests, "concurrency_limit_exceeded", "Concurrency limit exceeded", reqID)
		return
	}
	defer release()

	s.proxy.ServeHTTP(aw, r)
}

func parseRequestEnvelope(body []byte, contentType string) (requestEnvelope, error) {
	var out requestEnvelope
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err == nil && strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return out, fmt.Errorf("multipart boundary is missing")
		}
		mr := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return out, err
			}
			if part.FormName() == "model" {
				value, err := io.ReadAll(io.LimitReader(part, 1024))
				_ = part.Close()
				if err != nil {
					return out, err
				}
				out.Model = strings.TrimSpace(string(value))
				break
			}
			_ = part.Close()
		}
		return out, nil
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, err
	}
	return out, nil
}

func requiresModel(path string) bool {
	switch path {
	case "/v1/chat/completions", "/v1/responses", "/v1/embeddings", "/v1/audio/transcriptions":
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

func parseUsage(body []byte, stream bool) (modelName string, input, output, total int64) {
	type usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		InputTokens      int64 `json:"input_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		OutputTokens     int64 `json:"output_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
	}
	type response struct {
		Model string `json:"model"`
		Usage *usage `json:"usage"`
	}

	apply := func(raw []byte) {
		var v response
		if json.Unmarshal(raw, &v) != nil {
			return
		}
		if v.Model != "" {
			modelName = v.Model
		}
		if v.Usage != nil {
			input = v.Usage.PromptTokens
			if v.Usage.InputTokens > input {
				input = v.Usage.InputTokens
			}
			output = v.Usage.CompletionTokens
			if v.Usage.OutputTokens > output {
				output = v.Usage.OutputTokens
			}
			total = v.Usage.TotalTokens
			if total == 0 {
				total = input + output
			}
		}
	}

	if !stream {
		apply(body)
		return
	}
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		apply(payload)
	}
	return
}

type routeTelemetry struct {
	Provider      string
	UpstreamModel string
	RoutePriority int
	Attempts      int
}

// Failover reports whether ThinkWatch actually fell back to another route.
// It is only derived from real attempt telemetry, never from planned routes.
func (t routeTelemetry) Failover() bool {
	return t.Attempts > 1
}

func parseRouteTelemetry(h http.Header) routeTelemetry {
	v := routeTelemetry{
		Provider:      strings.TrimSpace(h.Get("X-ThinkWatch-Provider")),
		UpstreamModel: strings.TrimSpace(h.Get("X-ThinkWatch-Upstream-Model")),
	}
	// Compatibility fallback: current ThinkWatch Core exposes the resolved
	// provider name as X-ThinkWatch-Upstream. It is a provider, not an upstream
	// model, so it must not be read into UpstreamModel.
	if v.Provider == "" {
		v.Provider = strings.TrimSpace(h.Get("X-ThinkWatch-Upstream"))
	}
	if n, err := strconv.Atoi(strings.TrimSpace(h.Get("X-ThinkWatch-Route-Priority"))); err == nil && n > 0 {
		v.RoutePriority = n
	}
	if n, err := strconv.Atoi(strings.TrimSpace(h.Get("X-ThinkWatch-Attempts"))); err == nil && n > 0 {
		v.Attempts = n
	}
	return v
}

func parseErrorCode(body []byte) string {
	var v struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &v) == nil {
		return v.Error.Code
	}
	return ""
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

func (s *Server) recordSecurityEvent(r *http.Request, event model.SecurityEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = s.pg.InsertSecurityEvent(ctx, event)
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
