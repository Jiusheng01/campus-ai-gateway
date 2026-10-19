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
//
// The unrestricted rows come in two shapes because the gateway wrote two. An
// empty array is what the console sent; JSON null is what the writer stored when
// the field was omitted entirely, and the old data plane read both as "no
// restriction". A backfill that covered only the array would revoke the null
// rows silently.
func TestAllowedModelsBackfillKeepsEveryUnrestrictedRow(t *testing.T) {
	url := isolatedDatabase(t)

	dir := t.TempDir()
	entries, err := os.ReadDir(repoMigrations)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		// Everything before the backfill.
		//
		// These files are applied through the runner's own primitives rather than
		// Apply, because Apply also verifies the whole end-state schema contract
		// and this phase deliberately stops at 0010 — a schema that, by design,
		// is missing the columns later migrations add. Skipping the contract here
		// is what lets the rows be inserted at the pre-backfill shape, which is
		// the ordering this test exists to exercise.
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

	conn := connect(t, url)
	pre, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureBookkeeping(ctx, conn); err != nil {
		t.Fatalf("prepare bookkeeping: %v", err)
	}
	for _, m := range pre {
		if err := applyOne(ctx, conn, m); err != nil {
			t.Fatalf("apply up to 0010: %v", err)
		}
	}

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
		{"legacy-null", `null`},
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
		"legacy-null":      `["*"]`,
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
