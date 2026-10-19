package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The circuit opens after the threshold, waits out its cooldown, lets exactly
// one probe through, re-opens on a failed probe and closes on a good one. The
// clock is injected so this runs instantly.
func TestBreakerOpensHalfOpensAndRecovers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	b := newCircuitBreaker().withClock(func() time.Time { return now })

	for i := 0; i < breakerFailureThreshold; i++ {
		if !b.allow("p") {
			t.Fatalf("a closed breaker must allow (call %d)", i)
		}
		b.record("p", false)
	}
	if b.allow("p") {
		t.Fatal("the breaker must be open after the failure threshold")
	}

	now = now.Add(breakerCooldown - time.Second)
	if b.allow("p") {
		t.Fatal("the breaker must stay open until the cooldown elapses")
	}

	now = now.Add(2 * time.Second)
	if !b.allow("p") {
		t.Fatal("an elapsed cooldown must let a probe through")
	}
	if b.allow("p") {
		t.Fatal("only one probe may be in flight")
	}

	b.record("p", false)
	if b.allow("p") {
		t.Fatal("a failed probe must re-open the circuit")
	}

	now = now.Add(breakerCooldown + time.Second)
	if !b.allow("p") {
		t.Fatal("the second cooldown must let a probe through")
	}
	b.record("p", true)
	if !b.allow("p") {
		t.Fatal("a successful probe must close the circuit")
	}
}

func TestClassifyAttemptSplitsRetryableFromTerminal(t *testing.T) {
	cases := map[int]attemptClass{
		200: attemptServed,
		400: attemptTerminal,
		422: attemptTerminal,
		401: attemptFailover,
		403: attemptFailover,
		404: attemptFailover,
		408: attemptFailover,
		429: attemptFailover,
		500: attemptFailover,
		503: attemptFailover,
	}
	for status, want := range cases {
		if got := classifyAttempt(status); got != want {
			t.Fatalf("classifyAttempt(%d) = %v, want %v", status, got, want)
		}
	}
}

// An open circuit is skipped, not tried: the upstream is never hit, and the
// attempt chain says "skipped" so the console can tell it apart from a failure.
func TestServeDirectSkipsAnOpenChannelAndRecordsIt(t *testing.T) {
	var deadHits int32
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&deadHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dead.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[],"usage":{"total_tokens":1}}`))
	}))
	defer good.Close()

	t.Setenv("TEST_BREAKER_KEY_A", "k")
	t.Setenv("TEST_BREAKER_KEY_B", "k")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "a", BaseURL: dead.URL, UpstreamModel: "a", CredentialEnv: "TEST_BREAKER_KEY_A", Priority: 1, Weight: 1, TimeoutMS: 5000},
			{Provider: "b", BaseURL: good.URL, UpstreamModel: "b", CredentialEnv: "TEST_BREAKER_KEY_B", Priority: 1, Weight: 1, TimeoutMS: 5000},
		},
	}})

	// Drive "a" open.
	for i := 0; i < breakerFailureThreshold; i++ {
		s.breaker.record("a", false)
	}

	rec := newAttemptRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-smart"}`))
	req = req.WithContext(withAttemptRecorder(req.Context(), rec))
	s.serveDirect(httptest.NewRecorder(), req)

	if n := atomic.LoadInt32(&deadHits); n != 0 {
		t.Fatalf("the open channel was hit %d times, want 0", n)
	}
	chain := rec.list()
	var sawSkipped, sawServed bool
	for _, a := range chain {
		if a.Provider == "a" && a.Outcome == "skipped" {
			sawSkipped = true
		}
		if a.Provider == "b" && a.Outcome == "served" {
			sawServed = true
		}
	}
	if !sawSkipped || !sawServed {
		t.Fatalf("chain = %+v, want a skipped for a and a served for b", chain)
	}
}

// When every line is open the request is refused with a clear 503 rather than an
// empty "all channels failed".
func TestServeDirectRefusesWhenEveryChannelIsOpen(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	t.Setenv("TEST_BREAKER_KEY_C", "k")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "c", BaseURL: up.URL, UpstreamModel: "c", CredentialEnv: "TEST_BREAKER_KEY_C", Priority: 1, Weight: 1, TimeoutMS: 5000},
		},
	}})
	for i := 0; i < breakerFailureThreshold; i++ {
		s.breaker.record("c", false)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-smart"}`))
	s.serveDirect(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "channels_circuit_open") {
		t.Fatalf("body = %s, want channels_circuit_open", rec.Body.String())
	}
}
