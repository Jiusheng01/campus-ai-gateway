package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ipLiteral matches an IPv4 literal. ADR-0009 keeps network detail in the
// operational log and the security event; a caller-facing response must never
// carry it.
var ipLiteral = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)

// The Provider transport must not be rerouted by the deployment's environment.
// With HTTP(S)_PROXY set, http.ProxyFromEnvironment would dial the proxy, and
// Dialer.Control would then see the proxy's address rather than the Provider's:
// the address denial and the per-connection DNS revalidation would both be
// bypassed.
func TestProviderTransportDoesNotUseAnEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:3128")
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:3128")
	t.Setenv("ALL_PROXY", "http://proxy.invalid:3128")

	pool := &transportPool{}
	transport := pool.get(time.Minute, strictEgressPolicy())
	if transport.Proxy != nil {
		t.Fatal("the provider transport must dial the Provider directly, not through an environment proxy")
	}
}

// The dial-time policy is handed the address the resolver produced for this
// connection, so every case here is also the answer to "is DNS revalidated?":
// the check runs once per dial and sees the current result, not a cached one.
func TestStrictEgressPolicyDeniesDangerousAddresses(t *testing.T) {
	policy := strictEgressPolicy()
	for _, tc := range []struct {
		name    string
		address string
		want    string
	}{
		{name: "ipv4 loopback", address: "127.0.0.1:443", want: "loopback"},
		{name: "ipv6 loopback", address: "[::1]:443", want: "loopback"},
		{name: "ipv4 link-local unicast", address: "169.254.10.10:443", want: "link-local unicast"},
		{name: "ipv6 link-local unicast", address: "[fe80::1]:443", want: "link-local unicast"},
		{name: "ipv6 link-local multicast", address: "[ff02::1]:443", want: "link-local multicast"},
		{name: "ipv4 multicast", address: "224.0.0.1:443", want: "multicast"},
		{name: "ipv6 multicast", address: "[ff0e::1]:443", want: "multicast"},
		{name: "ipv4 unspecified", address: "0.0.0.0:443", want: "unspecified"},
		{name: "ipv6 unspecified", address: "[::]:443", want: "unspecified"},
		// 0.0.0.0/8 routes to local interfaces on Linux, so a name that
		// resolves anywhere in it is a local-service target, not an upstream.
		{name: "zero network low", address: "0.1.2.3:443", want: "0.0.0.0/8"},
		{name: "zero network high", address: "0.255.255.255:443", want: "0.0.0.0/8"},
		{name: "aws metadata", address: "169.254.169.254:80", want: "cloud metadata"},
		{name: "ec2 ipv6 metadata", address: "[fd00:ec2::254]:80", want: "cloud metadata"},
		{name: "alibaba metadata", address: "100.100.100.200:80", want: "cloud metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := policy.control("tcp", tc.address, nil)
			if err == nil {
				t.Fatalf("%s must be refused", tc.address)
			}
			var denied *egressDeniedError
			if !errors.As(err, &denied) {
				t.Fatalf("refusal must be the recognisable egress denial, got %T: %v", err, err)
			}
			if !strings.Contains(denied.Reason, tc.want) {
				t.Fatalf("reason for %s = %q, want it to name %q", tc.address, denied.Reason, tc.want)
			}
		})
	}
}

// A public address is an ordinary Provider target and must not be refused.
func TestStrictEgressPolicyAllowsPublicAddresses(t *testing.T) {
	policy := strictEgressPolicy()
	for _, address := range []string{
		"93.184.216.34:443",
		"[2606:4700:4700::1111]:443",
	} {
		if err := policy.control("tcp", address, nil); err != nil {
			t.Fatalf("%s must be allowed: %v", address, err)
		}
	}
}

// Tests run their upstreams on 127.0.0.1. The permissive policy is the explicit
// test injection that keeps them possible without a production switch.
func TestPermissiveEgressPolicyAllowsLoopback(t *testing.T) {
	policy := permissiveEgressPolicy()
	for _, address := range []string{"127.0.0.1:8080", "[::1]:8080"} {
		if err := policy.control("tcp", address, nil); err != nil {
			t.Fatalf("%s must be allowed under the test policy: %v", address, err)
		}
	}
}

// The strict policy is wired into the dialer, not merely available as a
// predicate: a loopback Provider is refused before a connection is made, and
// the failure names the channel and the reason.
func TestStrictServerDeniesALoopbackProviderBeforeConnecting(t *testing.T) {
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
	}))
	defer upstream.Close()

	t.Setenv("TEST_EGRESS_KEY", "sk-test")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_EGRESS_KEY", Priority: 1},
		},
	}})
	s.egress = strictEgressPolicy()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-smart"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "provider_egress_denied") {
		t.Fatalf("the failure must carry the egress denial code, got %s", body)
	}
	if !strings.Contains(body, "qwen") || !strings.Contains(body, "loopback") {
		t.Fatalf("the failure must name the channel and the reason, got %s", body)
	}
	if ipLiteral.MatchString(body) {
		t.Fatalf("the business response must not carry the resolved address: %s", body)
	}
	if n := atomic.LoadInt64(&hits); n != 0 {
		t.Fatalf("a denied target must never be connected to; upstream was hit %d time(s)", n)
	}
}

// The HTTP allowlist is enforced at egress, not only while the form is saved. A
// row written before the rule existed — or whose host was removed from the
// allowlist afterwards — must still be refused before any plaintext connection
// is made.
func TestServeDirectDeniesAStoredHTTPProviderOutsideTheAllowlist(t *testing.T) {
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
	}))
	defer upstream.Close()

	t.Setenv("TEST_EGRESS_STORED_HTTP_KEY", "sk-test")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_EGRESS_STORED_HTTP_KEY", Priority: 1},
		},
	}})
	// The stored row predates this deployment's allowlist: 127.0.0.1 is not in it.
	s.cfg.ProviderHTTPAllowlist = []string{"10.0.0.0/8"}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-smart"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "provider_egress_denied") {
		t.Fatalf("the failure must carry the egress denial code: %s", body)
	}
	if !strings.Contains(body, "PROVIDER_HTTP_ALLOWLIST") {
		t.Fatalf("the failure must name the allowlist rule: %s", body)
	}
	if n := atomic.LoadInt64(&hits); n != 0 {
		t.Fatalf("a non-allowlisted plaintext target must never be dialled; upstream was hit %d time(s)", n)
	}
}

// The same stored http:// row is allowed when its host is on the allowlist:
// the check is the explicit exception, not a blanket refusal of plaintext.
func TestServeDirectAllowsAnAllowlistedStoredHTTPProvider(t *testing.T) {
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	t.Setenv("TEST_EGRESS_STORED_HTTP_ALLOWED_KEY", "sk-test")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_EGRESS_STORED_HTTP_ALLOWED_KEY", Priority: 1},
		},
	}})
	s.cfg.ProviderHTTPAllowlist = []string{"127.0.0.1"}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-smart"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Fatalf("an allowlisted plaintext target must be dialled once, upstream was hit %d time(s)", n)
	}
}

// A denial is a security event, not only a request failure: it is the record
// that tells an auditor someone pointed a channel at the gateway or its cloud
// metadata service.
func TestEgressDenialRecordsASecurityEvent(t *testing.T) {
	pg := usagePostgres(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	t.Setenv("TEST_EGRESS_EVENT_KEY", "sk-test")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_EGRESS_EVENT_KEY", Priority: 1},
		},
	}})
	s.pg = pg
	s.egress = strictEgressPolicy()

	const reqID = "req_egress_denied_event"
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-smart"}`))
	req.Header.Set("X-Request-Id", reqID)
	req.Header.Set("Content-Type", "application/json")
	s.serveDirect(httptest.NewRecorder(), req)

	events, err := pg.ListSecurityEvents(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListSecurityEvents: %v", err)
	}
	for _, event := range events {
		if event.RequestID != reqID {
			continue
		}
		if event.EventType != egressDenialEventType {
			t.Fatalf("event type = %q, want %q", event.EventType, egressDenialEventType)
		}
		if !strings.Contains(event.Detail, "qwen") || !strings.Contains(event.Detail, "loopback") {
			t.Fatalf("the event detail must name the channel and reason: %q", event.Detail)
		}
		return
	}
	t.Fatalf("no security event was recorded for the denied request (%d recent events)", len(events))
}
