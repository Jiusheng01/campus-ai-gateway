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

	now = now.Add(2*breakerCooldown + time.Second)
	if !b.allow("p") {
		t.Fatal("the doubled cooldown must let a probe through")
	}
	b.record("p", true)
	if !b.allow("p") {
		t.Fatal("a successful probe must close the circuit")
	}
}

// ADR-0006's second trigger: more than half of the last 20 attempts failing
// opens the circuit even when the failures are not consecutive.
func TestBreakerOpensOnFailureRate(t *testing.T) {
	b := newCircuitBreaker()
	// Four groups of three failures then a success (16 attempts, longest run
	// three), then four more failures: 16 of the last 20 attempts failed, but no
	// run of consecutive failures reached five, so only the failure-rate trigger
	// can open the circuit here.
	for i := 0; i < 4; i++ {
		b.record("p", false)
		b.record("p", false)
		b.record("p", false)
		b.record("p", true)
	}
	for i := 0; i < 4; i++ {
		b.record("p", false)
	}
	if b.allow("p") {
		t.Fatal("a >50% failure rate over 20 attempts must open the circuit")
	}
}

// A failed probe doubles the wait, up to the five-minute cap.
func TestBreakerDoublesTheCooldownOnAFailedProbe(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	b := newCircuitBreaker().withClock(func() time.Time { return now })

	for i := 0; i < breakerFailureThreshold; i++ {
		b.record("p", false)
	}
	// First probe after the base cooldown fails -> the next wait is doubled.
	now = now.Add(breakerCooldown + time.Second)
	if !b.allow("p") {
		t.Fatal("the first cooldown must let a probe through")
	}
	b.record("p", false)

	now = now.Add(breakerCooldown + time.Second)
	if b.allow("p") {
		t.Fatal("the doubled cooldown must not have elapsed yet")
	}
	now = now.Add(breakerCooldown + time.Second)
	if !b.allow("p") {
		t.Fatal("a probe must be let through once the doubled cooldown elapses")
	}

	// Keep failing probes: the wait keeps doubling and caps at five minutes.
	wait := 2 * breakerCooldown
	for i := 0; i < 10; i++ {
		b.record("p", false)
		if wait < breakerMaxCooldown {
			wait *= 2
			if wait > breakerMaxCooldown {
				wait = breakerMaxCooldown
			}
		}
		now = now.Add(wait - time.Second)
		if b.allow("p") {
			t.Fatalf("probe %d let through before its cooldown", i)
		}
		now = now.Add(time.Second)
		if !b.allow("p") {
			t.Fatalf("probe %d not let through after a %s cooldown", i, wait)
		}
	}
}

// A request that was already in flight when the circuit tripped reports its
// success afterwards. That is not evidence the channel is healthy now, so it
// must not silently close the circuit and undo the breaker.
func TestBreakerIgnoresALateSuccessWhileOpen(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	b := newCircuitBreaker().withClock(func() time.Time { return now })
	for i := 0; i < breakerFailureThreshold; i++ {
		b.record("p", false)
	}
	if b.allow("p") {
		t.Fatal("the circuit must be open")
	}

	b.record("p", true)
	if b.allow("p") {
		t.Fatal("a late success must not close an open circuit")
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
		"demo-model": {
			{Provider: "a", BaseURL: dead.URL, UpstreamModel: "a", CredentialEnv: "TEST_BREAKER_KEY_A", Priority: 1, Weight: 1, TimeoutMS: 5000},
			{Provider: "b", BaseURL: good.URL, UpstreamModel: "b", CredentialEnv: "TEST_BREAKER_KEY_B", Priority: 1, Weight: 1, TimeoutMS: 5000},
		},
	}})

	// Drive "a" open.
	for i := 0; i < breakerFailureThreshold; i++ {
		s.breaker.record("a", false)
	}

	rec := newAttemptRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	req = req.WithContext(withAttemptRecorder(req.Context(), rec))
	w := httptest.NewRecorder()
	s.serveDirect(w, req)

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
	// A skipped line is not a Routing Attempt: only b was dialled, so the
	// attempt count is 1, not 2. The request log turns this into the failover
	// flag, so counting the skip would report a failover that never happened.
	if got := w.Header().Get(headerAttempts); got != "1" {
		t.Fatalf("%s = %q, want \"1\" (the skipped line is not an attempt)", headerAttempts, got)
	}
}

// When every line is open the request is refused with a clear 503 rather than an
// empty "all channels failed".
func TestServeDirectRefusesWhenEveryChannelIsOpen(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	t.Setenv("TEST_BREAKER_KEY_C", "k")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "c", BaseURL: up.URL, UpstreamModel: "c", CredentialEnv: "TEST_BREAKER_KEY_C", Priority: 1, Weight: 1, TimeoutMS: 5000},
		},
	}})
	for i := 0; i < breakerFailureThreshold; i++ {
		s.breaker.record("c", false)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
	s.serveDirect(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "channels_circuit_open") {
		t.Fatalf("body = %s, want channels_circuit_open", rec.Body.String())
	}
}
