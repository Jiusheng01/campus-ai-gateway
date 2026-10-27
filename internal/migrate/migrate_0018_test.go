package migrate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// applyBeforeAndSeed leaves a database at the schema recorded by every
// migration below cutoff, then seeds the ownership rows 0018 must preserve.
// The runner is not used for this half: Apply would also verify the final
// contract, and the pre-0018 schema deliberately lacks the objects 0018 adds.
func applyBeforeAndSeed(t *testing.T, url string, files []Migration, cutoff int) {
	t.Helper()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect legacy: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	if _, err := conn.Exec(ctx, `CREATE TABLE `+schemaMigrationsTable+` (
		version INTEGER PRIMARY KEY,
		filename VARCHAR(255) NOT NULL,
		checksum VARCHAR(64) NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		t.Fatalf("create bookkeeping: %v", err)
	}
	for _, m := range files {
		if m.Version >= cutoff {
			continue
		}
		if _, err := conn.Exec(ctx, m.Body); err != nil {
			t.Fatalf("apply legacy %s: %v", m.Filename, err)
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO `+schemaMigrationsTable+`(version, filename, checksum) VALUES ($1,$2,$3)`,
			m.Version, m.Filename, m.Checksum); err != nil {
			t.Fatalf("record legacy %s: %v", m.Filename, err)
		}
	}

	seed := []string{
		`INSERT INTO gateway_tenant(code,name) VALUES ('owned-tenant','Owned Tenant')`,
		`INSERT INTO gateway_admin_user(username,display_name,password_hash,roles)
		 VALUES ('legacy-account','Legacy Account','hash','["member"]')`,
		`INSERT INTO gateway_api_key(tenant_id,name,key_prefix,key_digest,status,allowed_models)
		 SELECT id,'legacy-key','lgcy','legacy-digest',1,'["*"]'::jsonb FROM gateway_tenant WHERE code='owned-tenant'`,
		`INSERT INTO gateway_request_log(request_id,tenant_id,api_key_id,requested_model,status_code)
		 SELECT 'legacy-request-1',t.id,k.id,'legacy-model',200
		 FROM gateway_tenant t, gateway_api_key k
		 WHERE t.code='owned-tenant' AND k.name='legacy-key'`,
		`INSERT INTO gateway_request_log(request_id,tenant_id,api_key_id,requested_model,status_code)
		 SELECT 'legacy-request-2',t.id,k.id,'legacy-model',500
		 FROM gateway_tenant t, gateway_api_key k
		 WHERE t.code='owned-tenant' AND k.name='legacy-key'`,
	}
	for _, stmt := range seed {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
}

// TestMigration0018UpgradesSeededDatabase is the upgrade seam for the ownership
// model. It starts at a real 0017 database with a tenant, an account, a key and
// Request Records already in it, applies only 0018, and asserts every row
// survives with the documented defaults. The key is looked up through the same
// storage query the data plane uses, so a migration that added the columns but
// not their data-plane contract fails here too.
func TestMigration0018UpgradesSeededDatabase(t *testing.T) {
	url := isolatedDatabase(t)
	files, err := Load(repoMigrations)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	applyBeforeAndSeed(t, url, files, 18)

	result, err := Apply(context.Background(), url, repoMigrations)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// The runner advances the database to the end of the migration set, so a
	// database parked at 0017 crosses 0018 and every later file too. The
	// assertions below still pin what 0018 itself is responsible for.
	if len(result.Applied) != 2 ||
		!strings.HasSuffix(result.Applied[0], "0018_ownership_and_snapshot.sql") ||
		!strings.HasSuffix(result.Applied[1], "0019_admin_password_reset.sql") {
		t.Fatalf("applied = %v, want 0018 then 0019", result.Applied)
	}

	second, err := Apply(context.Background(), url, repoMigrations)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if len(second.Applied) != 0 {
		t.Fatalf("second Apply applied %v", second.Applied)
	}

	cfg, _ := pgx.ParseConfig(url)
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect upgraded: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	ctx := context.Background()

	if err := CheckFinalSchema(ctx, conn); err != nil {
		t.Fatalf("final schema contract after 0018: %v", err)
	}

	// Every seeded row survives, and the new columns are ownerless because
	// nothing has assigned an owner to pre-0018 data.
	for _, q := range []string{
		`SELECT COUNT(*) FROM gateway_tenant WHERE code='owned-tenant'`,
		`SELECT COUNT(*) FROM gateway_admin_user WHERE username='legacy-account'`,
		`SELECT COUNT(*) FROM gateway_api_key WHERE name='legacy-key'`,
		`SELECT COUNT(*) FROM gateway_request_log WHERE request_id='legacy-request-1'`,
		`SELECT COUNT(*) FROM gateway_request_log WHERE request_id='legacy-request-2'`,
	} {
		var count int
		if err := conn.QueryRow(ctx, q).Scan(&count); err != nil || count != 1 {
			t.Fatalf("preserved row check %q = %d, err %v", q, count, err)
		}
	}

	var accountID string
	var tenantID, keyOwner, logOwner *string
	if err := conn.QueryRow(ctx,
		`SELECT id::text, tenant_id::text FROM gateway_admin_user WHERE username='legacy-account'`).
		Scan(&accountID, &tenantID); err != nil {
		t.Fatalf("admin user after 0018: %v", err)
	}
	if tenantID != nil {
		t.Fatalf("existing account must stay unassigned: id=%s tenant_id=%v", accountID, *tenantID)
	}
	var seededTenantID string
	if err := conn.QueryRow(ctx,
		`SELECT id::text FROM gateway_tenant WHERE code='owned-tenant'`).Scan(&seededTenantID); err != nil {
		t.Fatalf("seeded tenant id: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`SELECT owner_user_id::text FROM gateway_api_key WHERE name='legacy-key'`).Scan(&keyOwner); err != nil {
		t.Fatalf("key owner after 0018: %v", err)
	}
	if keyOwner != nil {
		t.Fatalf("existing key gained an owner: %v", *keyOwner)
	}
	if err := conn.QueryRow(ctx,
		`SELECT owner_user_id::text FROM gateway_request_log WHERE request_id='legacy-request-1'`).Scan(&logOwner); err != nil {
		t.Fatalf("request log owner after 0018: %v", err)
	}
	if logOwner != nil {
		t.Fatalf("existing Request Record gained an owner: %v", *logOwner)
	}

	// An existing Tenant allows every model after the migration, and so does a
	// Tenant created afterwards without naming the column.
	var allowed string
	if err := conn.QueryRow(ctx,
		`SELECT allowed_models::text FROM gateway_tenant WHERE code='owned-tenant'`).Scan(&allowed); err != nil {
		t.Fatalf("tenant allowlist after 0018: %v", err)
	}
	if allowed != `["*"]` {
		t.Fatalf("existing tenant allowed_models = %s, want [\"*\"]", allowed)
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO gateway_tenant(code,name) VALUES ('fresh-tenant','Fresh Tenant')`); err != nil {
		t.Fatalf("insert tenant after 0018: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`SELECT allowed_models::text FROM gateway_tenant WHERE code='fresh-tenant'`).Scan(&allowed); err != nil {
		t.Fatalf("new tenant default: %v", err)
	}
	if allowed != `["*"]` {
		t.Fatalf("a new tenant's allowed_models = %s, want [\"*\"]", allowed)
	}

	// The key still authenticates: authentication is a lookup by digest that
	// accepts an enabled key under an enabled tenant, and all three still
	// resolve to the seeded rows.
	var keyStatus, tenantStatus int
	if err := conn.QueryRow(ctx, `
SELECT k.status, t.status
FROM gateway_api_key k JOIN gateway_tenant t ON t.id=k.tenant_id
WHERE k.key_digest='legacy-digest' AND k.tenant_id=$1::uuid`, seededTenantID).
		Scan(&keyStatus, &tenantStatus); err != nil {
		t.Fatalf("the preserved key no longer authenticates: %v", err)
	}
	if keyStatus != 1 || tenantStatus != 1 {
		t.Fatalf("key/tenant status after 0018 = %d/%d, want 1/1", keyStatus, tenantStatus)
	}

	// ON DELETE SET NULL: removing a Tenant keeps the Account and clears its
	// membership; removing the Account keeps the key and clears its owner.
	if _, err := conn.Exec(ctx,
		`UPDATE gateway_admin_user SET tenant_id=(SELECT id FROM gateway_tenant WHERE code='owned-tenant')
		 WHERE username='legacy-account'`); err != nil {
		t.Fatalf("assign tenant: %v", err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM gateway_api_key WHERE name='legacy-key'`); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM gateway_tenant WHERE code='owned-tenant'`); err != nil {
		t.Fatalf("delete tenant: %v", err)
	}
	var tenantAfter *string
	if err := conn.QueryRow(ctx,
		`SELECT tenant_id::text FROM gateway_admin_user WHERE username='legacy-account'`).Scan(&tenantAfter); err != nil {
		t.Fatalf("account after tenant delete: %v", err)
	}
	if tenantAfter != nil {
		t.Fatalf("deleting the tenant removed the membership by cascade instead of SET NULL: %v", *tenantAfter)
	}
}

// A key owned by an Account loses its owner when that Account is removed, but
// the key row itself survives: an administrator can still see and revoke it.
func TestMigration0018KeyOwnerIsSetNullWhenTheAccountIsDeleted(t *testing.T) {
	url := isolatedDatabase(t)
	if _, err := Apply(context.Background(), url, repoMigrations); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	ctx := context.Background()
	conn := connect(t, url)
	defer func() { _ = conn.Close(ctx) }()

	var tenantID, userID, keyID string
	if err := conn.QueryRow(ctx,
		`INSERT INTO gateway_tenant(code,name) VALUES ('fk-tenant','FK Tenant') RETURNING id::text`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx,
		`INSERT INTO gateway_admin_user(username,password_hash,roles) VALUES ('fk-account','hash','["member"]') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx,
		`INSERT INTO gateway_api_key(tenant_id,name,key_prefix,key_digest,owner_user_id)
		 VALUES ($1,'fk-key','fk','fk-digest',$2) RETURNING id::text`, tenantID, userID).Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM gateway_admin_user WHERE id=$1::uuid`, userID); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	var owner *string
	if err := conn.QueryRow(ctx,
		`SELECT owner_user_id::text FROM gateway_api_key WHERE id=$1::uuid`, keyID).Scan(&owner); err != nil {
		t.Fatalf("key after account delete: %v", err)
	}
	if owner != nil {
		t.Fatalf("deleting the account cascaded into the key instead of SET NULL: %v", *owner)
	}
}
