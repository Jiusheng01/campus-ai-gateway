package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The allowed_models backfill rewrites existing rows, so it is the one change in
// this release that can silently alter live access. These assertions pin both
// halves of the contract: the rows that were unrestricted keep being
// unrestricted, and the rows that were restricted are not widened.
//
// The migration is applied in two phases on purpose. Rows are inserted while the
// schema is still at 0010, exactly as they would exist in a deployment that is
// being upgraded, and only then does 0011 run over them. Applying everything at
// once and inserting afterwards would test the predicate but not the ordering.
func TestAllowedModelsBackfillConvertsOnlyTheEmptyList(t *testing.T) {
	url := isolatedDatabase(t)

	dir := t.TempDir()
	entries, err := os.ReadDir(repoMigrations)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		// Everything before the backfill.
		if e.Name() >= "0011" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(repoMigrations, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Apply(context.Background(), url, dir); err != nil {
		t.Fatalf("apply up to 0010: %v", err)
	}

	conn := connect(t, url)
	ctx := context.Background()
	var tenantID string
	if err := conn.QueryRow(ctx,
		`INSERT INTO gateway_tenant(code, name) VALUES ('cs', 'CS') RETURNING id::text`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	seed := []struct {
		name  string
		value string
	}{
		{"unrestricted", `[]`},
		{"already-wildcard", `["*"]`},
		{"restricted", `["campus-smart"]`},
	}
	for _, s := range seed {
		if _, err := conn.Exec(ctx,
			`INSERT INTO gateway_api_key(tenant_id, name, key_prefix, key_digest, allowed_models)
			 VALUES ($1, $2, 'sk-test', $3, $4::jsonb)`,
			tenantID, s.name, "digest-"+s.name, s.value); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.Close(ctx)

	if _, err := Apply(context.Background(), url, repoMigrations); err != nil {
		t.Fatalf("apply 0011: %v", err)
	}

	conn = connect(t, url)
	defer func() { _ = conn.Close(context.Background()) }()
	for name, want := range map[string]string{
		"unrestricted":     `["*"]`,
		"already-wildcard": `["*"]`,
		"restricted":       `["campus-smart"]`,
	} {
		var got string
		if err := conn.QueryRow(context.Background(),
			`SELECT allowed_models::text FROM gateway_api_key WHERE name=$1`, name).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s: allowed_models = %s, want %s", name, got, want)
		}
	}
}
