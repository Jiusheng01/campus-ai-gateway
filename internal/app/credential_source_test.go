package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
)

// A channel's Credential has two sources (ADR-0017), and the data plane must be
// indifferent to which one is configured: both end up as the upstream's Bearer
// token.
func TestServeDirectSendsTheCredentialOfBothSourceTypes(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, security.CredentialKeySize)
	sealed, err := security.Seal(key, []byte("sk-inline-secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	t.Setenv("TEST_CREDENTIAL_ENV_SOURCE", "sk-env-secret")

	cases := []struct {
		name       string
		credential directRoute
		wantAuth   string
	}{
		{
			name: "environment variable",
			credential: directRoute{
				CredentialEnv:    "TEST_CREDENTIAL_ENV_SOURCE",
				CredentialSource: model.CredentialSourceEnv,
				CredentialRef:    "TEST_CREDENTIAL_ENV_SOURCE",
			},
			wantAuth: "Bearer sk-env-secret",
		},
		{
			name: "sealed inline value",
			credential: directRoute{
				CredentialSource:     model.CredentialSourceInline,
				CredentialCiphertext: sealed,
			},
			wantAuth: "Bearer sk-inline-secret",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotAuth := make(chan string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth <- r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer upstream.Close()

			route := tc.credential
			route.Provider = "qwen"
			route.BaseURL = upstream.URL + "/v1"
			route.UpstreamModel = "qwen-plus"
			route.Priority = 1

			s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
				"demo-model": {route},
			}})
			s.credentialSealingKey = key

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
				strings.NewReader(`{"model":"demo-model"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			s.serveDirect(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
			}
			if got := <-gotAuth; got != tc.wantAuth {
				t.Fatalf("upstream Authorization = %q, want %q", got, tc.wantAuth)
			}
		})
	}
}

// A stored ciphertext this process cannot open must fail closed, exactly like a
// missing environment variable: the request is never dialled and the caller sees
// the gateway's missing-credential answer, not an upstream one.
func TestServeDirectFailsClosedOnAnUndecryptableInlineCredential(t *testing.T) {
	serverKey := bytes.Repeat([]byte{0x11}, security.CredentialKeySize)
	otherKey := bytes.Repeat([]byte{0x22}, security.CredentialKeySize)
	sealedElsewhere, err := security.Seal(otherKey, []byte("sk-inline-secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	var reached int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reached, 1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {{
			Provider:             "qwen",
			BaseURL:              upstream.URL + "/v1",
			UpstreamModel:        "x",
			CredentialSource:     model.CredentialSourceInline,
			CredentialCiphertext: sealedElsewhere,
			Priority:             1,
		}},
	}})
	s.credentialSealingKey = serverKey

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"demo-model"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt64(&reached); n != 0 {
		t.Fatalf("a channel whose credential cannot be opened must never be dialled; it was hit %d time(s)", n)
	}
	if !strings.Contains(rec.Body.String(), "inline credential") {
		t.Fatalf("the failure must say the inline credential is the problem: %s", rec.Body.String())
	}
}

// An inline Credential with no stored value is a configuration error, not an
// empty Bearer token sent upstream.
func TestServeDirectFailsClosedOnAnUnconfiguredInlineCredential(t *testing.T) {
	var reached int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reached, 1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {{
			Provider:         "qwen",
			BaseURL:          upstream.URL + "/v1",
			UpstreamModel:    "x",
			CredentialSource: model.CredentialSourceInline,
			Priority:         1,
		}},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"demo-model"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt64(&reached); n != 0 {
		t.Fatalf("an empty credential must never be dialled; it was hit %d time(s)", n)
	}
}
