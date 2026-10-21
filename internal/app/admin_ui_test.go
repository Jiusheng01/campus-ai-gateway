package app

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// adminUIHeaderExpectations are the headers every Admin UI response must carry.
// They live next to the handler's own constant so the assertion and the
// implementation cannot drift apart silently.
func adminUIHeaderExpectations() map[string]string {
	return map[string]string{
		"Content-Security-Policy": adminUIContentSecurityPolicy,
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Permissions-Policy":      "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
	}
}

// P1-06. The Admin UI is the operator's console: it keeps access tokens in
// memory and issues privileged control-plane calls on their behalf. Served
// without a CSP it is one injected script away from exfiltration; without
// X-Frame-Options it can be framed for clickjacking; without nosniff a
// mislabelled response can be reinterpreted as script.
//
// The handler used to set only Content-Type and Cache-Control, so every
// assertion here fails on the old implementation.
func TestAdminUISetsSecurityHeadersOnEveryResponse(t *testing.T) {
	s := &Server{cfg: testConfig()}

	// Resolve the hashed asset names from the embedded build rather than
	// hardcoding them, so a frontend rebuild does not break this test.
	jsAsset, cssAsset := adminUIAssetFixtures(t)

	cases := []struct {
		name     string
		path     string
		wantCode int
	}{
		{"index", "/admin-ui", http.StatusOK},
		{"index with slash", "/admin-ui/", http.StatusOK},
		{"script asset", jsAsset, http.StatusOK},
		{"stylesheet asset", cssAsset, http.StatusOK},
		{"client route fallback", "/admin-ui/settings/account", http.StatusOK},
		{"unknown asset", "/admin-ui/assets/does-not-exist.js", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			s.serveAdminUI(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("GET %s: expected %d, got %d: %s", tc.path, tc.wantCode, rec.Code, rec.Body.String())
			}
			for header, want := range adminUIHeaderExpectations() {
				if got := rec.Header().Get(header); got != want {
					t.Fatalf("GET %s: header %s = %q, want %q", tc.path, header, got, want)
				}
			}
		})
	}
}

// The cache semantics the UI depends on must survive the header change:
// index.html stays revalidated, hashed assets stay immutable.
func TestAdminUIKeepsCacheSemantics(t *testing.T) {
	s := &Server{cfg: testConfig()}
	jsAsset, _ := adminUIAssetFixtures(t)

	req := httptest.NewRequest(http.MethodGet, "/admin-ui", nil)
	rec := httptest.NewRecorder()
	s.serveAdminUI(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index.html Cache-Control = %q, want no-cache", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("index.html Content-Type = %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, jsAsset, nil)
	rec = httptest.NewRecorder()
	s.serveAdminUI(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset Cache-Control = %q, want immutable", got)
	}
}

// The handler is mounted under /admin-ui and /admin-ui/ only. Anything else
// that reaches it is a 404, and still carries the headers.
func TestAdminUIRejectsForeignPathsWithHeaders(t *testing.T) {
	s := &Server{cfg: testConfig()}
	for _, path := range []string{"/", "/v1/models", "/admin/users", "/admin-ui/../../etc/passwd"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.serveAdminUI(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s: expected 404, got %d", path, rec.Code)
		}
		for header, want := range adminUIHeaderExpectations() {
			if got := rec.Header().Get(header); got != want {
				t.Fatalf("GET %s: header %s = %q, want %q", path, header, got, want)
			}
		}
	}
}

// The CSP must not be weakened to make the app build or run: no
// 'unsafe-inline' or 'unsafe-eval' in script-src, and a restrictive
// default-src.
func TestAdminUIContentSecurityPolicyIsStrict(t *testing.T) {
	policy := adminUIContentSecurityPolicy
	for _, forbidden := range []string{"script-src 'self' 'unsafe-inline'", "'unsafe-eval'", "default-src *"} {
		if strings.Contains(policy, forbidden) {
			t.Fatalf("policy contains %q: %s", forbidden, policy)
		}
	}
	for _, required := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'none'", "base-uri 'none'", "connect-src 'self'", "img-src 'self' data:"} {
		if !strings.Contains(policy, required) {
			t.Fatalf("policy is missing %q: %s", required, policy)
		}
	}
}

// The personal workspace is the same build served under /console. A direct
// load or a refresh of an addressable page must return the shell, while a path
// that looks like a file and does not exist stays a 404 rather than being
// swallowed by the fallback.
func TestConsoleUIServesTheSameShell(t *testing.T) {
	s := &Server{cfg: testConfig()}

	for _, path := range []string{"/console", "/console/", "/console/keys", "/console/usage", "/console/profile"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.serveConsoleUI(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d: %s", path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("GET %s: Content-Type = %q", path, got)
		}
		if !strings.Contains(rec.Body.String(), `id="root"`) || !strings.Contains(rec.Body.String(), "/admin-ui/assets/") {
			t.Fatalf("GET %s did not serve the shared app shell: %s", path, rec.Body.String())
		}
		for header, want := range adminUIHeaderExpectations() {
			if got := rec.Header().Get(header); got != want {
				t.Fatalf("GET %s: header %s = %q, want %q", path, header, got, want)
			}
		}
	}

	for _, path := range []string{"/console/assets/does-not-exist.js", "/console/app.js", "/admin-ui", "/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.serveConsoleUI(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s: expected 404, got %d", path, rec.Code)
		}
	}
}

// adminUIAssetFixtures returns one script and one stylesheet URL from the
// embedded production build. Glob paths are relative to the embedded root
// ("adminui/dist/..."); the handler mounts that root under /admin-ui/, so the
// prefix is stripped to build a URL.
func adminUIAssetFixtures(t *testing.T) (js, css string) {
	t.Helper()
	const distPrefix = "adminui/dist/"
	jsPaths, err := fs.Glob(adminUIFS, distPrefix+"assets/*.js")
	if err != nil || len(jsPaths) == 0 {
		t.Fatalf("embedded build has no script asset (%v)", err)
	}
	cssPaths, err := fs.Glob(adminUIFS, distPrefix+"assets/*.css")
	if err != nil || len(cssPaths) == 0 {
		t.Fatalf("embedded build has no stylesheet asset (%v)", err)
	}
	return "/admin-ui/" + strings.TrimPrefix(jsPaths[0], distPrefix),
		"/admin-ui/" + strings.TrimPrefix(cssPaths[0], distPrefix)
}
