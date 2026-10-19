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
		"campus-smart": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_REFLECT", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-smart"}`))
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
		"campus-smart": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_ERR", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-smart"}`))
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
	if got := rec.Header().Get("X-Gateway-Attempts"); got != "1" {
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
		"campus-smart": {
			{Provider: "qwen", BaseURL: deadURL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_DEAD", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-smart"}`))
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
		"campus-smart": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_TESTERR", Priority: 1},
		},
	}})

	payload := []byte(`{"model":"campus-smart","messages":[{"role":"user","content":"ping"}]}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/model-test", strings.NewReader(string(payload)))
	s.serveModelTestDirect(rec, req, thinkWatchTestRequest{Model: "campus-smart"}, payload, "req_test")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-internal-abc") || strings.Contains(rec.Body.String(), "10.0.0.7") {
		t.Fatalf("the connectivity test leaked upstream detail: %s", rec.Body.String())
	}
}
