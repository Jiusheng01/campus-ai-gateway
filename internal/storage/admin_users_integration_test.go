package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// These tests run against a real PostgreSQL because the properties they check
// are properties of the database, not of Go code:
//
//   - that pg_advisory_xact_lock really serialises mutations which touch two
//     different rows. An in-memory fake decides in Go, where the interleaving
//     being guarded against cannot even be expressed, so it can only ever
//     confirm that the fake agrees with itself.
//   - that a rejected change rolls back every field rather than the ones it
//     happened to write first.
//
// The test database is created from the real migrations and dropped on the way
// in, so it never touches the development database. Point TEST_DATABASE_URL at
// a different server to move it; the tests skip when nothing answers.

const defaultTestDatabaseURL = "postgres://campus:campus@127.0.0.1:5432/campus_gateway_test?sslmode=disable"

var (
	schemaOnce sync.Once
	schemaErr  error
)

func testDatabaseURL() string {
	if v := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")); v != "" {
		return v
	}
	return defaultTestDatabaseURL
}

// testPostgres returns a pool against a freshly migrated test database, or skips.
func testPostgres(t *testing.T) *Postgres {
	t.Helper()
	url := testDatabaseURL()
	schemaOnce.Do(func() { schemaErr = buildTestDatabase(url) })
	if schemaErr != nil {
		t.Skipf("test PostgreSQL unavailable: %v (set TEST_DATABASE_URL to run the integration tests)", schemaErr)
	}
	pg, err := OpenPostgres(context.Background(), url)
	if err != nil {
		t.Skipf("test PostgreSQL unavailable: %v", err)
	}
	t.Cleanup(pg.Close)
	return pg
}

// buildTestDatabase recreates the test database and applies the real migrations
// to it, so the schema under test is the one that ships.
func buildTestDatabase(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return fmt.Errorf("parse TEST_DATABASE_URL: %w", err)
	}
	dbName := cfg.Database
	if dbName == "" {
		return errors.New("TEST_DATABASE_URL has no database name")
	}

	adminCfg := cfg.Copy()
	adminCfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	// FORCE closes any connection a previous run left behind. Nothing outside
	// this test database is touched.
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+quoteIdent(dbName)+` WITH (FORCE)`); err != nil {
		return fmt.Errorf("drop test database: %w", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+quoteIdent(dbName)); err != nil {
		return fmt.Errorf("create test database: %w", err)
	}

	// Migrations are multi-statement files, which the extended protocol refuses,
	// so this connection speaks the simple protocol.
	migrateCfg := cfg.Copy()
	migrateCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db, err := pgx.ConnectConfig(ctx, migrateCfg)
	if err != nil {
		return fmt.Errorf("connect to test database: %w", err)
	}
	defer func() { _ = db.Close(ctx) }()

	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil {
		return fmt.Errorf("locate migrations: %w", err)
	}
	if len(files) == 0 {
		return errors.New("no migrations found; run the tests from the repository root")
	}
	sort.Strings(files)
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read %s: %w", file, err)
		}
		if _, err := db.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", filepath.Base(file), err)
		}
	}
	return nil
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// --- fixtures --------------------------------------------------------------

// insertAccount writes an account directly, bypassing the service layer, so a
// test can set up a state the API would refuse to create.
func insertAccount(t *testing.T, pg *Postgres, username string, roles []string, status int) model.AdminUser {
	t.Helper()
	ctx := context.Background()
	user, err := pg.CreateUser(ctx, username, username, "argon2id-placeholder", roles, false)
	if err != nil {
		t.Fatalf("insert %s: %v", username, err)
	}
	if status != model.AdminStatusEnabled {
		if _, _, err := pg.UpdateAdminUserAccount(ctx, user.ID, AdminUserMutation{Status: &status}); err != nil {
			t.Fatalf("set status for %s: %v", username, err)
		}
		user, err = pg.FindAdminUserByID(ctx, user.ID)
		if err != nil {
			t.Fatalf("reload %s: %v", username, err)
		}
	}
	return user
}

// clearAccounts empties the account table. The database is a scratch one built
// from migrations, never the development database.
func clearAccounts(t *testing.T, pg *Postgres) {
	t.Helper()
	if _, err := pg.Pool.Exec(context.Background(), `DELETE FROM gateway_admin_user`); err != nil {
		t.Fatalf("clear accounts: %v", err)
	}
}

func enabledSuperAdmins(t *testing.T, pg *Postgres) int64 {
	t.Helper()
	var n int64
	err := pg.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM gateway_admin_user WHERE status=$1 AND `+superAdminPredicate,
		model.AdminStatusEnabled).Scan(&n)
	if err != nil {
		t.Fatalf("count super admins: %v", err)
	}
	return n
}

func ptr[T any](v T) *T { return &v }

// --- account statistics -----------------------------------------------------

// P2-1 (review). The counts on the user management page must agree with the
// role semantics the gateway enforces. Phase 1 withdrew the global
// tenant_admin grant, so an account holding only that role is a member in
// every API decision — and must be counted as one, not advertised as an
// administrator on the console.
//
// This fails on the old SQL, whose predicate still listed tenant_admin among
// the management roles: the legacy account was counted as an administrator.
func TestAdminUserStatsCountsRolesTheWayTheGatewayDoes(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	insertAccount(t, pg, "plain-member", []string{"member"}, model.AdminStatusEnabled)
	insertAccount(t, pg, "legacy-tenant-admin", []string{"tenant_admin"}, model.AdminStatusEnabled)
	insertAccount(t, pg, "reader", []string{"auditor"}, model.AdminStatusEnabled)
	insertAccount(t, pg, "boss", []string{"super_admin"}, model.AdminStatusEnabled)
	insertAccount(t, pg, "off", []string{"operator"}, model.AdminStatusDisabled)

	// A tenant_admin bound to a scope is a real administrator: it reaches the
	// console for its subtree, so counting it as a member would understate the
	// accounts that can change configuration. The SQL decides this from the
	// same role list the gateway uses, with the scope as a second condition.
	tenant, err := pg.CreateTenant(ctx, model.Tenant{Code: "stats-scope", Name: "Stats Scope", Status: 1})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	scoped := insertAccount(t, pg, "scoped-tenant-admin", []string{"tenant_admin"}, model.AdminStatusEnabled)
	scopeID := tenant.ID
	if _, _, err := pg.UpdateAdminUserAccount(ctx, scoped.ID, AdminUserMutation{Scope: &ScopeUpdate{TenantID: &scopeID}}); err != nil {
		t.Fatalf("set scope: %v", err)
	}

	stats, err := pg.AdminUserStats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Total != 6 {
		t.Fatalf("total = %d, want 6", stats.Total)
	}
	// plain member plus the legacy tenant_admin, which now grants nothing.
	if stats.Members != 2 {
		t.Fatalf("members = %d, want 2: a legacy tenant_admin must count as a member", stats.Members)
	}
	// auditor, super_admin, the (disabled) operator and the scoped tenant_admin:
	// the count is about the roles held, not about the accounts being enabled.
	if stats.Admins != 4 {
		t.Fatalf("admins = %d, want 4: a scoped tenant_admin must count as an administrator", stats.Admins)
	}
	if stats.Disabled != 1 {
		t.Fatalf("disabled = %d, want 1", stats.Disabled)
	}
}

// --- concurrent super-administrator invariant ------------------------------

func TestConcurrentDemotionOfTwoSuperAdminsKeepsOne(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	first := insertAccount(t, pg, "super-one", []string{"super_admin"}, model.AdminStatusEnabled)
	second := insertAccount(t, pg, "super-two", []string{"super_admin"}, model.AdminStatusEnabled)

	member := []string{"member"}
	start := make(chan struct{})
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []error
	)
	for _, id := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			<-start
			_, _, err := pg.UpdateAdminUserAccount(ctx, target, AdminUserMutation{Roles: &member})
			mu.Lock()
			results = append(results, err)
			mu.Unlock()
		}(id)
	}
	close(start)
	wg.Wait()

	var succeeded, refused int
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrLastSuperAdmin):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("succeeded=%d refused=%d, want exactly one of each", succeeded, refused)
	}

	// The whole point: a row lock alone would let both through, because they
	// target different rows and each would still see the other as enabled.
	if got := enabledSuperAdmins(t, pg); got != 1 {
		t.Fatalf("enabled super admins = %d, want 1", got)
	}
}

// Two transactions touching different rows and different columns still contend
// for the same invariant, which is why the lock has to be global rather than
// per row.
func TestConcurrentCrossFieldMutationsKeepOneSuperAdmin(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	first := insertAccount(t, pg, "super-one", []string{"super_admin"}, model.AdminStatusEnabled)
	second := insertAccount(t, pg, "super-two", []string{"super_admin"}, model.AdminStatusEnabled)

	member := []string{"member"}
	disabled := model.AdminStatusDisabled

	start := make(chan struct{})
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, _, err := pg.UpdateAdminUserAccount(ctx, first.ID, AdminUserMutation{Roles: &member})
		mu.Lock()
		results = append(results, err)
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		<-start
		_, _, err := pg.UpdateAdminUserAccount(ctx, second.ID, AdminUserMutation{Status: &disabled})
		mu.Lock()
		results = append(results, err)
		mu.Unlock()
	}()
	close(start)
	wg.Wait()

	var succeeded, refused int
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrLastSuperAdmin):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("succeeded=%d refused=%d, want exactly one of each", succeeded, refused)
	}
	if got := enabledSuperAdmins(t, pg); got != 1 {
		t.Fatalf("enabled super admins = %d, want 1", got)
	}
}

func TestSerialHandoverKeepsTheLastSuperAdmin(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	first := insertAccount(t, pg, "super-one", []string{"super_admin"}, model.AdminStatusEnabled)
	second := insertAccount(t, pg, "super-two", []string{"super_admin"}, model.AdminStatusEnabled)

	gatewayAdmin := []string{"gateway_admin"}
	if _, _, err := pg.UpdateAdminUserAccount(ctx, first.ID, AdminUserMutation{Roles: &gatewayAdmin}); err != nil {
		t.Fatalf("first may step down while the second is enabled: %v", err)
	}

	member := []string{"member"}
	if _, _, err := pg.UpdateAdminUserAccount(ctx, second.ID, AdminUserMutation{Roles: &member}); !errors.Is(err, ErrLastSuperAdmin) {
		t.Fatalf("expected ErrLastSuperAdmin once only one remains, got %v", err)
	}
	if got := enabledSuperAdmins(t, pg); got != 1 {
		t.Fatalf("enabled super admins = %d, want 1", got)
	}
}

// --- atomicity of a multi-field patch --------------------------------------

func TestRejectedMultiFieldPatchLeavesEveryFieldUntouched(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	admin := insertAccount(t, pg, "campus-admin", []string{"super_admin"}, model.AdminStatusEnabled)
	before, err := pg.FindAdminUserByID(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The display name is a legitimate change; the role and status changes are
	// not, because they would remove the only administrator. The whole request
	// has to be refused, not just the part that failed the invariant.
	member := []string{"member"}
	disabled := model.AdminStatusDisabled
	_, _, err = pg.UpdateAdminUserAccount(ctx, admin.ID, AdminUserMutation{
		Roles:       &member,
		Status:      &disabled,
		DisplayName: ptr("Former Admin"),
	})
	if !errors.Is(err, ErrLastSuperAdmin) {
		t.Fatalf("expected ErrLastSuperAdmin, got %v", err)
	}

	after, err := pg.FindAdminUserByID(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.DisplayName != before.DisplayName {
		t.Fatalf("display name changed to %q despite the refusal; the patch must roll back whole", after.DisplayName)
	}
	if !containsRole(after.Roles, "super_admin") {
		t.Fatalf("roles = %v, want super_admin retained", after.Roles)
	}
	if after.Status != model.AdminStatusEnabled {
		t.Fatalf("status = %d, want enabled", after.Status)
	}
	if after.AuthVersion != before.AuthVersion {
		t.Fatalf("auth_version moved from %d to %d on a refused patch", before.AuthVersion, after.AuthVersion)
	}
}

func TestCombinedMutationBumpsAuthVersionExactlyOnce(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	// A second administrator so the invariant is not what stops this.
	insertAccount(t, pg, "second-admin", []string{"super_admin"}, model.AdminStatusEnabled)
	admin := insertAccount(t, pg, "campus-admin", []string{"super_admin"}, model.AdminStatusEnabled)

	before, err := pg.FindAdminUserByID(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	roles := []string{"gateway_admin"}
	disabled := model.AdminStatusDisabled
	updated, changes, err := pg.UpdateAdminUserAccount(ctx, admin.ID, AdminUserMutation{
		Roles:       &roles,
		Status:      &disabled,
		DisplayName: ptr("Former Admin"),
	})
	if err != nil {
		t.Fatalf("combined mutation: %v", err)
	}
	if updated.AuthVersion != before.AuthVersion+1 {
		t.Fatalf("auth_version %d -> %d, want exactly +1", before.AuthVersion, updated.AuthVersion)
	}
	if !changes.RolesChanged || !changes.StatusChanged || !changes.DisplayNameChanged {
		t.Fatalf("change set = %+v, want all three true", changes)
	}
	if !changes.AuthenticationChanged() {
		t.Fatal("a role and status change must report as an authentication change")
	}
}

// --- change set semantics --------------------------------------------------

func TestNoopMutationWritesNothing(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	admin := insertAccount(t, pg, "campus-admin", []string{"super_admin"}, model.AdminStatusEnabled)
	before, err := pg.FindAdminUserByID(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Exactly the values already stored.
	same := []string{"super_admin"}
	enabled := model.AdminStatusEnabled
	updated, changes, err := pg.UpdateAdminUserAccount(ctx, admin.ID, AdminUserMutation{
		Roles:       &same,
		Status:      &enabled,
		DisplayName: ptr(before.DisplayName),
	})
	if err != nil {
		t.Fatalf("no-op mutation: %v", err)
	}
	if changes.AnythingChanged() {
		t.Fatalf("change set = %+v, want nothing reported as changed", changes)
	}
	if updated.AuthVersion != before.AuthVersion {
		t.Fatalf("auth_version moved on a no-op: %d -> %d", before.AuthVersion, updated.AuthVersion)
	}
	if !updated.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("updated_at moved on a no-op: %v -> %v", before.UpdatedAt, updated.UpdatedAt)
	}
}

func TestRoleReorderIsNotAChange(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	admin := insertAccount(t, pg, "campus-admin", []string{"gateway_admin", "auditor"}, model.AdminStatusEnabled)
	before, err := pg.FindAdminUserByID(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The same two roles, in the other order. The stored order is not part of
	// the meaning, so this must not look like a role change — otherwise saving
	// an unrelated field would sign the account out.
	reordered := []string{"auditor", "gateway_admin"}
	updated, changes, err := pg.UpdateAdminUserAccount(ctx, admin.ID, AdminUserMutation{Roles: &reordered})
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if changes.RolesChanged {
		t.Fatal("a reordered role list must not report a change")
	}
	if updated.AuthVersion != before.AuthVersion {
		t.Fatalf("auth_version moved on a reorder: %d -> %d", before.AuthVersion, updated.AuthVersion)
	}
}

func TestDisplayNameOnlyLeavesAuthVersionAlone(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	admin := insertAccount(t, pg, "campus-admin", []string{"super_admin"}, model.AdminStatusEnabled)
	before, err := pg.FindAdminUserByID(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	updated, changes, err := pg.UpdateAdminUserAccount(ctx, admin.ID, AdminUserMutation{DisplayName: ptr("张三老师")})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if updated.DisplayName != "张三老师" {
		t.Fatalf("display name = %q", updated.DisplayName)
	}
	if updated.AuthVersion != before.AuthVersion {
		t.Fatalf("a label change must not move auth_version: %d -> %d", before.AuthVersion, updated.AuthVersion)
	}
	if changes.AuthenticationChanged() {
		t.Fatal("a label change is not an authentication change")
	}
	if !changes.DisplayNameChanged {
		t.Fatal("the display name change must be reported")
	}
}

func TestTenantExistsTreatsAMalformedIDAsMissing(t *testing.T) {
	pg := testPostgres(t)

	exists, err := pg.TenantExists(context.Background(), "not-a-uuid")
	if err != nil {
		t.Fatalf("TenantExists on a malformed id returned an error: %v", err)
	}
	if exists {
		t.Fatal("TenantExists on a malformed id reported a Tenant")
	}
}

func TestMutationOfUnknownAccountIsNotFound(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)

	_, _, err := pg.UpdateAdminUserAccount(context.Background(),
		"00000000-0000-0000-0000-000000000000", AdminUserMutation{DisplayName: ptr("x")})
	if !errors.Is(err, ErrAdminUserNotFound) {
		t.Fatalf("expected ErrAdminUserNotFound, got %v", err)
	}
}

// --- the tenant_admin grant is a pair ---------------------------------------

// The role grants reach over a subtree, so it is meaningless — and, if stored,
// dangerous — without knowing which subtree. The database refuses the half
// grants rather than trusting every future caller to remember the pairing, and
// refuses them in the same transaction that would have written them.
func TestTenantAdminGrantRequiresRoleAndScopeTogether(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	tenant, err := pg.CreateTenant(ctx, model.Tenant{Code: "grant-scope", Name: "Grant Scope", Status: 1})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	tenantAdmin := []string{"tenant_admin"}
	member := []string{"member"}

	t.Run("role without scope is refused", func(t *testing.T) {
		account := insertAccount(t, pg, "role-only", member, model.AdminStatusEnabled)
		_, _, err := pg.UpdateAdminUserAccount(ctx, account.ID, AdminUserMutation{Roles: &tenantAdmin})
		if !errors.Is(err, ErrAdminScopeRequired) {
			t.Fatalf("expected ErrAdminScopeRequired, got %v", err)
		}
		reloaded, err := pg.FindAdminUserByID(ctx, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		if containsRole(reloaded.Roles, roleTenantAdmin) {
			t.Fatal("a refused grant must not leave the role behind")
		}
		if reloaded.ScopeTenantID != nil {
			t.Fatal("a refused grant must not leave a scope behind")
		}
	})

	t.Run("scope without role is refused", func(t *testing.T) {
		account := insertAccount(t, pg, "scope-only", member, model.AdminStatusEnabled)
		scopeID := tenant.ID
		_, _, err := pg.UpdateAdminUserAccount(ctx, account.ID, AdminUserMutation{Scope: &ScopeUpdate{TenantID: &scopeID}})
		if !errors.Is(err, ErrAdminScopeUnexpected) {
			t.Fatalf("expected ErrAdminScopeUnexpected, got %v", err)
		}
	})

	t.Run("both together are stored and named", func(t *testing.T) {
		account := insertAccount(t, pg, "paired", member, model.AdminStatusEnabled)
		scopeID := tenant.ID
		updated, changes, err := pg.UpdateAdminUserAccount(ctx, account.ID, AdminUserMutation{
			Roles: &tenantAdmin,
			Scope: &ScopeUpdate{TenantID: &scopeID},
		})
		if err != nil {
			t.Fatalf("grant: %v", err)
		}
		if !changes.ScopeChanged || updated.ScopeTenantID == nil || *updated.ScopeTenantID != tenant.ID {
			t.Fatalf("scope not stored: %+v", updated.ScopeTenantID)
		}
		if updated.ScopeTenantName != tenant.Name {
			t.Fatalf("scope name = %q, want %q", updated.ScopeTenantName, tenant.Name)
		}
	})

	t.Run("clearing the scope while keeping the role is refused", func(t *testing.T) {
		account := insertAccount(t, pg, "clear-scope", member, model.AdminStatusEnabled)
		scopeID := tenant.ID
		if _, _, err := pg.UpdateAdminUserAccount(ctx, account.ID, AdminUserMutation{
			Roles: &tenantAdmin,
			Scope: &ScopeUpdate{TenantID: &scopeID},
		}); err != nil {
			t.Fatalf("grant: %v", err)
		}
		_, _, err := pg.UpdateAdminUserAccount(ctx, account.ID, AdminUserMutation{
			Scope: &ScopeUpdate{TenantID: nil},
		})
		if !errors.Is(err, ErrAdminScopeRequired) {
			t.Fatalf("expected ErrAdminScopeRequired, got %v", err)
		}
	})

	t.Run("unknown and malformed scope tenants are not found", func(t *testing.T) {
		account := insertAccount(t, pg, "bad-scope", member, model.AdminStatusEnabled)
		for _, id := range []string{"00000000-0000-0000-0000-000000000000", "not-a-uuid"} {
			bad := id
			_, _, err := pg.UpdateAdminUserAccount(ctx, account.ID, AdminUserMutation{
				Roles: &tenantAdmin,
				Scope: &ScopeUpdate{TenantID: &bad},
			})
			if !errors.Is(err, ErrAdminTenantNotFound) {
				t.Fatalf("scope %q: expected ErrAdminTenantNotFound, got %v", id, err)
			}
		}
	})
}

// A scope narrows where an account may act. Dropping it widens the account
// back to global reach, so it can only be done together with losing the role.
func TestRemovingTenantAdminRoleAlongWithScopeSucceeds(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	tenant, err := pg.CreateTenant(ctx, model.Tenant{Code: "revoke-scope", Name: "Revoke Scope", Status: 1})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	tenantAdmin := []string{"tenant_admin"}
	member := []string{"member"}
	account := insertAccount(t, pg, "downgraded", member, model.AdminStatusEnabled)
	scopeID := tenant.ID
	if _, _, err := pg.UpdateAdminUserAccount(ctx, account.ID, AdminUserMutation{
		Roles: &tenantAdmin,
		Scope: &ScopeUpdate{TenantID: &scopeID},
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	updated, changes, err := pg.UpdateAdminUserAccount(ctx, account.ID, AdminUserMutation{
		Roles: &member,
		Scope: &ScopeUpdate{TenantID: nil},
	})
	if err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	if !changes.ScopeChanged || updated.ScopeTenantID != nil || updated.ScopeTenantName != "" {
		t.Fatalf("scope not cleared: %+v %q", updated.ScopeTenantID, updated.ScopeTenantName)
	}
	if containsRole(updated.Roles, roleTenantAdmin) {
		t.Fatal("role still present after downgrade")
	}
}

// --- transactional account + membership -------------------------------------

func createTenantForRegistration(t *testing.T, pg *Postgres, status int) model.Tenant {
	t.Helper()
	tenant, err := pg.CreateTenant(context.Background(), model.Tenant{
		Code:   "reg-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8],
		Name:   "Registration Tenant",
		Status: status,
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	return tenant
}

func countAccountsWithName(t *testing.T, pg *Postgres, username string) int64 {
	t.Helper()
	var n int64
	if err := pg.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM gateway_admin_user WHERE lower(username)=lower($1)`, username).Scan(&n); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	return n
}

func registrationInput(username string, requireEnabled bool, tenantID string) AdminUserCreate {
	return AdminUserCreate{
		Username:              username,
		DisplayName:           username,
		PasswordHash:          "argon2id-placeholder",
		Roles:                 []string{"member"},
		PasswordResetRequired: false,
		TenantID:              tenantID,
		RequireEnabledTenant:  requireEnabled,
	}
}

func TestCreateUserWithTenantCommitsBothRows(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	tenant := createTenantForRegistration(t, pg, 1)
	username := "together-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]

	created, err := pg.CreateUserWithTenant(ctx, registrationInput(username, true, tenant.ID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.TenantID != tenant.ID || created.TenantName != tenant.Name {
		t.Fatalf("returned membership = %q/%q, want %q/%q", created.TenantID, created.TenantName, tenant.ID, tenant.Name)
	}
	if created.AuthVersion != 1 {
		t.Fatalf("auth_version = %d, want 1", created.AuthVersion)
	}

	reloaded, err := pg.FindAdminUserByUsername(ctx, username)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.TenantID != tenant.ID || reloaded.TenantName != tenant.Name {
		t.Fatalf("stored membership = %q/%q", reloaded.TenantID, reloaded.TenantName)
	}
}

func TestCreateUserWithTenantWritesNothingWhenTenantIsMissing(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	username := "missing-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := pg.CreateUserWithTenant(ctx, registrationInput(username, true, uuid.NewString())); !errors.Is(err, ErrAdminTenantNotFound) {
		t.Fatalf("error = %v, want ErrAdminTenantNotFound", err)
	}
	if got := countAccountsWithName(t, pg, username); got != 0 {
		t.Fatalf("a failed create left %d accounts", got)
	}
}

func TestCreateUserWithTenantRefusesDisabledTenantForRegistration(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	tenant := createTenantForRegistration(t, pg, 2)
	username := "disabled-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := pg.CreateUserWithTenant(ctx, registrationInput(username, true, tenant.ID)); !errors.Is(err, ErrAdminTenantDisabled) {
		t.Fatalf("error = %v, want ErrAdminTenantDisabled", err)
	}
	if got := countAccountsWithName(t, pg, username); got != 0 {
		t.Fatalf("a refused create left %d accounts", got)
	}

	// The administrator path may deliberately stage an account under a Tenant
	// that is not switched on yet.
	adminName := "staged-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	created, err := pg.CreateUserWithTenant(ctx, registrationInput(adminName, false, tenant.ID))
	if err != nil {
		t.Fatalf("admin create against a disabled Tenant: %v", err)
	}
	if created.TenantID != tenant.ID {
		t.Fatalf("staged membership = %q, want %q", created.TenantID, tenant.ID)
	}
}

// A duplicate username is reported as a conflict and leaves the original row —
// including its membership — exactly as it was.
func TestCreateUserWithTenantLeavesTheExistingAccountOnADuplicate(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	first := createTenantForRegistration(t, pg, 1)
	second := createTenantForRegistration(t, pg, 1)
	username := "duplicate-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]

	original, err := pg.CreateUserWithTenant(ctx, registrationInput(username, true, first.ID))
	if err != nil {
		t.Fatalf("create original: %v", err)
	}
	if _, err := pg.CreateUserWithTenant(ctx, registrationInput(strings.ToUpper(username), true, second.ID)); !errors.Is(err, ErrAdminUsernameTaken) {
		t.Fatalf("error = %v, want ErrAdminUsernameTaken", err)
	}

	reloaded, err := pg.FindAdminUserByID(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.TenantID != first.ID {
		t.Fatalf("the duplicate changed the original membership: %q", reloaded.TenantID)
	}
	if got := countAccountsWithName(t, pg, username); got != 1 {
		t.Fatalf("duplicate registration produced %d accounts", got)
	}
}

// Concurrent signups with the same username must produce exactly one account:
// the unique index is the arbiter, and the losing transaction writes neither
// the key nor the membership.
func TestConcurrentCreateUserWithTenantProducesOneAccount(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	tenant := createTenantForRegistration(t, pg, 1)
	username := "concurrent-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]

	const attempts = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var succeeded int
	var conflicts int
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := pg.CreateUserWithTenant(ctx, registrationInput(username, true, tenant.ID))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrAdminUsernameTaken):
				conflicts++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if succeeded != 1 || conflicts != attempts-1 {
		t.Fatalf("succeeded=%d conflicts=%d, want 1 and %d", succeeded, conflicts, attempts-1)
	}
	if got := countAccountsWithName(t, pg, username); got != 1 {
		t.Fatalf("concurrent registrations left %d accounts", got)
	}
}

// A Tenant deleted while the registration transaction is running must make the
// registration fail, not create an account whose membership was written from a
// stale read. The DELETE holds the row lock; the insert waits for it and then
// sees no Tenant.
func TestCreateUserWithTenantLosesCleanlyToAConcurrentTenantDelete(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	tenant := createTenantForRegistration(t, pg, 1)
	username := "deleted-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]

	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM gateway_tenant WHERE id=$1::uuid`, tenant.ID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("delete tenant: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := pg.CreateUserWithTenant(ctx, registrationInput(username, true, tenant.ID))
		done <- err
	}()

	// Give the registration time to reach the locked row, then let the DELETE
	// commit. The registration must observe "no Tenant" rather than proceed.
	time.Sleep(150 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit delete: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrAdminTenantNotFound) {
			t.Fatalf("error = %v, want ErrAdminTenantNotFound", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("registration did not finish after the Tenant delete committed")
	}
	if got := countAccountsWithName(t, pg, username); got != 0 {
		t.Fatalf("a lost race left %d accounts", got)
	}
}

// A failure inside the INSERT itself must roll the whole transaction back. The
// trigger makes the database, not the Go code, the source of the failure, which
// is what proves the rollback rather than merely the absence of a Go-side
// write.
func TestCreateUserWithTenantRollsBackOnInsertFailure(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)
	ctx := context.Background()

	tenant := createTenantForRegistration(t, pg, 1)
	username := "trigger-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]

	if _, err := pg.Pool.Exec(ctx, `
CREATE OR REPLACE FUNCTION reject_registration_insert() RETURNS trigger AS $$
BEGIN
  IF NEW.username = '`+username+`' THEN
    RAISE EXCEPTION 'injected insert failure';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx, `
CREATE TRIGGER reject_registration_insert_trigger
BEFORE INSERT ON gateway_admin_user
FOR EACH ROW EXECUTE FUNCTION reject_registration_insert()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pg.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS reject_registration_insert_trigger ON gateway_admin_user`)
		_, _ = pg.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS reject_registration_insert()`)
	})

	if _, err := pg.CreateUserWithTenant(ctx, registrationInput(username, true, tenant.ID)); err == nil {
		t.Fatal("the injected insert failure must be reported")
	}
	if got := countAccountsWithName(t, pg, username); got != 0 {
		t.Fatalf("a rolled-back insert left %d accounts", got)
	}
}
