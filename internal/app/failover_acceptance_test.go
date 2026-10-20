package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func failoverRequest(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"demo-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.serveDirect(rec, req)
	return rec
}

func TestServeDirectFailsOverOnConnectionRefusal(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"backup-up","usage":{"total_tokens":1}}`))
	}))
	defer backup.Close()
	t.Setenv("TEST_FAILOVER_DEAD", "k1")
	t.Setenv("TEST_FAILOVER_GOOD", "k2")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "dead", BaseURL: deadURL + "/v1", UpstreamModel: "dead-up", CredentialEnv: "TEST_FAILOVER_DEAD", Priority: 1, TimeoutMS: 1000},
			{Provider: "backup", BaseURL: backup.URL + "/v1", UpstreamModel: "backup-up", CredentialEnv: "TEST_FAILOVER_GOOD", Priority: 2, TimeoutMS: 1000},
		},
	}})
	rec := failoverRequest(t, s)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(headerProvider); got != "backup" {
		t.Fatalf("provider = %q, want backup", got)
	}
	if got := rec.Header().Get(headerAttempts); got != "2" {
		t.Fatalf("attempts = %q, want 2", got)
	}
}

func TestServeDirectFailsOverOnResponseHeaderTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer slow.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"backup-up","usage":{"total_tokens":1}}`))
	}))
	defer backup.Close()
	t.Setenv("TEST_TIMEOUT_PRIMARY", "k1")
	t.Setenv("TEST_TIMEOUT_BACKUP", "k2")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "slow", BaseURL: slow.URL + "/v1", UpstreamModel: "slow-up", CredentialEnv: "TEST_TIMEOUT_PRIMARY", Priority: 1, TimeoutMS: 40},
			{Provider: "backup", BaseURL: backup.URL + "/v1", UpstreamModel: "backup-up", CredentialEnv: "TEST_TIMEOUT_BACKUP", Priority: 2, TimeoutMS: 1000},
		},
	}})
	rec := failoverRequest(t, s)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(headerProvider); got != "backup" {
		t.Fatalf("provider = %q, want backup", got)
	}
}

// Once a response has started streaming, the gateway must not start a second
// upstream call; the client already has partial output.
func TestServeDirectDoesNotFailOverAfterStreamingStarts(t *testing.T) {
	var backupHits int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"chunk\":1}\n\n"))
		w.(http.Flusher).Flush()
		// Return; the gateway must deliver what it got and stop.
	}))
	defer first.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&backupHits, 1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer backup.Close()
	t.Setenv("TEST_STREAM_PRIMARY", "k1")
	t.Setenv("TEST_STREAM_BACKUP", "k2")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "primary", BaseURL: first.URL + "/v1", UpstreamModel: "up", CredentialEnv: "TEST_STREAM_PRIMARY", Priority: 1, TimeoutMS: 1000},
			{Provider: "backup", BaseURL: backup.URL + "/v1", UpstreamModel: "up", CredentialEnv: "TEST_STREAM_BACKUP", Priority: 2, TimeoutMS: 1000},
		},
	}})
	rec := failoverRequest(t, s)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "chunk") {
		t.Fatalf("stream = %d %q", rec.Code, rec.Body.String())
	}
	if got := atomic.LoadInt32(&backupHits); got != 0 {
		t.Fatalf("a second upstream was called after streaming started: %d", got)
	}
}
