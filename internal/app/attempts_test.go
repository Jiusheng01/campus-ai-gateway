package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// A failover request must leave a record of every channel it tried, in order,
// with each outcome — not just the winning channel and a count.
func TestServeDirectRecordsTheAttemptChain(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()
	served := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[],"usage":{"total_tokens":1}}`))
	}))
	defer served.Close()

	t.Setenv("TEST_ATTEMPT_KEY_A", "k")
	t.Setenv("TEST_ATTEMPT_KEY_B", "k")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "a", BaseURL: failing.URL, UpstreamModel: "a-model", CredentialEnv: "TEST_ATTEMPT_KEY_A", Priority: 1, Weight: 1, TimeoutMS: 5000},
			{Provider: "b", BaseURL: served.URL, UpstreamModel: "b-model", CredentialEnv: "TEST_ATTEMPT_KEY_B", Priority: 1, Weight: 1, TimeoutMS: 5000},
		},
	}})

	rec := newAttemptRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	req = req.WithContext(withAttemptRecorder(req.Context(), rec))
	s.serveDirect(httptest.NewRecorder(), req)

	got := rec.list()
	if len(got) != 2 {
		t.Fatalf("recorded %d attempts, want 2: %+v", len(got), got)
	}
	if got[0].AttemptNo != 1 || got[0].Provider != "a" || got[0].Outcome != model.AttemptFailed || got[0].StatusCode != 500 {
		t.Fatalf("first attempt = %+v, want #1 a failed 500", got[0])
	}
	if got[1].AttemptNo != 2 || got[1].Provider != "b" || got[1].Outcome != model.AttemptServed {
		t.Fatalf("second attempt = %+v, want #2 b served", got[1])
	}
	if got[1].UpstreamModel != "b-model" {
		t.Fatalf("upstream model = %q, want b-model", got[1].UpstreamModel)
	}
}

// A transport failure is recorded with the gateway's own error kind, so the
// chain explains "could not reach" without quoting the dial error.
func TestServeDirectRecordsATransportFailure(t *testing.T) {
	t.Setenv("TEST_ATTEMPT_KEY_C", "k")
	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "dead", BaseURL: "http://127.0.0.1:1", UpstreamModel: "m", CredentialEnv: "TEST_ATTEMPT_KEY_C", Priority: 1, Weight: 1, TimeoutMS: 1000},
		},
	}})

	rec := newAttemptRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	req = req.WithContext(withAttemptRecorder(req.Context(), rec))
	s.serveDirect(httptest.NewRecorder(), req)

	got := rec.list()
	if len(got) != 1 {
		t.Fatalf("recorded %d attempts, want 1: %+v", len(got), got)
	}
	if got[0].Outcome != model.AttemptFailed || got[0].ErrorKind == "" {
		t.Fatalf("want a failed attempt with an error kind, got %+v", got[0])
	}
}
