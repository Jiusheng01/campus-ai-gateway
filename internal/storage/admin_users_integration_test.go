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
	user, err := pg.CreateUser(ctx, username, username, "argon2id-placeholder", roles)
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

func TestMutationOfUnknownAccountIsNotFound(t *testing.T) {
	pg := testPostgres(t)
	clearAccounts(t, pg)

	_, _, err := pg.UpdateAdminUserAccount(context.Background(),
		"00000000-0000-0000-0000-000000000000", AdminUserMutation{DisplayName: ptr("x")})
	if !errors.Is(err, ErrAdminUserNotFound) {
		t.Fatalf("expected ErrAdminUserNotFound, got %v", err)
	}
}
