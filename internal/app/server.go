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
	"sync"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
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

	// upstream is the data plane: the in-process handler in direct_proxy.go.
	// Everything in front of it — authentication, quotas, rate limits, request
	// logging — is identical for every request.
	upstream  http.Handler
	mux       *http.ServeMux
	adminAuth *adminAuthService

	// Direct data plane state.
	//
	// routeCache is rebuilt at most once per routeCacheTTL, so a saved change
	// reaches business traffic within seconds without putting three queries on
	// the hot path and without every write handler having to remember to
	// invalidate anything.
	routeMu    sync.Mutex
	routeCache routeTable
	routeAt    time.Time
	transport  *transportPool

	// selector advances the smooth-weighted-round-robin rotation that picks
	// which line of a priority band starts a request. In-memory scheduling
	// state; losing it on restart only resets the rotation.
	selector *routeSelector

	// breaker is the per-provider circuit: a channel that keeps failing is
	// skipped for a cooldown instead of being tried on every request.
	breaker *circuitBreaker

	// credentialSealingKey seals inline provider credentials at write time and
	// opens them at request time (ADR-0017). It is fixed for the process: a key
	// change is a restart, which is also why the fallback warning is emitted once
	// at startup.
	credentialSealingKey []byte

	// httpAllowlist is the compiled PROVIDER_HTTP_ALLOWLIST. It is fixed for the
	// process and consulted whenever a Provider target is saved.
	httpAllowlist *hostAllowlist

	// egress is the dial-time address policy (ADR-0012). NewServer sets the
	// strict policy. It is an unexported field with no environment or Config
	// switch on purpose: the only way to relax it is the test helper's direct
	// injection, so production cannot be talked out of it by configuration.
	egress egressPolicy
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

func (w *auditResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
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
	httpAllowlist, err := parseHostAllowlist(cfg.ProviderHTTPAllowlist)
	if err != nil {
		return nil, fmt.Errorf("PROVIDER_HTTP_ALLOWLIST contains an invalid entry: %w", err)
	}
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
	s := &Server{
		cfg:           cfg,
		pg:            pg,
		redis:         rd,
		limit:         ratelimit.New(rd.Client),
		mux:           http.NewServeMux(),
		httpAllowlist: httpAllowlist,
		egress:        strictEgressPolicy(),
	}
	s.upstream = http.HandlerFunc(s.serveDirect)
	log.Printf("data plane: direct — channels are dialled in-process")
	credentialKey, derivedCredentialKey := cfg.CredentialSealingKey()
	if derivedCredentialKey {
		log.Printf("CREDENTIAL_ENCRYPTION_KEY is not configured; derived development key is being used. " +
			"Set CREDENTIAL_ENCRYPTION_KEY in production; changing it makes existing inline credentials unreadable.")
	}
	s.credentialSealingKey = credentialKey
	// The direct plane is the only plane, so its state is always built: the
	// transports it dials with, the rotation that picks a line, and the breaker
	// that skips a failing channel.
	s.transport = &transportPool{}
	s.selector = newRouteSelector()
	s.breaker = newCircuitBreaker()
	if cfg.SessionAuthEnabled() {
		loginLimiter := ratelimit.NewAdminLoginLimiter(rd.Client).
			WithPolicy(cfg.AdminLoginMaxAttempts, cfg.AdminLoginWindow)
		registerLimiter := ratelimit.NewRegistrationLimiter(rd.Client).
			WithPolicy(cfg.PublicRegisterMaxAttempts, cfg.PublicRegisterWindow)
		sessionKey, derived := cfg.AdminSessionKey()
		if derived {
			log.Printf("ADMIN_SESSION_SECRET is not configured; derived development key is being used. " +
				"Set ADMIN_SESSION_SECRET (>= 32 bytes, e.g. `openssl rand -base64 48`) in production.")
		}
		s.adminAuth = newAdminAuthService(pg, loginLimiter, sessionKey, cfg).
			withRegistrationLimiter(registerLimiter)
		if cfg.PublicRegistrationOpen() {
			log.Printf("public registration is enabled (%s)", registerLimiter.Describe())
		} else {
			log.Printf("public registration is disabled")
		}
	}
	s.routes()
	s.bootstrapAdminUser(ctx)
	s.limit.SetQuotaRebuildSource(s.currentMonthQuotaUsage)
	s.rebuildQuotaCounters(ctx)
	return s, nil
}

// sealingKey returns the credential sealing key. A Server built outside
// NewServer (a unit test, a probe) falls back to deriving it from its Config, so
// the key is never silently empty.
func (s *Server) sealingKey() []byte {
	if len(s.credentialSealingKey) == security.CredentialKeySize {
		return s.credentialSealingKey
	}
	key, _ := s.cfg.CredentialSealingKey()
	return key
}

// currentMonthQuotaUsage aggregates the settled request log for the current UTC
// month — exactly the window the gate's live counters cover. The monthly keys
// ("YYYYMM") span month-to-date and the daily keys ("YYYYMMDD") are a subset of
// it. Bucketing from the first of the month rather than from today is what
// makes a reconstructed monthly counter correct: starting at midnight today
// would re-arm the month counter with only today's share and under-count the
// rest of the month, so the gate would over-admit for the remainder of it.
func (s *Server) currentMonthQuotaUsage(ctx context.Context) ([]model.QuotaSettledRow, error) {
	now := time.Now().UTC()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return s.pg.DailySettledQuota(ctx, from, from.AddDate(0, 1, 0))
}

// runQuotaRebuild is the single rebuild routine behind all three triggers — the
// startup reconstruction, the limiter's missing-counter hot path and the manual
// endpoint. Keeping one implementation means the three cannot drift into three
// subtly different baselines, and it reads the settled month-to-date usage from
// the request log (the durable truth) before resetting the Redis counters to it.
func (s *Server) runQuotaRebuild(ctx context.Context) (ratelimit.RebuildResult, error) {
	settled, err := s.currentMonthQuotaUsage(ctx)
	if err != nil {
		return ratelimit.RebuildResult{}, fmt.Errorf("aggregate request log: %w", err)
	}
	return s.limit.RebuildQuotaCounters(ctx, settled)
}

// rebuildQuotaCounters reconstructs the Redis tenant quota counters from the
// settled request log on startup, so a gateway that comes up after a Redis
// flush — or any cache loss — restores the gate to the settled baseline rather
// than resetting every counter to zero and over-admitting tenants.
//
// It is deliberately best-effort: a failure must not take the gateway down,
// only be logged, because the live reserve/reconcile path keeps the counters
// warm from then on and the manual reconciliation trigger can retry.
func (s *Server) rebuildQuotaCounters(ctx context.Context) {
	res, err := s.runQuotaRebuild(ctx)
	if err != nil {
		log.Printf("quota rebuild: %v", err)
		return
	}
	log.Printf("quota rebuild: reconstructed %d daily, %d monthly token, %d monthly request counter(s) from the request log",
		res.DailyTokens, res.MonthlyTokens, res.MonthlyReqs)
}

// handleQuotaRebuild is the manual reconciliation trigger for the Quota
// Counters: an operator can rebuild the Redis counters from the settled request
// log on demand — after repairing an incident, or when the live numbers are
// suspected to have drifted. It shares the exact rebuild the startup and
// missing-counter triggers use, so all three produce the same baseline.
func (s *Server) handleQuotaRebuild(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	res, err := s.runQuotaRebuild(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "quota_rebuild_failed", "Unable to rebuild the quota counters from the request log", reqID)
		return
	}
	log.Printf("quota rebuild: manual reconciliation reconstructed %d daily, %d monthly token, %d monthly request counter(s)",
		res.DailyTokens, res.MonthlyTokens, res.MonthlyReqs)
	writeJSON(w, http.StatusOK, map[string]any{
		"daily_token_counters":     res.DailyTokens,
		"monthly_token_counters":   res.MonthlyTokens,
		"monthly_request_counters": res.MonthlyReqs,
	})
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
	s.mux.HandleFunc("/auth/register", s.handleAccountRegister)

	s.mux.Handle("/admin/password", s.adminMiddleware(http.HandlerFunc(s.handleAdminPasswordChange)))
	s.mux.Handle("/admin/sessions", s.adminMiddleware(http.HandlerFunc(s.handleAdminSessions)))
	s.mux.Handle("/admin/sessions/", s.adminMiddleware(http.HandlerFunc(s.handleAdminSessionByID)))
	s.mux.Handle("/admin/users", s.adminMiddleware(http.HandlerFunc(s.handleAdminUsers)))
	s.mux.Handle("/admin/users/", s.adminMiddleware(http.HandlerFunc(s.handleAdminUserByID)))
	s.mux.Handle("/admin/model-test", s.adminMiddleware(http.HandlerFunc(s.handleModelTest)))
	s.mux.Handle("/admin/model-test-options", s.adminMiddleware(http.HandlerFunc(s.handleModelTestOptions)))
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
		if err != nil || !k.Enabled() {
			s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, EventType: "invalid_api_key", ClientIP: s.clientIPFrom(r), Detail: "unknown or disabled key"})
			httpx.Error(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API key", reqID)
			return
		}
		// A key belongs to a tenant, and switching a tenant off has to switch
		// off everything under it. Checking only the key's own status made
		// "停用租户" a label with no effect: the keys kept serving.
		if !k.TenantEnabled() {
			s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: k.TenantID, APIKeyID: k.ID, EventType: "tenant_disabled", ClientIP: s.clientIPFrom(r)})
			httpx.Error(w, http.StatusForbidden, "tenant_disabled", "Tenant is disabled", reqID)
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

		allowed := make(map[string]struct{}, len(k.AllowedModels))
		allModels := false
		for _, m := range k.AllowedModels {
			if m == model.AllModelsWildcard {
				allModels = true
				continue
			}
			allowed[m] = struct{}{}
		}
		id := model.Identity{
			TenantID:            k.TenantID,
			APIKeyID:            k.ID,
			AllowedModels:       allowed,
			AllModels:           allModels,
			AllowedIPs:          k.AllowedIPs,
			RPMLimit:            k.RPMLimit,
			TPMLimit:            k.TPMLimit,
			MaxConcurrency:      k.MaxConcurrency,
			RouteGroup:          k.RouteGroup,
			DailyTokenLimit:     k.DailyTokenLimit,
			MonthlyTokenLimit:   k.MonthlyTokenLimit,
			MonthlyRequestLimit: k.MonthlyRequestLimit,
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
	attempts := newAttemptRecorder()
	r = r.WithContext(withAttemptRecorder(r.Context(), attempts))
	var envelope requestEnvelope
	var reservation *ratelimit.TPMReservation
	var tenantReservation *ratelimit.TenantQuotaReservation
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
		}
		_ = s.pg.InsertRequestLog(ctx, log)
		// The Routing Attempt chain, recorded by the data plane into the
		// request-scoped recorder. Best-effort: a request that answered must not
		// fail because its telemetry write did (ADR-0004 allows the loss).
		_ = s.pg.InsertRequestAttempts(ctx, reqID, attempts.list())
	}()

	allowed, err := s.limit.AllowTenantRequest(r.Context(), id.TenantID, id.MonthlyRequestLimit)
	if err != nil {
		stateUnavailable(aw, reqID)
		return
	}
	if !allowed {
		s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: id.TenantID, APIKeyID: id.APIKeyID, EventType: "tenant_request_quota_exceeded", ClientIP: s.clientIPFrom(r)})
		httpx.Error(aw, http.StatusTooManyRequests, "tenant_request_quota_exceeded", "Tenant monthly request quota exceeded", reqID)
		return
	}

	allowed, err = s.limit.AllowRPM(r.Context(), id.TenantID, id.RPMLimit)
	if err != nil {
		stateUnavailable(aw, reqID)
		return
	}
	if !allowed {
		s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: id.TenantID, APIKeyID: id.APIKeyID, EventType: "rpm_exceeded", ClientIP: s.clientIPFrom(r)})
		httpx.Error(aw, http.StatusTooManyRequests, "rate_limit_exceeded", "Request rate limit exceeded", reqID)
		return
	}

	if r.Method == http.MethodGet && (r.URL.Path == "/v1/models" || strings.HasPrefix(r.URL.Path, "/v1/models/")) {
		if r.URL.Path != "/v1/models" {
			// There is no per-model metadata endpoint here, and answering 404
			// for one is honest rather than forwarding a request this gateway
			// has no meaning for.
			httpx.Error(aw, http.StatusNotFound, "not_found", "Unknown endpoint", reqID)
			return
		}
		if !id.AllModels {
			// The key is restricted — possibly to nothing at all. Falling
			// through to the routing table would advertise models this key
			// cannot call, which is the same mistake as answering 200 for a
			// request that will be refused a line later.
			writeAllowedModels(aw, id.AllowedModels)
			return
		}
		// The set of models that can be routed right now, which is exactly the
		// routing table the data plane will consult.
		s.serveDirectModels(aw, r)
		return
	}

	maxBody := maxRequestBody(r.URL.Path)
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
	if !id.AllowsModel(envelope.Model) {
		s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: id.TenantID, APIKeyID: id.APIKeyID, EventType: "model_forbidden", ClientIP: s.clientIPFrom(r), RequestedModel: envelope.Model})
		httpx.Error(aw, http.StatusForbidden, "model_not_allowed", "Model is not allowed for this API key", reqID)
		return
	}

	reserveAmount := estimateTPM(body, envelope)
	tenantReservation, allowed, err = s.limit.ReserveTenantTokens(r.Context(), id.TenantID, reserveAmount, id.DailyTokenLimit, id.MonthlyTokenLimit)
	if err != nil {
		stateUnavailable(aw, reqID)
		return
	}
	if !allowed {
		s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: id.TenantID, APIKeyID: id.APIKeyID, EventType: "tenant_token_quota_exceeded", ClientIP: s.clientIPFrom(r), RequestedModel: envelope.Model})
		httpx.Error(aw, http.StatusTooManyRequests, "tenant_token_quota_exceeded", "Tenant token quota exceeded", reqID)
		return
	}
	reservation, allowed, err = s.limit.ReserveTPM(r.Context(), id.TenantID, reserveAmount, id.TPMLimit)
	if err != nil {
		_ = tenantReservation.Refund(r.Context())
		stateUnavailable(aw, reqID)
		return
	}
	if !allowed {
		_ = tenantReservation.Refund(r.Context())
		s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: id.TenantID, APIKeyID: id.APIKeyID, EventType: "tpm_exceeded", ClientIP: s.clientIPFrom(r), RequestedModel: envelope.Model})
		httpx.Error(aw, http.StatusTooManyRequests, "token_limit_exceeded", "Token rate limit exceeded", reqID)
		return
	}

	release, allowed, err := s.limit.AcquireConcurrencySlot(r.Context(), id.TenantID, id.MaxConcurrency)
	if err != nil {
		_ = reservation.Refund(r.Context())
		_ = tenantReservation.Refund(r.Context())
		stateUnavailable(aw, reqID)
		return
	}
	if !allowed {
		_ = reservation.Refund(r.Context())
		_ = tenantReservation.Refund(r.Context())
		s.recordSecurityEvent(r, model.SecurityEvent{RequestID: reqID, TenantID: id.TenantID, APIKeyID: id.APIKeyID, EventType: "concurrency_exceeded", ClientIP: s.clientIPFrom(r), RequestedModel: envelope.Model})
		httpx.Error(aw, http.StatusTooManyRequests, "concurrency_limit_exceeded", "Concurrency limit exceeded", reqID)
		return
	}
	defer release()

	s.upstream.ServeHTTP(aw, r)
}

// stateUnavailable answers a request the gateway could not admit because an
// enforcement dependency could not be read. ADR-0007 splits the two outcomes a
// limiter can produce: an over-limit answer is a 429, but state that is
// unavailable at all is a 503 — failing closed means refusing the request, and
// the client being told "try again" rather than "you are over your limit" is
// the difference between an outage and a policy decision.
func stateUnavailable(aw http.ResponseWriter, reqID string) {
	httpx.Error(aw, http.StatusServiceUnavailable, "enforcement_state_unavailable", "Rate limit enforcement state is unavailable", reqID)
}

// maxRequestBody bounds how much of a business request the gateway will buffer.
//
// The data plane reads the body — to rewrite the model field and to give the
// request logger the usage block — so the cap belongs to the route.
func maxRequestBody(path string) int64 {
	if path == "/v1/audio/transcriptions" {
		return 64 << 20
	}
	return 16 << 20
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

// modelFreePaths are the /v1 endpoints that legitimately carry no model field.
//
// This is an exception list, and that direction matters. It used to be an
// allow-list of four paths, which meant every other endpoint under /v1 was
// forwarded without a model name and therefore without the per-key model
// allow-list: a key restricted to one model could reach any other route. Naming
// the exceptions instead makes the check cover endpoints that do not exist yet,
// which is the only way it keeps holding. The cost of failing closed is a 400
// on a route this gateway cannot route anyway; the cost of failing open is the
// allow-list.
var modelFreePaths = map[string]bool{
	"/v1/models": true,
}

func requiresModel(path string) bool {
	if !strings.HasPrefix(path, "/v1/") {
		return false
	}
	return !modelFreePaths[path]
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

// Failover reports whether the data plane actually fell back to another route.
// It is only derived from real attempt telemetry, never from planned routes.
func (t routeTelemetry) Failover() bool {
	return t.Attempts > 1
}

// parseRouteTelemetry reads the data plane's attribution off the response.
//
// It reads the X-Gateway-* names the direct plane stamps. They are the only
// names now: the retired second plane reported through its own headers, and it
// is gone, so there is nothing to fall back to.
func parseRouteTelemetry(h http.Header) routeTelemetry {
	get := func(name string) string {
		return strings.TrimSpace(h.Get(name))
	}
	v := routeTelemetry{
		Provider:      get(headerProvider),
		UpstreamModel: get(headerUpstreamModel),
	}
	if n, err := strconv.Atoi(get(headerRoutePriority)); err == nil && n > 0 {
		v.RoutePriority = n
	}
	if n, err := strconv.Atoi(get(headerAttempts)); err == nil && n > 0 {
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
	if s.pg == nil {
		return
	}
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
