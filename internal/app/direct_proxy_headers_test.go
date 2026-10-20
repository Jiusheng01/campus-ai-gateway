package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The direct data plane copies upstream response headers to the caller. That
// copy is a security boundary, not a formality: a channel that reflects the
// request's Authorization header back (some proxies and debugging gateways do)
// would otherwise hand the tenant the channel's own credential. The credential
// is injected by the gateway on the way out (attemptDirect), so a reflection is
// the gateway's key, not the client's.
func TestServeDirectDoesNotForwardCredentialBearingResponseHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo the credential the gateway injected, plus a cookie, exactly as a
		// careless upstream might. The redirect, server identity and CORS
		// headers are the other shapes ADR-0009 says must not reach a client.
		w.Header().Set("Authorization", r.Header.Get("Authorization"))
		w.Header().Set("Www-Authenticate", `Bearer realm="echo"`)
		w.Header().Set("Set-Cookie", "session=leak; Path=/")
		w.Header().Set("Proxy-Authenticate", `Basic realm="proxy"`)
		w.Header().Set("Location", "https://evil.test/steal")
		w.Header().Set("Server", "upstream/1.0")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	t.Setenv("TEST_DIRECT_KEY_REFLECT", "sk-channel-secret")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_REFLECT", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Authorization"); got != "" {
		t.Fatalf("the channel credential leaked back to the caller: Authorization=%q", got)
	}
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("an upstream cookie leaked back to the caller: Set-Cookie=%q", got)
	}
	if got := rec.Header().Get("Www-Authenticate"); got != "" {
		t.Fatalf("an upstream challenge leaked back to the caller: Www-Authenticate=%q", got)
	}
	if got := rec.Header().Get("Proxy-Authenticate"); got != "" {
		t.Fatalf("an upstream proxy challenge leaked back to the caller: Proxy-Authenticate=%q", got)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("an upstream redirect leaked back to the caller: Location=%q", got)
	}
	if got := rec.Header().Get("Server"); got != "" {
		t.Fatalf("upstream server identity leaked back to the caller: Server=%q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("an upstream CORS header leaked back to the caller: %q", got)
	}
	// Ordinary end-to-end headers must still pass through; the fix is an allow
	// list, not "drop every header".
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("a normal response header was lost: Content-Type=%q", got)
	}
}

// The request side is the mirror of the response side. The gateway forwards
// only the headers ADR-0012 names — content negotiation, W3C trace context and
// the Client Correlation ID (X-Request-Id) — so a tenant's cookies and its
// forwarding chain never reach a third-party Provider.
func TestServeDirectForwardsOnlyAllowlistedRequestHeaders(t *testing.T) {
	var upstreamHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	t.Setenv("TEST_DIRECT_KEY_REQUEST_HEADERS", "sk-channel-credential")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_REQUEST_HEADERS", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	req.Header.Set("Tracestate", "campus=abc")
	req.Header.Set("X-Request-Id", "client-corr-1")
	// Everything below is what a browser or a reverse proxy would attach. None
	// of it is the gateway's business to hand to a Provider.
	req.Header.Set("Cookie", "session=tenant-secret")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Real-IP", "203.0.113.9")
	req.Header.Set("X-Campus-Internal", "must-not-leak")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"Cookie", "X-Forwarded-For", "X-Real-IP", "X-Campus-Internal"} {
		if got := upstreamHeaders.Get(name); got != "" {
			t.Fatalf("a non-allowlisted request header reached the upstream: %s=%q", name, got)
		}
	}
	// The headers ADR-0012 does name must still arrive; the fix is an allow
	// list, not "drop every header".
	if got := upstreamHeaders.Get("Accept"); got != "application/json" {
		t.Fatalf("content negotiation was lost: Accept=%q", got)
	}
	if got := upstreamHeaders.Get("Traceparent"); got != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
		t.Fatalf("W3C trace context was lost: Traceparent=%q", got)
	}
	if got := upstreamHeaders.Get("X-Request-Id"); got != "client-corr-1" {
		t.Fatalf("the Client Correlation ID was lost: X-Request-Id=%q", got)
	}
	if got := upstreamHeaders.Get("Authorization"); got != "Bearer sk-channel-credential" {
		t.Fatalf("Authorization = %q, want the channel credential", got)
	}
}

// Accept-Encoding is not on the request allow list. Streaming passes the body
// through unchanged, so the gateway forces identity; a client value would never
// reach the upstream anyway, and listing it in the allow list only made the
// policy claim something the code did not do.
func TestAcceptEncodingIsForcedToIdentityNotForwarded(t *testing.T) {
	if forwardableRequestHeaders["Accept-Encoding"] {
		t.Fatal("Accept-Encoding must not be on the request allow list; the data plane forces identity")
	}

	var upstreamEncoding string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	t.Setenv("TEST_DIRECT_KEY_ACCEPT_ENCODING", "sk-channel-credential")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_ACCEPT_ENCODING", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if upstreamEncoding != "identity" {
		t.Fatalf("upstream Accept-Encoding = %q, want identity", upstreamEncoding)
	}
}

// The gateway authenticates the caller with its API Key and then authenticates
// to the Provider with the channel Credential. A client that sends its own
// Authorization must not be able to replace the second one.
func TestServeDirectReplacesClientAuthorizationWithChannelCredential(t *testing.T) {
	var upstreamAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	t.Setenv("TEST_DIRECT_KEY_CLIENT_AUTH", "sk-channel-credential")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_CLIENT_AUTH", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-tenant-api-key")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if upstreamAuth != "Bearer sk-channel-credential" {
		t.Fatalf("the channel credential did not override the client's Authorization: got %q", upstreamAuth)
	}
}

// An upstream body is not a trusted place to copy operator-facing text from.
// It can carry internal addresses, upstream credentials or internal routing
// detail. The client gets a stable error code and the attempt count; the body
// stays in the gateway.
func TestServeDirectDoesNotEchoTheUpstreamErrorBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"upstream at 10.0.0.7 rejected key sk-internal-abc"}`))
	}))
	defer upstream.Close()

	t.Setenv("TEST_DIRECT_KEY_ERR", "sk-a")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_ERR", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want the upstream's 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "sk-internal-abc") || strings.Contains(rec.Body.String(), "10.0.0.7") {
		t.Fatalf("the upstream error body leaked to the caller: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("the stable error code must remain so clients can branch on it: %s", rec.Body.String())
	}
	if got := rec.Header().Get(headerAttempts); got != "1" {
		t.Fatalf("attempts header = %q, want 1", got)
	}
}

// A connection error's text can include the dialled address. The caller gets a
// stable code; the address stays in the gateway.
func TestServeDirectDoesNotEchoTheDialError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := upstream.URL
	upstream.Close() // now nothing listens there

	t.Setenv("TEST_DIRECT_KEY_DEAD", "sk-a")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "qwen", BaseURL: deadURL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_DEAD", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	host := strings.TrimPrefix(deadURL, "http://")
	if strings.Contains(rec.Body.String(), host) {
		t.Fatalf("the dial error leaked the upstream address: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_unavailable") {
		t.Fatalf("the stable error code must remain: %s", rec.Body.String())
	}
}

// The admin connectivity test reports upstream failures to an operator. It is
// an authenticated, diagnostic endpoint, but it still must not echo the raw
// body or the dial error, because those are exactly the places internal detail
// lives and the operator does not need it to decide what to fix.
func TestServeModelTestDirectDoesNotEchoUpstreamDetail(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`internal gateway 10.0.0.7 said sk-internal-abc`))
	}))
	defer upstream.Close()

	t.Setenv("TEST_DIRECT_KEY_TESTERR", "sk-test")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_TESTERR", Priority: 1},
		},
	}})

	payload := []byte(`{"model":"demo-model","messages":[{"role":"user","content":"ping"}]}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/model-test", strings.NewReader(string(payload)))
	s.serveModelTestDirect(rec, req, modelTestRequest{Model: "demo-model"}, payload, "req_test")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-internal-abc") || strings.Contains(rec.Body.String(), "10.0.0.7") {
		t.Fatalf("the connectivity test leaked upstream detail: %s", rec.Body.String())
	}
}
