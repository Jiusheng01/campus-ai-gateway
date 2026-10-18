package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// trustedHeaderFromPeer drives one trusted-header request through the real
// admin middleware from an explicit peer address, so the only thing that
// differs between the cases below is who is calling.
func trustedHeaderFromPeer(t *testing.T, s *Server, peer, secret string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/admin/whoami", nil)
	req.RemoteAddr = peer
	req.Header.Set("X-Request-Id", "test-req")
	req.Header.Set(s.cfg.AdminUserHeader, "zhang.san")
	req.Header.Set(s.cfg.AdminRolesHeader, RoleSuperAdmin)
	req.Header.Set("X-Campus-Auth-Secret", secret)

	rec := httptest.NewRecorder()
	s.adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec
}

// A peer outside ADMIN_PROXY_CIDRS cannot become an administrator, however
// correct its shared secret is. This is the whole point of the list: in this
// mode the secret is the only other condition, so on a port that is reachable
// from the campus network a leaked string would otherwise be enough.
func TestTrustedHeaderRefusesAPeerOutsideTheAllowedRange(t *testing.T) {
	cfg := trustedHeaderConfig()
	cfg.AdminProxyCIDRs = []string{"10.0.0.0/8"}
	s, _ := newTestServer(t, cfg)

	rec := trustedHeaderFromPeer(t, s, "203.0.113.9:51234", cfg.AdminProxySecret)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("an out-of-range peer with the correct secret returned %d, want 401", rec.Code)
	}
}

// An out-of-range peer learns nothing about the secret: the answer is the same
// whether it presents the right one or the wrong one. A differential response
// here would turn the port into an oracle for guessing the string.
func TestTrustedHeaderPeerRefusalDoesNotDependOnTheSecret(t *testing.T) {
	cfg := trustedHeaderConfig()
	cfg.AdminProxyCIDRs = []string{"10.0.0.0/8"}
	s, _ := newTestServer(t, cfg)

	correct := trustedHeaderFromPeer(t, s, "203.0.113.9:51234", cfg.AdminProxySecret)
	wrong := trustedHeaderFromPeer(t, s, "203.0.113.9:51234", "not-the-secret")

	if correct.Code != wrong.Code {
		t.Fatalf("out-of-range status differs by secret (%d vs %d), which leaks the secret", correct.Code, wrong.Code)
	}
	if got, want := errorCode(t, correct), errorCode(t, wrong); got != want {
		t.Fatalf("out-of-range error code differs by secret (%q vs %q)", got, want)
	}
	if correct.Body.String() != wrong.Body.String() {
		t.Fatalf("out-of-range body differs by secret:\n%s\n%s", correct.Body.String(), wrong.Body.String())
	}
}

// The allowed range admits the proxy it names, and it accepts the same entry
// shapes as TRUSTED_PROXY_CIDRS does - a network in CIDR form and a bare
// address - because both are parsed by the same helper.
func TestTrustedHeaderAdmitsAPeerInsideTheAllowedRange(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry string
		peer  string
	}{
		{"cidr network", "10.0.0.0/8", "10.1.2.3:51234"},
		{"bare address", "127.0.0.1", "127.0.0.1:51234"},
		{"ipv6 loopback", "::1/128", "[::1]:51234"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := trustedHeaderConfig()
			cfg.AdminProxyCIDRs = []string{tc.entry}
			s, _ := newTestServer(t, cfg)

			if rec := trustedHeaderFromPeer(t, s, tc.peer, cfg.AdminProxySecret); rec.Code != http.StatusOK {
				t.Fatalf("peer %s with entry %q returned %d, want 200: %s", tc.peer, tc.entry, rec.Code, rec.Body.String())
			}
		})
	}
}

// Leaving the list unset keeps the historical behaviour: any peer may attempt
// the authentication. That is a compatibility choice rather than a safe one,
// which is why startup warns about it out loud.
func TestTrustedHeaderWithoutAPeerRangeKeepsAnyPeer(t *testing.T) {
	cfg := trustedHeaderConfig()
	cfg.AdminProxyCIDRs = nil
	s, _ := newTestServer(t, cfg)

	if rec := trustedHeaderFromPeer(t, s, "203.0.113.9:51234", cfg.AdminProxySecret); rec.Code != http.StatusOK {
		t.Fatalf("with no peer list the old behaviour must hold, got %d: %s", rec.Code, rec.Body.String())
	}
	// A wrong secret is still a wrong secret.
	if rec := trustedHeaderFromPeer(t, s, "203.0.113.9:51234", "not-the-secret"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong secret must still be refused, got %d", rec.Code)
	}
}

// The peer is the direct connection, never anything the caller can write.
// Otherwise the list would be advisory: an out-of-range caller would simply
// claim to be the proxy in a forwarding header.
func TestTrustedHeaderPeerComesFromTheConnectionNotAHeader(t *testing.T) {
	cfg := trustedHeaderConfig()
	cfg.AdminProxyCIDRs = []string{"10.0.0.0/8"}
	s, _ := newTestServer(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/admin/whoami", nil)
	req.RemoteAddr = "203.0.113.9:51234"
	req.Header.Set("X-Request-Id", "test-req")
	req.Header.Set("X-Forwarded-For", "10.1.2.3")
	req.Header.Set("X-Real-Ip", "10.1.2.3")
	req.Header.Set(s.cfg.AdminUserHeader, "zhang.san")
	req.Header.Set(s.cfg.AdminRolesHeader, RoleSuperAdmin)
	req.Header.Set("X-Campus-Auth-Secret", cfg.AdminProxySecret)

	rec := httptest.NewRecorder()
	s.adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a forwarding header must not be able to move the caller into the allowed range, got %d", rec.Code)
	}
}

// The length floor belongs to trusted-header mode alone. The other two modes do
// not read ADMIN_PROXY_SECRET at all, so a short leftover value there must not
// stop the gateway from starting.
func TestProxySecretLengthFloorAppliesOnlyToTrustedHeaderMode(t *testing.T) {
	base := func(t *testing.T) {
		setConfigEnv(t, "")
		t.Setenv("ADMIN_PROXY_SECRET", "short")
	}

	for _, mode := range []string{"session", "token"} {
		t.Run(mode, func(t *testing.T) {
			base(t)
			t.Setenv("ADMIN_AUTH_MODE", mode)
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("mode %q must not be constrained by the shared-secret length: %v", mode, err)
			}
			if cfg.AdminAuthModeTrustedHeader() {
				t.Fatalf("mode %q must not report itself as trusted-header", mode)
			}
		})
	}

	t.Run("trusted-header", func(t *testing.T) {
		base(t)
		t.Setenv("ADMIN_AUTH_MODE", "trusted-header")
		if _, err := LoadConfig(); err == nil {
			t.Fatal("trusted-header mode must refuse a shared secret shorter than the floor")
		}
	})
}
