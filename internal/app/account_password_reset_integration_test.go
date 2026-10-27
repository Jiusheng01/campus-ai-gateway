package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

// The reset is a property of a single SQL statement — the hash, the flag and
// auth_version move together or not at all — so the fake cannot prove it. These
// tests run against a migrated database, like the Tenant assignment suite.

func resetIntegrationStore(t *testing.T) *storage.Postgres {
	t.Helper()
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	return pg
}

func TestAdminPasswordResetRunsOnTheRealSchema(t *testing.T) {
	pg := resetIntegrationStore(t)
	s := tenantAssignmentServer(t, pg)
	tenantAssignmentAccount(t, pg, "campus-admin", []string{RoleSuperAdmin})
	victim := tenantAssignmentAccount(t, pg, "zhangsan", []string{RoleMember})

	adminToken := tenantAssignmentToken(t, s, "campus-admin")
	// The account holder is signed in before the operator acts.
	victimToken := tenantAssignmentToken(t, s, "zhangsan")

	rec := tenantAssignmentCall(t, s, adminToken, http.MethodPost,
		"/admin/users/"+victim.ID+"/password-reset", `{"new_password":"Interim#2026Reset"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset = %d: %s", rec.Code, rec.Body.String())
	}

	// Read the row back through the same projection the login path scans, so a
	// column that was added to one query and not the other shows up here.
	stored, err := pg.FindAdminUserByID(context.Background(), victim.ID)
	if err != nil {
		t.Fatalf("re-read the account: %v", err)
	}
	if !stored.PasswordResetRequired {
		t.Fatal("password_reset_required was not persisted")
	}
	if stored.AuthVersion != victim.AuthVersion+1 {
		t.Fatalf("auth_version = %d, want %d", stored.AuthVersion, victim.AuthVersion+1)
	}

	// The session opened before the reset is off the wire.
	if got := tenantAssignmentCall(t, s, victimToken, http.MethodGet, "/admin/whoami", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("the pre-reset session = %d, want 401", got.Code)
	}

	// The handed-over password signs in, but only far enough to replace itself.
	resetToken := accessTokenWith(t, s, "zhangsan", "Interim#2026Reset")
	if got := tenantAssignmentCall(t, s, resetToken, http.MethodGet, "/admin/request-logs", ""); got.Code != http.StatusForbidden {
		t.Fatalf("a read through the fence = %d, want 403: %s", got.Code, got.Body.String())
	}

	// Choosing a password of their own clears the flag on the real schema. This
	// is the half the fake could not prove: ClearAdminPasswordReset is a
	// separate statement.
	change := resetPasswordChangeCall(t, s, resetToken,
		`{"current_password":"Interim#2026Reset","new_password":"Chosen#2026ByMe"}`)
	if change.Code != http.StatusOK {
		t.Fatalf("password change inside the fence = %d: %s", change.Code, change.Body.String())
	}

	after, err := pg.FindAdminUserByID(context.Background(), victim.ID)
	if err != nil {
		t.Fatalf("re-read the account: %v", err)
	}
	if after.PasswordResetRequired {
		t.Fatal("choosing a new password did not clear password_reset_required")
	}
	if after.Status != model.AdminStatusEnabled {
		t.Fatalf("the account must survive the reset enabled, got status %d", after.Status)
	}

	// And the account works again, with the password it chose.
	finalToken := accessTokenWith(t, s, "zhangsan", "Chosen#2026ByMe")
	if got := resetWhoAmICall(t, s, finalToken); got.Code != http.StatusOK {
		t.Fatalf("the account after choosing its own password = %d, want 200: %s", got.Code, got.Body.String())
	}
}

// accessTokenWith signs in and returns the access token, failing the test when
// the login is refused.
func accessTokenWith(t *testing.T, s *Server, username, password string) string {
	t.Helper()
	tokens, err := s.adminAuth.login(context.Background(), username, password, "10.0.0.11", "test-agent")
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	return tokens.AccessToken
}

// resetPasswordChangeCall drives the self-service password change through the
// middleware that owns it. /admin/password is registered on the mux directly
// rather than under the management router, so a probe has to name its handler.
func resetPasswordChangeCall(t *testing.T, s *Server, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postJSON(t, s.adminMiddleware(http.HandlerFunc(s.handleAdminPasswordChange)), http.MethodPost,
		"/admin/password", body, map[string]string{"Authorization": "Bearer " + token})
}

// resetWhoAmICall reads the identity through the management router, which is
// where /admin/whoami is served. The Tenant suite's helper answers 404 for that
// path because it only wires the account handlers.
func resetWhoAmICall(t *testing.T, s *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Request-Id", "reset-integration-test")
	rec := httptest.NewRecorder()
	s.adminMiddleware(http.HandlerFunc(s.handleAdmin)).ServeHTTP(rec, req)
	return rec
}
