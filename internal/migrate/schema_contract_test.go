package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// mustFailClosed seeds a drifted legacy schema, runs the migration, and asserts
// the run refuses to record anything. It is the core assertion of P1-01: a
// database that is not at the final contract must fail closed with zero rows in
// schema_migrations, so a future run retries rather than trusting a
// half-verified history.
func mustFailClosed(t *testing.T, seed func(t *testing.T, conn *pgx.Conn)) {
	t.Helper()
	url := isolatedDatabase(t)
	conn := connect(t, url)
	seed(t, conn)
	_ = conn.Close(context.Background())

	if _, err := Apply(context.Background(), url, repoMigrations); err == nil {
		t.Fatal("a drifted legacy database must fail closed, not record a history it does not have")
	}

	conn = connect(t, url)
	defer conn.Close(context.Background())
	var count int
	if err := conn.QueryRow(context.Background(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("a failed bootstrap must record nothing, got %d rows", count)
	}
}

// P1-01 C: a legacy gateway_tenant missing the code column must fail.
func TestApplyFailsClosedOnMissingTenantCode(t *testing.T) {
	mustFailClosed(t, func(t *testing.T, conn *pgx.Conn) {
		if _, err := conn.Exec(context.Background(),
			`CREATE TABLE gateway_tenant (id UUID PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
	})
}

// P1-01 D: the exact Codex reproduction — a legacy gateway_api_key that only
// has (id, tenant_id). The old "table exists" check called this current; the
// contract must reject it because key_digest and every other runtime column
// are missing.
func TestApplyFailsClosedOnPartialGatewayAPIKey(t *testing.T) {
	mustFailClosed(t, func(t *testing.T, conn *pgx.Conn) {
		if _, err := conn.Exec(context.Background(),
			`CREATE TABLE gateway_api_key (id UUID PRIMARY KEY, tenant_id UUID)`); err != nil {
			t.Fatal(err)
		}
	})
}

// P1-01 E: every core business table is drifted at least once by seeding it as
// a primary-key-only stub and confirming the bootstrap still fails closed. The
// replay cannot repair a stub — CREATE ... IF NOT EXISTS no-ops — so the missing
// runtime columns surface in the contract check.
func TestApplyFailsClosedOnStubbedCoreTable(t *testing.T) {
	stubs := map[string]string{
		"gateway_tenant":          `CREATE TABLE gateway_tenant (id UUID PRIMARY KEY)`,
		"gateway_api_key":         `CREATE TABLE gateway_api_key (id UUID PRIMARY KEY, tenant_id UUID)`,
		"gateway_request_log":     `CREATE TABLE gateway_request_log (id BIGSERIAL PRIMARY KEY)`,
		"gateway_provider":        `CREATE TABLE gateway_provider (id UUID PRIMARY KEY)`,
		"gateway_model":           `CREATE TABLE gateway_model (id UUID PRIMARY KEY)`,
		"gateway_model_route":     `CREATE TABLE gateway_model_route (id UUID PRIMARY KEY)`,
		"gateway_admin_audit_log": `CREATE TABLE gateway_admin_audit_log (id BIGSERIAL PRIMARY KEY)`,
		"gateway_security_event":  `CREATE TABLE gateway_security_event (id BIGSERIAL PRIMARY KEY)`,
		"gateway_admin_user":      `CREATE TABLE gateway_admin_user (id UUID PRIMARY KEY)`,
		"gateway_admin_session":   `CREATE TABLE gateway_admin_session (sid UUID PRIMARY KEY)`,
	}
	for name, ddl := range stubs {
		t.Run(name, func(t *testing.T) {
			mustFailClosed(t, func(t *testing.T, conn *pgx.Conn) {
				if _, err := conn.Exec(context.Background(), ddl); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

// P1-01 §11: a column that exists but with the wrong data type must fail. Here
// gateway_tenant.status is TEXT where the contract requires smallint.
func TestApplyFailsClosedOnWrongColumnType(t *testing.T) {
	mustFailClosed(t, func(t *testing.T, conn *pgx.Conn) {
		if _, err := conn.Exec(context.Background(), `
			CREATE TABLE gateway_tenant (
				id UUID PRIMARY KEY,
				code VARCHAR(64) NOT NULL,
				name VARCHAR(128) NOT NULL,
				status TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				daily_token_limit BIGINT NOT NULL DEFAULT 0,
				monthly_token_limit BIGINT NOT NULL DEFAULT 0,
				monthly_request_limit BIGINT NOT NULL DEFAULT 0,
				monthly_budget_micros BIGINT NOT NULL DEFAULT 0
			)`); err != nil {
			t.Fatal(err)
		}
	})
}

// P1-01 §12: a column that exists with the right type but the wrong
// nullability must fail. Here gateway_tenant.code is nullable where the
// contract requires NOT NULL.
func TestApplyFailsClosedOnWrongNullability(t *testing.T) {
	mustFailClosed(t, func(t *testing.T, conn *pgx.Conn) {
		if _, err := conn.Exec(context.Background(), `
			CREATE TABLE gateway_tenant (
				id UUID PRIMARY KEY,
				code VARCHAR(64),
				name VARCHAR(128) NOT NULL,
				status SMALLINT NOT NULL DEFAULT 1,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				daily_token_limit BIGINT NOT NULL DEFAULT 0,
				monthly_token_limit BIGINT NOT NULL DEFAULT 0,
				monthly_request_limit BIGINT NOT NULL DEFAULT 0,
				monthly_budget_micros BIGINT NOT NULL DEFAULT 0
			)`); err != nil {
			t.Fatal(err)
		}
	})
}

// P1-01 §13: a missing UNIQUE (here the case-insensitive username uniqueness,
// or the tenant code uniqueness) must fail. This seeds gateway_tenant without
// the UNIQUE on code.
func TestApplyFailsClosedOnMissingUnique(t *testing.T) {
	mustFailClosed(t, func(t *testing.T, conn *pgx.Conn) {
		if _, err := conn.Exec(context.Background(), `
			CREATE TABLE gateway_tenant (
				id UUID PRIMARY KEY,
				code VARCHAR(64) NOT NULL,
				name VARCHAR(128) NOT NULL,
				status SMALLINT NOT NULL DEFAULT 1,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				daily_token_limit BIGINT NOT NULL DEFAULT 0,
				monthly_token_limit BIGINT NOT NULL DEFAULT 0,
				monthly_request_limit BIGINT NOT NULL DEFAULT 0,
				monthly_budget_micros BIGINT NOT NULL DEFAULT 0
			)`); err != nil {
			t.Fatal(err)
		}
	})
}

// P1-01 B: a database carrying the complete final schema but no
// schema_migrations bookkeeping is brought under bookkeeping and recorded.
func TestApplyBootstrapsFullLegacySchema(t *testing.T) {
	url := isolatedDatabase(t)
	applyFilesRaw(t, url, repoMigrations, len(listMigrationFiles(t, repoMigrations)))

	result, err := Apply(context.Background(), url, repoMigrations)
	if err != nil {
		t.Fatalf("bootstrap apply: %v", err)
	}
	if !result.Bootstrapped {
		t.Fatal("a database without bookkeeping must report a bootstrap")
	}
	if result.Recorded != len(listMigrationFiles(t, repoMigrations)) {
		t.Fatalf("recorded %d, want %d", result.Recorded, len(listMigrationFiles(t, repoMigrations)))
	}
	conn := connect(t, url)
	defer conn.Close(context.Background())
	for _, name := range []string{
		"gateway_tenant", "gateway_api_key", "gateway_request_log",
		"gateway_provider", "gateway_model", "gateway_model_route",
		"gateway_admin_audit_log", "gateway_security_event",
		"gateway_admin_user", "gateway_admin_session",
	} {
		var exists bool
		if err := conn.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`, name).
			Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected %s to exist after bootstrap", name)
		}
	}
}

// P1-01 §13: dropping a runtime index after a successful apply must be caught
// by the contract check.
func TestFinalSchemaDetectsDroppedIndex(t *testing.T) {
	url := isolatedDatabase(t)
	if _, err := Apply(context.Background(), url, repoMigrations); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	conn := connect(t, url)
	defer conn.Close(context.Background())

	if _, err := conn.Exec(context.Background(), `DROP INDEX IF EXISTS idx_gateway_api_key_tenant`); err != nil {
		t.Fatal(err)
	}
	if err := CheckFinalSchema(context.Background(), conn); err == nil {
		t.Fatal("dropping idx_gateway_api_key_tenant must fail the contract check")
	}

	if _, err := conn.Exec(context.Background(), `DROP INDEX IF EXISTS uq_gateway_model_route_binding`); err != nil {
		t.Fatal(err)
	}
	if err := CheckFinalSchema(context.Background(), conn); err == nil {
		t.Fatal("dropping the composite unique index must fail the contract check")
	}

	if _, err := conn.Exec(context.Background(), `DROP INDEX IF EXISTS idx_gateway_admin_user_username_lower`); err != nil {
		t.Fatal(err)
	}
	if err := CheckFinalSchema(context.Background(), conn); err == nil {
		t.Fatal("dropping the lower(username) unique index must fail the contract check")
	}
}

// P1-01 §14: dropping a foreign key after a successful apply must be caught.
func TestFinalSchemaDetectsDroppedForeignKey(t *testing.T) {
	url := isolatedDatabase(t)
	if _, err := Apply(context.Background(), url, repoMigrations); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	conn := connect(t, url)
	defer conn.Close(context.Background())

	var cname string
	if err := conn.QueryRow(context.Background(),
		`SELECT conname FROM pg_constraint WHERE conrelid='gateway_api_key'::regclass AND contype='f'`).
		Scan(&cname); err != nil {
		t.Fatalf("find fk: %v", err)
	}
	if _, err := conn.Exec(context.Background(),
		`ALTER TABLE gateway_api_key DROP CONSTRAINT `+cname); err != nil {
		t.Fatal(err)
	}
	if err := CheckFinalSchema(context.Background(), conn); err == nil {
		t.Fatal("dropping the gateway_api_key foreign key must fail the contract check")
	}
}

// P1-01 §9: the failure message must be diagnostic — it names the object and
// what was expected versus what was found — so an operator can locate the gap.
func TestFinalSchemaFailureIsDiagnostic(t *testing.T) {
	url := isolatedDatabase(t)
	conn := connect(t, url)
	if _, err := conn.Exec(context.Background(),
		`CREATE TABLE gateway_api_key (id UUID PRIMARY KEY, tenant_id UUID)`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(context.Background())

	_, err := Apply(context.Background(), url, repoMigrations)
	if err == nil {
		t.Fatal("expected failure")
	}
	for _, want := range []string{"gateway_api_key", "key_digest", "expected", "actual"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("failure must be diagnostic; missing %q in: %v", want, err)
		}
	}
}
