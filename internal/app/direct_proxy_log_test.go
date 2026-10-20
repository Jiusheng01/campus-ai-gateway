package app

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureLog runs fn with the standard logger redirected into a buffer and
// returns everything written. The server log is the output under test here:
// the risk is what ends up in it, not what the handler returns.
//
// These tests do not run in parallel. log's writer is package-global, and the
// previous writer is restored before captureLog returns so a failure cannot
// leak a buffer into another test.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	}()

	fn()

	return buf.String()
}

// R4: the failure line names the channel and the status, but it must not quote
// the upstream body. A channel that reflects the request's Authorization header
// - which the gateway injected - puts the channel credential in its error body,
// so logging that body records the credential. The request log is retained,
// shipped and read by people who are not entitled to the channel secret.
func TestServeDirectDoesNotLogTheUpstreamErrorBody(t *testing.T) {
	const credential = "sk-channel-credential-must-not-be-logged"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		// A careless upstream echoes the credential the gateway injected.
		_, _ = w.Write([]byte(`{"error":"rejected Authorization: ` + r.Header.Get("Authorization") + ` at 10.0.0.7"}`))
	}))
	defer upstream.Close()

	t.Setenv("TEST_DIRECT_KEY_LOG", credential)

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_LOG", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "req_r4_body")
	rec := httptest.NewRecorder()

	logged := captureLog(t, func() { s.serveDirect(rec, req) })

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want the upstream's 503", rec.Code)
	}
	if strings.Contains(logged, credential) {
		t.Fatalf("the server log recorded the channel credential: %s", logged)
	}
	// Dropping the body must not turn the line into a useless one. The request
	// id, model, status and channel are what an operator needs to locate the
	// failing route.
	for _, want := range []string{"req_r4_body", `"demo-model"`, "503", "qwen"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("the server log lost %q, which the operator needs: %s", want, logged)
		}
	}
}

// R4, second shape: a transport error's text is not upstream content, but it is
// upstream-adjacent. A malformed base_url is quoted verbatim - userinfo and
// password included - in the error directTargetURL builds from it, and url.Parse
// then quotes the raw URL a second time. Logging that error records the
// credential.
func TestServeDirectDoesNotLogACredentialFromTheChannelTarget(t *testing.T) {
	const password = "base-url-password-must-not-be-logged"
	// The control character makes url.Parse reject the target after the join;
	// the resulting errors quote base_url and the raw URL as they are.
	base := "https://operator:" + password + "@upstream.test/v1\nbroken"

	t.Setenv("TEST_DIRECT_KEY_BADURL", "sk-channel")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "qwen", BaseURL: base, UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_BADURL", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "req_r4_url")
	rec := httptest.NewRecorder()

	logged := captureLog(t, func() { s.serveDirect(rec, req) })

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if strings.Contains(logged, password) {
		t.Fatalf("the server log recorded the base_url password: %s", logged)
	}
	for _, want := range []string{"req_r4_url", `"demo-model"`, "qwen"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("the server log lost %q, which the operator needs: %s", want, logged)
		}
	}
}

// A missing credential is the one failure whose own text reaches the log,
// because it names an environment variable rather than its value. That text has
// to stay useful - the operator learns which variable to set - so this pins that
// the branch is exercised and names the variable.
func TestServeDirectLogsTheMissingCredentialVariable(t *testing.T) {
	t.Setenv("TEST_DIRECT_KEY_UNSET_LOG", "")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_UNSET_LOG", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "req_r4_missing")
	rec := httptest.NewRecorder()

	logged := captureLog(t, func() { s.serveDirect(rec, req) })

	if !strings.Contains(logged, "TEST_DIRECT_KEY_UNSET_LOG") {
		t.Fatalf("the log must name the variable an operator has to set: %s", logged)
	}
}
