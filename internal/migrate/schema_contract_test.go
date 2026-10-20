package migrate

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// migratedDatabase returns a database with the repository migrations applied.
// Tests can then remove bookkeeping and introduce one precise drift, which
// models a legacy database whose business schema exists but predates this
// runner.
func migratedDatabase(t *testing.T) (string, *pgx.Conn) {
	t.Helper()
	url := isolatedDatabase(t)
	if _, err := Apply(context.Background(), url, repoMigrations); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	conn := connect(t, url)
	if _, err := conn.Exec(context.Background(), `DROP TABLE schema_migrations`); err != nil {
		t.Fatalf("drop bookkeeping: %v", err)
	}
	return url, conn
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

// VARCHAR length is part of the type contract. Treating VARCHAR(1) as
// VARCHAR(64) lets the migration gate pass a database that rejects valid tenant
// codes at runtime.
func TestApplyFailsClosedOnWrongVarcharLength(t *testing.T) {
	url, conn := migratedDatabase(t)
	if _, err := conn.Exec(context.Background(),
		`ALTER TABLE gateway_tenant ALTER COLUMN code TYPE VARCHAR(1)`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(context.Background())

	if _, err := Apply(context.Background(), url, repoMigrations); err == nil || !strings.Contains(err.Error(), "length 64") {
		t.Fatalf("VARCHAR(1) must be rejected with the expected length, got %v", err)
	}
}

// Defaults that application INSERTs omit are runtime semantics, not cosmetic
// catalog details. A status/auth_version default of zero creates an unusable
// administrator even though every column and constraint is present.
func TestApplyFailsClosedOnWrongDefault(t *testing.T) {
	url, conn := migratedDatabase(t)
	if _, err := conn.Exec(context.Background(), `
		ALTER TABLE gateway_admin_user ALTER COLUMN status SET DEFAULT 0;
		ALTER TABLE gateway_admin_user ALTER COLUMN auth_version SET DEFAULT 0`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(context.Background())

	if _, err := Apply(context.Background(), url, repoMigrations); err == nil || !strings.Contains(err.Error(), "default 1") {
		t.Fatalf("wrong administrator defaults must be rejected, got %v", err)
	}
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

// A UNIQUE constraint is not a primary key. The old contract grouped contype
// p and u together, so replacing a primary key with UNIQUE(id) passed even
// though foreign-key and identity semantics had changed.
func TestFinalSchemaRejectsUniqueSubstitutedForPrimaryKey(t *testing.T) {
	url, conn := migratedDatabase(t)
	if _, err := conn.Exec(context.Background(), `
		ALTER TABLE gateway_admin_audit_log DROP CONSTRAINT gateway_admin_audit_log_pkey;
		ALTER TABLE gateway_admin_audit_log ADD CONSTRAINT round3_id_unique UNIQUE (id)`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(context.Background())

	if _, err := Apply(context.Background(), url, repoMigrations); err == nil || !strings.Contains(err.Error(), "primary key") {
		t.Fatalf("UNIQUE(id) must not satisfy the primary-key contract, got %v", err)
	}
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

// A database already at the end state can re-enter bookkeeping even though a
// historical file no longer replays cleanly.
//
// 0014 retires the columns 0002 indexes, so replaying the whole set against a
// current database stops at that index with "column does not exist". The
// bootstrap must not treat that as fatal: the contract is the gate, and losing
// schema_migrations on a current database would otherwise be unrecoverable.
func TestApplyBootstrapsCurrentSchemaDespiteRetiredColumnReference(t *testing.T) {
	url := isolatedDatabase(t)
	applyFilesRaw(t, url, repoMigrations, len(listMigrationFiles(t, repoMigrations)))

	// The premise: the set can no longer be replayed verbatim. If this ever
	// succeeds the tolerance below is untested and the comments that explain it
	// are stale.
	conn := connect(t, url)
	_, replayErr := conn.Exec(context.Background(),
		`CREATE INDEX IF NOT EXISTS idx_gateway_model_provider ON gateway_model(provider_id)`)
	_ = conn.Close(context.Background())
	if replayErr == nil {
		t.Fatal("premise: replaying 0002's model index against the current schema must fail")
	}

	result, err := Apply(context.Background(), url, repoMigrations)
	if err != nil {
		t.Fatalf("a database already at the end state must still be bootstrappable: %v", err)
	}
	if !result.Bootstrapped {
		t.Fatal("a database without bookkeeping must report a bootstrap")
	}
	if result.Recorded != len(listMigrationFiles(t, repoMigrations)) {
		t.Fatalf("recorded %d, want %d", result.Recorded, len(listMigrationFiles(t, repoMigrations)))
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

// Index names alone are not a contract. This is the exact decoy shape used in
// the independent review: one named index is UNIQUE(username), while another
// unrelated functional index happens to mention lower() and username. Together
// they fooled the old two-query validator while allowing Admin/admin to coexist.
func TestFinalSchemaRejectsWrongUsernameIndexDefinition(t *testing.T) {
	url, conn := migratedDatabase(t)
	if _, err := conn.Exec(context.Background(), `
		DROP INDEX idx_gateway_admin_user_username_lower;
		CREATE UNIQUE INDEX idx_gateway_admin_user_username_lower ON gateway_admin_user(username);
		CREATE UNIQUE INDEX idx_round3_decoy ON gateway_admin_user(lower(display_name)) INCLUDE (username)`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(context.Background())

	if _, err := Apply(context.Background(), url, repoMigrations); err == nil || !strings.Contains(err.Error(), "lower(username::text)") {
		t.Fatalf("decoy indexes must not satisfy lower(username), got %v", err)
	}
}

// indexStatementPattern matches every index DDL statement the migration files
// use: a CREATE (optionally UNIQUE) INDEX, or a DROP INDEX. The name is the only
// thing read here; the definition is checked against the live catalog below,
// which is where a wrong expression would show up.
//
// CREATE and DROP share one pattern so the two can be applied in the order they
// appear in the file. A name a later migration re-creates is live again, and a
// set-subtraction that ignored order would get that wrong.
var indexStatementPattern = regexp.MustCompile(
	`(?i)\b(CREATE\s+(?:UNIQUE\s+)?INDEX|DROP\s+INDEX)\s+(?:IF\s+NOT\s+EXISTS\s+|IF\s+EXISTS\s+)?([a-z0-9_]+)`)

// declaredIndexNames returns every index the migration set leaves behind, in
// filename order: the names live after every file has been read, where a DROP
// retires a name and a later CREATE brings it back. Retired indexes are
// subtracted, so an index the contract no longer expects does not have to stay
// in the contract just because a historical file created it.
func declaredIndexNames(t *testing.T) []string {
	t.Helper()
	files := listMigrationFiles(t, repoMigrations)
	sort.Strings(files)
	var order []string
	live := map[string]bool{}
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(repoMigrations, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range indexStatementPattern.FindAllStringSubmatch(string(body), -1) {
			verb, idx := strings.ToUpper(strings.Fields(m[1])[0]), m[2]
			if verb == "DROP" {
				if live[idx] {
					delete(live, idx)
					kept := order[:0]
					for _, s := range order {
						if s != idx {
							kept = append(kept, s)
						}
					}
					order = kept
				}
				continue
			}
			if !live[idx] {
				live[idx] = true
				order = append(order, idx)
			}
		}
	}
	return order
}

// TestFinalSchemaDetectsEveryDeclaredRuntimeIndex keeps the contract and the
// migrations in step in both directions.
//
// It compares the two *sets* rather than counting entries against a constant.
// A hand-maintained number cannot tell a missing contract entry from a
// miscount: it fails the same way whichever side is wrong, and the person
// reading the failure has to work out which. Naming the discrepancy does that
// work for them. It also means this test never has to be edited when a
// migration is added — which is the moment it most needs to still be correct.
func TestFinalSchemaDetectsEveryDeclaredRuntimeIndex(t *testing.T) {
	url := isolatedDatabase(t)
	if _, err := Apply(context.Background(), url, repoMigrations); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	conn := connect(t, url)
	defer conn.Close(context.Background())

	_, _, _, indexes, _ := finalSchemaContract()
	inContract := make(map[string]bool, len(indexes))
	for _, idx := range indexes {
		inContract[idx.Name] = true
	}
	declared := declaredIndexNames(t)
	isDeclared := make(map[string]bool, len(declared))
	for _, name := range declared {
		isDeclared[name] = true
		if !inContract[name] {
			t.Errorf("migration declares index %s but the contract does not cover it", name)
		}
	}
	for _, idx := range indexes {
		if !isDeclared[idx.Name] {
			t.Errorf("the contract covers index %s that no migration declares", idx.Name)
		}
	}

	for _, idx := range indexes {
		t.Run(idx.Name, func(t *testing.T) {
			if _, err := conn.Exec(context.Background(), `DROP INDEX `+quoteIdent(idx.Name)); err != nil {
				t.Fatal(err)
			}
			if err := CheckFinalSchema(context.Background(), conn); err == nil {
				t.Fatalf("dropping %s must fail the contract", idx.Name)
			}
			if _, err := conn.Exec(context.Background(), idx.CreateSQL); err != nil {
				t.Fatalf("restore %s: %v", idx.Name, err)
			}
		})
	}
}

// ADR-0005 contract (migration 0014): gateway_model keeps only its client-facing
// identity, so the legacy routing columns are gone once the migration set has
// run. A database that still carries one — an unapplied or reverted 0014 — must
// fail closed, because those columns are a second writable routing source and
// the whole point of retiring them is that there is only one.
func TestFinalSchemaDroppedLegacyModelRoutingColumns(t *testing.T) {
	url := isolatedDatabase(t)
	if _, err := Apply(context.Background(), url, repoMigrations); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	conn := connect(t, url)
	defer conn.Close(context.Background())

	for _, col := range []string{"provider_id", "upstream_model"} {
		var present bool
		if err := conn.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns
				WHERE table_name = 'gateway_model' AND column_name = $1)`, col).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if present {
			t.Fatalf("gateway_model.%s must be dropped by the migration set (ADR-0005)", col)
		}
	}

	for _, reintroduced := range []string{"provider_id UUID", "upstream_model VARCHAR(128)"} {
		if _, err := conn.Exec(context.Background(),
			`ALTER TABLE gateway_model ADD COLUMN `+reintroduced); err != nil {
			t.Fatal(err)
		}
		if err := CheckFinalSchema(context.Background(), conn); err == nil {
			t.Fatalf("a reintroduced gateway_model.%s must fail the contract", reintroduced)
		}
		if _, err := conn.Exec(context.Background(),
			`ALTER TABLE gateway_model DROP COLUMN `+strings.Fields(reintroduced)[0]); err != nil {
			t.Fatal(err)
		}
	}
}

// ADR-0017 (migration 0015): a Provider references a first-class Credential, so
// gateway_credential exists, gateway_provider.credential_id points at it, and the
// gateway_provider.credential_env column is gone. A database that reintroduces
// the column has a second place a credential can be named, so it must fail
// closed.
func TestFinalSchemaHasTheCredentialEntityAndNoCredentialEnv(t *testing.T) {
	url := isolatedDatabase(t)
	if _, err := Apply(context.Background(), url, repoMigrations); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	conn := connect(t, url)
	defer conn.Close(context.Background())

	var hasCredentialTable bool
	if err := conn.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			WHERE table_name = 'gateway_credential')`).Scan(&hasCredentialTable); err != nil {
		t.Fatal(err)
	}
	if !hasCredentialTable {
		t.Fatal("gateway_credential must exist after the migration set runs")
	}

	var hasCredentialID bool
	if err := conn.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_name = 'gateway_provider' AND column_name = 'credential_id')`).Scan(&hasCredentialID); err != nil {
		t.Fatal(err)
	}
	if !hasCredentialID {
		t.Fatal("gateway_provider.credential_id must exist after the migration set runs")
	}

	var hasCredentialEnv bool
	if err := conn.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_name = 'gateway_provider' AND column_name = 'credential_env')`).Scan(&hasCredentialEnv); err != nil {
		t.Fatal(err)
	}
	if hasCredentialEnv {
		t.Fatal("gateway_provider.credential_env must be dropped by 0015")
	}

	if _, err := conn.Exec(context.Background(),
		`ALTER TABLE gateway_provider ADD COLUMN credential_env VARCHAR(128)`); err != nil {
		t.Fatal(err)
	}
	if err := CheckFinalSchema(context.Background(), conn); err == nil {
		t.Fatal("a reintroduced gateway_provider.credential_env must fail the contract")
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

// The referenced column and delete action are part of an FK definition. An FK
// from session.user_id to admin_user.alt_id names the expected table but makes
// every normal session insert fail.
func TestFinalSchemaRejectsWrongForeignKeyDefinition(t *testing.T) {
	url, conn := migratedDatabase(t)
	if _, err := conn.Exec(context.Background(), `
		ALTER TABLE gateway_admin_user ADD COLUMN alt_id UUID UNIQUE DEFAULT gen_random_uuid();
		ALTER TABLE gateway_admin_session DROP CONSTRAINT gateway_admin_session_user_id_fkey;
		ALTER TABLE gateway_admin_session ADD CONSTRAINT gateway_admin_session_user_id_fkey
			FOREIGN KEY(user_id) REFERENCES gateway_admin_user(alt_id)`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(context.Background())

	if _, err := Apply(context.Background(), url, repoMigrations); err == nil || !strings.Contains(err.Error(), "gateway_admin_user(id) ON DELETE CASCADE") {
		t.Fatalf("wrong FK target/action must be rejected, got %v", err)
	}
}

func TestFinalSchemaRejectsWrongCheckDefinition(t *testing.T) {
	url, conn := migratedDatabase(t)
	if _, err := conn.Exec(context.Background(), `
		ALTER TABLE gateway_model_route DROP CONSTRAINT gateway_model_route_priority_chk;
		ALTER TABLE gateway_model_route ADD CONSTRAINT gateway_model_route_priority_chk CHECK (priority >= 0)`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(context.Background())

	if _, err := Apply(context.Background(), url, repoMigrations); err == nil || !strings.Contains(err.Error(), "priority >= 1") {
		t.Fatalf("wrong CHECK definition must be rejected, got %v", err)
	}
}

// Recorded history is not proof that the schema has remained intact. The
// deployment gate verifies the final catalog on every run, including a database
// with all nine rows already recorded.
func TestRecordedDatabaseStillFailsClosedOnDrift(t *testing.T) {
	url := isolatedDatabase(t)
	if _, err := Apply(context.Background(), url, repoMigrations); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	conn := connect(t, url)
	if _, err := conn.Exec(context.Background(),
		`ALTER TABLE gateway_admin_user ALTER COLUMN status SET DEFAULT 0`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(context.Background())

	if _, err := Apply(context.Background(), url, repoMigrations); err == nil {
		t.Fatal("recorded migration history must not bypass final schema verification")
	}
}

// A failed bootstrap leaves an empty schema_migrations table behind. That
// state must remain a bootstrap on the next invocation; otherwise the ordinary
// per-file path records all migrations without running the final contract.
func TestFailedBootstrapCannotBypassContractOnSecondRun(t *testing.T) {
	url := isolatedDatabase(t)
	conn := connect(t, url)
	if _, err := conn.Exec(context.Background(),
		`CREATE TABLE gateway_tenant (id UUID PRIMARY KEY, code INTEGER)`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(context.Background())

	for run := 1; run <= 2; run++ {
		if _, err := Apply(context.Background(), url, repoMigrations); err == nil {
			t.Fatalf("run %d accepted the drifted schema", run)
		}
		conn = connect(t, url)
		var count int
		if err := conn.QueryRow(context.Background(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		_ = conn.Close(context.Background())
		if count != 0 {
			t.Fatalf("run %d recorded %d migrations after failed verification", run, count)
		}
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
