package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// providerDiagnosticTimeout bounds the management probe. It is deliberately
// shorter than the data-plane default: an operator waiting on a diagnostic
// needs an answer, and a stuck channel must not hold an admin request forever.
const providerDiagnosticTimeout = 30 * time.Second

const (
	providerDiagnosticMaxBody    = 16 << 10
	providerDiagnosticMaxPrompt  = 4 << 10
	providerDiagnosticMaxContent = 4 << 10
	providerDiagnosticPerMinute  = 6
)

// providerTestRequest is the only input a channel diagnostic accepts. There is
// deliberately no base_url, header or credential field: the probe uses the
// saved channel configuration, so it cannot be aimed at a new target.
type providerTestRequest struct {
	UpstreamModel  string `json:"upstream_model"`
	Prompt         string `json:"prompt"`
	MaxTokens      int64  `json:"max_tokens"`
	IncludeContent bool   `json:"include_content"`
}

type providerTestResponse struct {
	Channel       string `json:"channel"`
	UpstreamModel string `json:"upstream_model"`
	StatusCode    int    `json:"status_code"`
	LatencyMS     int64  `json:"latency_ms"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	TotalTokens   int64  `json:"total_tokens"`
	Content       string `json:"content,omitempty"`
}

// providerTestLimiter is a per-process, per-actor guard. It is not shared
// state: a diagnostic is a management action, and a second gateway instance
// keeping its own budget is an explicit limitation, not a silent global claim.
type providerTestLimiter struct {
	mu       sync.Mutex
	inFlight map[string]bool
	hits     map[string][]time.Time
}

func newProviderTestLimiter() *providerTestLimiter {
	return &providerTestLimiter{inFlight: map[string]bool{}, hits: map[string][]time.Time{}}
}

var fallbackProviderTestLimiter = newProviderTestLimiter()

func (l *providerTestLimiter) acquire(actor string, now time.Time) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight[actor] {
		return nil, false
	}
	cutoff := now.Add(-time.Minute)
	kept := l.hits[actor][:0]
	for _, t := range l.hits[actor] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= providerDiagnosticPerMinute {
		l.hits[actor] = kept
		return nil, false
	}
	l.hits[actor] = append(kept, now)
	l.inFlight[actor] = true
	return func() {
		l.mu.Lock()
		delete(l.inFlight, actor)
		l.mu.Unlock()
	}, true
}

func (s *Server) providerTestGate() *providerTestLimiter {
	if s.providerTests != nil {
		return s.providerTests
	}
	return fallbackProviderTestLimiter
}

// handleProviderTest serves POST /admin/providers/{id}/test.
func (s *Server) handleProviderTest(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	providerID, ok := providerIDFromProviderPath(r.URL.Path, "/test")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	var in providerTestRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, providerDiagnosticMaxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_test_request", "Request body must be valid JSON with only upstream_model, prompt, max_tokens and include_content", reqID)
		return
	}
	in.UpstreamModel = strings.TrimSpace(in.UpstreamModel)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.UpstreamModel == "" || len([]rune(in.UpstreamModel)) > 128 {
		httpx.Error(w, http.StatusBadRequest, "invalid_test_request", "upstream_model is required and must be at most 128 characters", reqID)
		return
	}
	if in.Prompt == "" || len(in.Prompt) > providerDiagnosticMaxPrompt {
		httpx.Error(w, http.StatusBadRequest, "invalid_test_request", "prompt is required and must be at most 4 KiB", reqID)
		return
	}
	if in.MaxTokens == 0 {
		in.MaxTokens = 64
	}
	if in.MaxTokens < 1 || in.MaxTokens > 1024 {
		httpx.Error(w, http.StatusBadRequest, "invalid_test_request", "max_tokens must be between 1 and 1024", reqID)
		return
	}

	// Content preview is a separate, explicit capability. Channel management
	// alone does not grant it: an answer may contain user or upstream data.
	principal, _ := adminPrincipalFrom(r.Context())
	if in.IncludeContent && !principal.Roles[RoleSuperAdmin] {
		httpx.Error(w, http.StatusForbidden, "content_preview_forbidden",
			"Answer-content preview requires super_admin; the default diagnostic returns no content", reqID)
		return
	}

	provider, err := s.pg.ProviderByID(r.Context(), providerID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "provider_not_found", "Provider was not found", reqID)
		return
	}

	release, allowed := s.providerTestGate().acquire(principal.User, time.Now())
	if !allowed {
		httpx.Error(w, http.StatusTooManyRequests, "test_rate_limited",
			"Only one diagnostic may run at a time, with at most six per minute per operator on this instance", reqID)
		return
	}
	defer release()

	result := s.runProviderDiagnostic(r, provider, in, reqID)
	if result.errCode != "" {
		s.recordProviderTest(provider.ID, result.last)
		httpx.Error(w, result.httpStatus, result.errCode, result.errMessage, reqID)
		return
	}
	s.recordProviderTest(provider.ID, result.last)
	writeJSON(w, http.StatusOK, result.response)
}

type providerDiagnosticResult struct {
	response  providerTestResponse
	last      model.ProviderTestResult
	httpStatus int
	errCode   string
	errMessage string
}

func (s *Server) runProviderDiagnostic(r *http.Request, provider model.Provider, in providerTestRequest, reqID string) providerDiagnosticResult {
	started := time.Now()
	fail := func(status int, code, message string, httpStatus, latencyMS int64) providerDiagnosticResult {
		return providerDiagnosticResult{
			httpStatus: status,
			errCode:    code,
			errMessage: message,
			last: model.ProviderTestResult{
				At: started.UTC(), OK: false, StatusCode: int(httpStatus),
				LatencyMS: latencyMS, UpstreamModel: in.UpstreamModel, ErrorKind: code,
			},
		}
	}

	payload, err := json.Marshal(map[string]any{
		"model": in.UpstreamModel,
		"messages": []map[string]string{
			{"role": "user", "content": in.Prompt},
		},
		"max_tokens": in.MaxTokens,
		"stream":     false,
	})
	if err != nil {
		return fail(http.StatusInternalServerError, "test_request_failed", "Unable to build the diagnostic request", 0, 0)
	}

	ctx, cancel := context.WithTimeout(r.Context(), providerDiagnosticTimeout)
	defer cancel()
	probe, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", nil)
	if err != nil {
		return fail(http.StatusInternalServerError, "test_request_failed", "Unable to create the diagnostic request", 0, 0)
	}
	probe.Header.Set("Content-Type", "application/json")

	route := directRoute{
		Provider:             provider.Name,
		BaseURL:              provider.BaseURL,
		CredentialEnv:        provider.CredentialEnv,
		CredentialSource:     provider.CredentialSource,
		CredentialRef:        provider.CredentialSecretRef,
		CredentialCiphertext: provider.CredentialCiphertext,
		TimeoutMS:            int(providerDiagnosticTimeout.Milliseconds()),
	}
	resp, err := s.attemptDirect(probe, payload, requestEnvelope{Model: in.UpstreamModel}, route)
	latency := time.Since(started).Milliseconds()
	if err != nil {
		var denied *egressDeniedError
		if errors.As(err, &denied) {
			log.Printf("provider test: request %s channel %s: %s", reqID, oneLine(provider.Name), denied.Error())
			s.recordEgressDenial(r, reqID, "", err)
			return fail(http.StatusBadGateway, "provider_egress_denied", denied.SafeSummary(), 0, latency)
		}
		kind := attemptErrorKind(err)
		if detail := clientSafeFailure(err); detail != "" {
			return fail(http.StatusBadRequest, "credential_unavailable",
				"cannot test channel "+provider.Name+": "+detail, 0, latency)
		}
		return fail(http.StatusBadGateway, "upstream_unavailable",
			"cannot test channel "+provider.Name+" ("+in.UpstreamModel+"): "+kind, 0, latency)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	_ = resp.Body.Close()
	if readErr != nil {
		return fail(http.StatusBadGateway, "upstream_unavailable", "the response could not be read", int64(resp.StatusCode), latency)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		kind := providerTestErrorKind(resp.StatusCode)
		return fail(http.StatusBadGateway, kind,
			"channel "+provider.Name+" ("+in.UpstreamModel+") answered HTTP "+http.StatusText(resp.StatusCode),
			int64(resp.StatusCode), latency)
	}
	parsed, err := parseProbeResponse(body, time.Since(started))
	if err != nil {
		return fail(http.StatusBadGateway, "invalid_upstream_response",
			"channel "+provider.Name+" ("+in.UpstreamModel+") returned a response that is not an OpenAI completion",
			int64(resp.StatusCode), latency)
	}
	out := providerTestResponse{
		Channel:       provider.Name,
		UpstreamModel: in.UpstreamModel,
		StatusCode:    resp.StatusCode,
		LatencyMS:     latency,
		InputTokens:   parsed.InputTokens,
		OutputTokens:  parsed.OutputTokens,
		TotalTokens:   parsed.TotalTokens,
	}
	if in.IncludeContent {
		out.Content = truncateRunes(parsed.Content, providerDiagnosticMaxContent)
	}
	return providerDiagnosticResult{
		response: out,
		last: model.ProviderTestResult{
			At: started.UTC(), OK: true, StatusCode: resp.StatusCode,
			LatencyMS: latency, UpstreamModel: in.UpstreamModel,
		},
	}
}

func (s *Server) recordProviderTest(providerID string, result model.ProviderTestResult) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.pg.RecordProviderTest(ctx, providerID, result)
}

func providerTestErrorKind(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "credential_rejected"
	case http.StatusNotFound:
		return "upstream_model_not_found"
	case http.StatusRequestTimeout:
		return "upstream_timeout"
	case http.StatusTooManyRequests:
		return "upstream_rate_limited"
	}
	if status >= 500 {
		return "upstream_error"
	}
	return "upstream_rejected"
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
