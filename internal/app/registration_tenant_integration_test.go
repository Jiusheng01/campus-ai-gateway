package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Public registration's central promise is a database property: the account
// and its configured Tenant Membership are written together, or neither is.
// These tests exercise the HTTP handler against a migrated database because an
// in-memory fake can only confirm that the fake rolls back.

const registrationIntegrationPassword = "Campus#2026Gate"

func registrationTenantServer(t *testing.T, pg *storage.Postgres, tenantID string) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.PublicRegisterTenantID = tenantID
	s := &Server{cfg: cfg, pg: pg, mux: http.NewServeMux()}
	s.adminAuth = newAdminAuthService(pg, newFakeLimiter(8), []byte("registration-tenant-test-secret"), cfg).
		withRegistrationLimiter(newFakeRegistrationLimiter(0))
	s.routes()
	return s
}

func registrationTenantClear(t *testing.T, pg *storage.Postgres) {
	t.Helper()
	tenantAssignmentClear(t, pg)
}

func registrationTenantSeedAdmin(t *testing.T, pg *storage.Postgres) {
	t.Helper()
	tenantAssignmentAccount(t, pg, uniqueAccountName("reg-admin"), []string{RoleSuperAdmin})
}

func registrationRequest(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "registration-integration-test")
	req.RemoteAddr = "10.0.0.44:1234"
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func registrationBody(username string) string {
	return `{"username":"` + username + `","display_name":"","password":"` +
		registrationIntegrationPassword + `","confirm_password":"` + registrationIntegrationPassword + `"}`
}

func registrationAccount(t *testing.T, pg *storage.Postgres, username string) (accountID string, tenantID string) {
	t.Helper()
	var tenant *string
	err := pg.Pool.QueryRow(context.Background(), `
SELECT id::text, tenant_id::text FROM gateway_admin_user WHERE lower(username)=lower($1)`, username).
		Scan(&accountID, &tenant)
	if err != nil {
		t.Fatalf("read account %s: %v", username, err)
	}
	if tenant != nil {
		tenantID = *tenant
	}
	return accountID, tenantID
}

func registrationSessions(t *testing.T, pg *storage.Postgres, accountID string) int64 {
	t.Helper()
	var n int64
	if err := pg.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM gateway_admin_session WHERE user_id=$1::uuid`, accountID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func TestRegistrationAssignsTheTenantAndOpensTheSessionTogether(t *testing.T) {
	pg := usagePostgres(t)
	registrationTenantClear(t, pg)
	tenant := usageSeedTenant(t, pg, "reg-valid", 0, 0, 0)
	registrationTenantSeedAdmin(t, pg)
	s := registrationTenantServer(t, pg, tenant.ID)

	username := uniqueAccountName("reg-member")
	rec := registrationRequest(t, s, registrationBody(username))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register = %d: %s", rec.Code, rec.Body.String())
	}

	accountID, tenantID := registrationAccount(t, pg, username)
	if tenantID != tenant.ID {
		t.Fatalf("stored membership = %q, want %q", tenantID, tenant.ID)
	}
	if got := registrationSessions(t, pg, accountID); got != 1 {
		t.Fatalf("registration sessions = %d, want exactly the one it returned", got)
	}

	// The response is a session, not a membership document; the account id it
	// names must be the same row the membership was written to.
	var body struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.User.ID != accountID {
		t.Fatalf("response account = %q, stored account = %q", body.User.ID, accountID)
	}
}

func TestRegistrationFailsAndWritesNothingWhenTheTenantIsMissing(t *testing.T) {
	pg := usagePostgres(t)
	registrationTenantClear(t, pg)
	registrationTenantSeedAdmin(t, pg)
	s := registrationTenantServer(t, pg, uuid.NewString())

	username := uniqueAccountName("reg-missing")
	rec := registrationRequest(t, s, registrationBody(username))
	if rec.Code != http.StatusConflict {
		t.Fatalf("register = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "registration_tenant_not_found" {
		t.Fatalf("error code = %q, want registration_tenant_not_found", code)
	}
	if got, _ := registrationAccountOrEmpty(t, pg, username); got != "" {
		t.Fatalf("a refused registration left account %q", got)
	}
}

func TestRegistrationFailsAndWritesNothingWhenTheTenantIsDisabled(t *testing.T) {
	pg := usagePostgres(t)
	registrationTenantClear(t, pg)
	tenant := usageSeedTenant(t, pg, "reg-disabled", 0, 0, 0)
	if _, err := pg.Pool.Exec(context.Background(),
		`UPDATE gateway_tenant SET status=2 WHERE id=$1::uuid`, tenant.ID); err != nil {
		t.Fatalf("disable tenant: %v", err)
	}
	registrationTenantSeedAdmin(t, pg)
	s := registrationTenantServer(t, pg, tenant.ID)

	username := uniqueAccountName("reg-disabled-member")
	rec := registrationRequest(t, s, registrationBody(username))
	if rec.Code != http.StatusConflict {
		t.Fatalf("register = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "registration_tenant_disabled" {
		t.Fatalf("error code = %q, want registration_tenant_disabled", code)
	}
	if accountID, _ := registrationAccountOrEmpty(t, pg, username); accountID != "" {
		t.Fatalf("a refused registration left account %q", accountID)
	}
}

// A Tenant deleted while a signup is in flight must turn into a refusal, not a
// committed account whose membership was decided from a stale read.
func TestRegistrationLosesCleanlyToAConcurrentTenantDelete(t *testing.T) {
	pg := usagePostgres(t)
	registrationTenantClear(t, pg)
	tenant := usageSeedTenant(t, pg, "reg-race", 0, 0, 0)
	registrationTenantSeedAdmin(t, pg)
	s := registrationTenantServer(t, pg, tenant.ID)

	ctx := context.Background()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM gateway_tenant WHERE id=$1::uuid`, tenant.ID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("delete tenant: %v", err)
	}

	username := uniqueAccountName("reg-race-member")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- registrationRequest(t, s, registrationBody(username))
	}()

	// Let the request reach the locked Tenant row, then commit the DELETE.
	time.Sleep(150 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit delete: %v", err)
	}

	select {
	case rec := <-done:
		if rec.Code != http.StatusConflict {
			t.Fatalf("register after concurrent delete = %d, want 409: %s", rec.Code, rec.Body.String())
		}
		if code := errorCode(t, rec); code != "registration_tenant_not_found" {
			t.Fatalf("error code = %q, want registration_tenant_not_found", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("registration did not finish after the Tenant delete committed")
	}

	if accountID, _ := registrationAccountOrEmpty(t, pg, username); accountID != "" {
		t.Fatalf("the lost race left account %q", accountID)
	}
}

// A duplicate username is a conflict and must leave the original account and
// its membership untouched.
func TestRegistrationDuplicateUsernameLeavesTheOriginalAccount(t *testing.T) {
	pg := usagePostgres(t)
	registrationTenantClear(t, pg)
	tenant := usageSeedTenant(t, pg, "reg-duplicate", 0, 0, 0)
	registrationTenantSeedAdmin(t, pg)
	s := registrationTenantServer(t, pg, tenant.ID)

	existing := tenantAssignmentAccount(t, pg, uniqueAccountName("reg-existing"), []string{RoleMember})
	if _, err := pg.AssignAdminUserTenant(context.Background(), existing.ID, tenant.ID); err != nil {
		t.Fatalf("assign existing: %v", err)
	}
	before, err := pg.FindAdminUserByID(context.Background(), existing.ID)
	if err != nil {
		t.Fatal(err)
	}

	rec := registrationRequest(t, s, registrationBody(strings.ToUpper(existing.Username)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate registration = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "username_taken" {
		t.Fatalf("error code = %q, want username_taken", code)
	}

	after, err := pg.FindAdminUserByID(context.Background(), existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TenantID != before.TenantID || after.PasswordHash != before.PasswordHash || after.AuthVersion != before.AuthVersion {
		t.Fatal("a duplicate registration modified the existing account")
	}
}

func TestConcurrentRegistrationsCreateExactlyOneAccount(t *testing.T) {
	pg := usagePostgres(t)
	registrationTenantClear(t, pg)
	tenant := usageSeedTenant(t, pg, "reg-concurrent", 0, 0, 0)
	registrationTenantSeedAdmin(t, pg)
	s := registrationTenantServer(t, pg, tenant.ID)

	username := uniqueAccountName("reg-concurrent-member")
	const attempts = 6
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var created, conflict int
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := registrationRequest(t, s, registrationBody(username))
			mu.Lock()
			defer mu.Unlock()
			switch rec.Code {
			case http.StatusCreated:
				created++
			case http.StatusConflict:
				conflict++
			default:
				t.Errorf("unexpected registration status %d: %s", rec.Code, rec.Body.String())
			}
		}()
	}
	close(start)
	wg.Wait()

	if created != 1 || conflict != attempts-1 {
		t.Fatalf("created=%d conflict=%d, want 1 and %d", created, conflict, attempts-1)
	}
	accountID, tenantID := registrationAccount(t, pg, username)
	if tenantID != tenant.ID {
		t.Fatalf("the surviving account has membership %q, want %q", tenantID, tenant.ID)
	}
	if got := registrationSessions(t, pg, accountID); got != 1 {
		t.Fatalf("sessions for the surviving account = %d, want 1", got)
	}
}

// Startup precheck: a configured default Tenant must exist and be enabled when
// registration is open. When registration is closed, a stale value is inert and
// must not stop the gateway.
func TestCheckRegistrationTenant(t *testing.T) {
	pg := usagePostgres(t)
	registrationTenantClear(t, pg)
	ctx := context.Background()

	enabled := usageSeedTenant(t, pg, "reg-startup-enabled", 0, 0, 0)
	disabled := usageSeedTenant(t, pg, "reg-startup-disabled", 0, 0, 0)
	if _, err := pg.Pool.Exec(ctx, `UPDATE gateway_tenant SET status=2 WHERE id=$1::uuid`, disabled.ID); err != nil {
		t.Fatalf("disable tenant: %v", err)
	}

	cfg := testConfig()
	cfg.PublicRegisterTenantID = enabled.ID
	if err := checkRegistrationTenant(ctx, pg, cfg); err != nil {
		t.Fatalf("enabled Tenant refused at startup: %v", err)
	}

	cfg.PublicRegisterTenantID = uuid.NewString()
	if err := checkRegistrationTenant(ctx, pg, cfg); err == nil {
		t.Fatal("a missing Tenant must stop startup while registration is open")
	}

	cfg.PublicRegisterTenantID = disabled.ID
	if err := checkRegistrationTenant(ctx, pg, cfg); err == nil {
		t.Fatal("a disabled Tenant must stop startup while registration is open")
	}

	// Registration closed: the stale value is allowed to sit there.
	cfg.PublicRegistrationEnabled = false
	if err := checkRegistrationTenant(ctx, pg, cfg); err != nil {
		t.Fatalf("a closed registration must not validate the stale Tenant: %v", err)
	}
}

func registrationAccountOrEmpty(t *testing.T, pg *storage.Postgres, username string) (string, string) {
	t.Helper()
	var id string
	var tenant *string
	err := pg.Pool.QueryRow(context.Background(),
		`SELECT id::text, tenant_id::text FROM gateway_admin_user WHERE lower(username)=lower($1)`, username).
		Scan(&id, &tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ""
	}
	if err != nil {
		t.Fatalf("read account %s: %v", username, err)
	}
	if tenant != nil {
		return id, *tenant
	}
	return id, ""
}
