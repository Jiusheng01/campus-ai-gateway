package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 0014 drops gateway_model.provider_id / upstream_model (ADR-0005). Before it
// does, every existing model must own a Route, or the drop would leave a model
// with nowhere to route to.
//
// 0003 backfilled one Route per model and DeleteModelRoute refuses to remove the
// last one, so that should already hold. This test seeds the shape those two
// guarantees do not cover — a model with no Route at all — and asserts 0014
// repairs it from the legacy binding before the binding disappears.
func TestLegacyModelBindingBackfillsTheFirstRouteBeforeDropping(t *testing.T) {
	url := isolatedDatabase(t)
	ctx := context.Background()

	// Bring the database to the state just before 0014, with bookkeeping, so
	// that applying the real directory runs only 0014 over the seeded rows.
	dir := t.TempDir()
	entries, err := os.ReadDir(repoMigrations)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "0014" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(repoMigrations, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), body, 0o600); err != nil {
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
			t.Fatalf("apply %s: %v", m.Filename, err)
		}
	}

	var providerID string
	if err := conn.QueryRow(ctx, `
INSERT INTO gateway_provider(name, base_url, protocol, credential_env)
VALUES ('legacy', 'https://legacy.example.test', 'openai-chat', 'LEGACY_KEY')
RETURNING id::text`).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	var modelID string
	if err := conn.QueryRow(ctx, `
INSERT INTO gateway_model(alias, provider_id, upstream_model, status)
VALUES ('demo-model', $1::uuid, 'qwen-max', 1)
RETURNING id::text`, providerID).Scan(&modelID); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)

	if _, err := Apply(ctx, url, repoMigrations); err != nil {
		t.Fatalf("apply 0014: %v", err)
	}

	conn = connect(t, url)
	defer func() { _ = conn.Close(ctx) }()

	var routeProviderID, upstream string
	var priority, status int
	if err := conn.QueryRow(ctx, `
SELECT provider_id::text, upstream_model, priority, status
FROM gateway_model_route
WHERE model_id=$1::uuid`, modelID).Scan(&routeProviderID, &upstream, &priority, &status); err != nil {
		t.Fatalf("0014 must leave the model with a Route: %v", err)
	}
	if routeProviderID != providerID || upstream != "qwen-max" || priority != 1 || status != 1 {
		t.Fatalf("backfilled route = provider %s upstream %q priority %d status %d",
			routeProviderID, upstream, priority, status)
	}

	var stillThere bool
	if err := conn.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM information_schema.columns
  WHERE table_name = 'gateway_model' AND column_name IN ('provider_id', 'upstream_model')
)`).Scan(&stillThere); err != nil {
		t.Fatal(err)
	}
	if stillThere {
		t.Fatal("0014 must drop the legacy binding columns after the backfill")
	}
}
