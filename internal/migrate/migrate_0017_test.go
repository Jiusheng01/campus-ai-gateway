package migrate

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// applyLegacyAndSeed leaves a database at the schema recorded by migrations
// 0001-0016, then seeds the rows MS1 promises to preserve. The runner is not
// used for this half: its compiled final contract already expects 0017.
func applyLegacyAndSeed(t *testing.T, url string, files []Migration) {
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
		if m.Version >= 17 {
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
		`INSERT INTO gateway_tenant(code,name) VALUES ('legacy-tenant','Legacy Tenant')`,
		`INSERT INTO gateway_credential(name,source_type,secret_ref) VALUES ('legacy-credential','env','LEGACY_KEY')`,
		`INSERT INTO gateway_provider(name,base_url,protocol,credential_id,status)
		 SELECT 'legacy-provider','https://legacy.example.test/v1','openai-chat',id,1 FROM gateway_credential WHERE name='legacy-credential'`,
		`INSERT INTO gateway_model(alias,status) VALUES ('legacy-alias',1)`,
		`INSERT INTO gateway_model_route(model_id,provider_id,upstream_model,priority,weight,timeout_ms,status)
		 SELECT m.id,p.id,'legacy-up',1,0,60000,1 FROM gateway_model m, gateway_provider p WHERE m.alias='legacy-alias' AND p.name='legacy-provider'`,
		`INSERT INTO gateway_api_key(tenant_id,name,key_prefix,key_digest,status,allowed_models)
		 SELECT id,'legacy-key','lgcy','legacy-digest',1,'["*"]'::jsonb FROM gateway_tenant WHERE code='legacy-tenant'`,
		`INSERT INTO gateway_request_log(request_id,requested_model,provider,upstream_model,status_code)
		 VALUES ('legacy-request','legacy-alias','legacy-provider','legacy-up',200)`,
	}
	for _, stmt := range seed {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
}

func TestMigration0017UpgradesSeededPreMS1Database(t *testing.T) {
	url := isolatedDatabase(t)
	files, err := Load(repoMigrations)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	applyLegacyAndSeed(t, url, files)

	result, err := Apply(context.Background(), url, repoMigrations)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Applied) != 1 || !strings.HasSuffix(result.Applied[0], "0017_model_service_management.sql") {
		t.Fatalf("applied = %v, want only 0017", result.Applied)
	}

	cfg, _ := pgx.ParseConfig(url)
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect upgraded: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	for _, tc := range []struct{ table, column string }{
		{"gateway_provider", "provider_type"},
		{"gateway_provider", "last_test_at"},
		{"gateway_model", "display_name"},
		{"gateway_model", "config_version"},
	} {
		var exists bool
		if err := conn.QueryRow(context.Background(), `
SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2)`,
			tc.table, tc.column).Scan(&exists); err != nil {
			t.Fatalf("column check %s.%s: %v", tc.table, tc.column, err)
		}
		if !exists {
			t.Fatalf("column %s.%s missing after 0017", tc.table, tc.column)
		}
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM gateway_tenant WHERE code='legacy-tenant'`,
		`SELECT COUNT(*) FROM gateway_api_key WHERE name='legacy-key'`,
		`SELECT COUNT(*) FROM gateway_request_log WHERE request_id='legacy-request'`,
	} {
		var count int
		if err := conn.QueryRow(context.Background(), q).Scan(&count); err != nil || count != 1 {
			t.Fatalf("preserved row check %q = %d, err %v", q, count, err)
		}
	}
	var weight int
	if err := conn.QueryRow(context.Background(), `SELECT weight FROM gateway_model_route WHERE upstream_model='legacy-up'`).Scan(&weight); err != nil || weight != 0 {
		t.Fatalf("legacy zero weight = %d, err %v", weight, err)
	}
	var version int64
	var display string
	if err := conn.QueryRow(context.Background(), `SELECT config_version, display_name FROM gateway_model WHERE alias='legacy-alias'`).Scan(&version, &display); err != nil {
		t.Fatalf("model defaults: %v", err)
	}
	if version != 1 || display != "" {
		t.Fatalf("model defaults = %d %q", version, display)
	}

	second, err := Apply(context.Background(), url, repoMigrations)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if len(second.Applied) != 0 {
		t.Fatalf("second Apply applied %v", second.Applied)
	}
}

// A pre-MS1 binary with only the old migration directory must fail closed when
// the database records 0017. This is the migration-history one-way door; the
// operations note gives the executable rollback path.
func TestPreMS1BinaryRefusesDatabaseRecording0017(t *testing.T) {
	onDisk := []Migration{{Version: 16, Filename: "0016_old.sql"}}
	inDB := []recorded{{Version: 17, Filename: "0017_model_service_management.sql"}}
	_, err := checkHistory(onDisk, inDB)
	if err == nil || !strings.Contains(err.Error(), "recorded but not in the directory") {
		t.Fatalf("checkHistory = %v, want ErrMissingMigration", err)
	}
	if !strings.Contains(fmt.Sprint(err), "0017") {
		t.Fatalf("error does not name the unknown version: %v", err)
	}
}
