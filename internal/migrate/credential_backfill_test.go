package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 0015 replaces gateway_provider.credential_env with a first-class Credential
// row and a credential_id reference (ADR-0017). Existing channels must keep the
// environment variable they named: the migration translates each non-null
// credential_env into one source_type='env' Credential and only then drops the
// column.
//
// The ordering matters. Applying everything at once and inserting afterwards
// would never exercise the backfill, so this test brings the database to 0014,
// seeds channels in the shape a deployment has today, and lets the real
// migration directory run from there.
func TestCredentialEnvBackfillsOneEnvCredentialPerProvider(t *testing.T) {
	url := isolatedDatabase(t)
	ctx := context.Background()

	dir := t.TempDir()
	entries, err := os.ReadDir(repoMigrations)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= "0015" {
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

	seed := []struct {
		name string
		env  string
	}{
		{"channel-a", "CHANNEL_A_API_KEY"},
		{"channel-b", "CHANNEL_B_API_KEY"},
	}
	providerIDs := make(map[string]string, len(seed))
	for _, s := range seed {
		var id string
		if err := conn.QueryRow(ctx, `
INSERT INTO gateway_provider(name, base_url, protocol, credential_env)
VALUES ($1, 'https://example.test', 'openai-chat', $2)
RETURNING id::text`, s.name, s.env).Scan(&id); err != nil {
			t.Fatal(err)
		}
		providerIDs[s.name] = id
	}
	_ = conn.Close(ctx)

	if _, err := Apply(ctx, url, repoMigrations); err != nil {
		t.Fatalf("apply 0015: %v", err)
	}

	conn = connect(t, url)
	defer func() { _ = conn.Close(ctx) }()

	for _, s := range seed {
		var credentialID, sourceType, secretRef string
		var ciphertext *string
		if err := conn.QueryRow(ctx, `
SELECT c.id::text, c.source_type, COALESCE(c.secret_ref,''), c.ciphertext
FROM gateway_provider p
JOIN gateway_credential c ON c.id = p.credential_id
WHERE p.id=$1::uuid`, providerIDs[s.name]).Scan(&credentialID, &sourceType, &secretRef, &ciphertext); err != nil {
			t.Fatalf("%s: 0015 must leave the provider with an env Credential: %v", s.name, err)
		}
		if credentialID == "" {
			t.Fatalf("%s: credential_id was not backfilled", s.name)
		}
		if sourceType != "env" || secretRef != s.env {
			t.Fatalf("%s: credential = source %q ref %q, want env/%q", s.name, sourceType, secretRef, s.env)
		}
		if ciphertext != nil {
			t.Fatalf("%s: an env credential must not carry a ciphertext", s.name)
		}

		var count int
		if err := conn.QueryRow(ctx,
			`SELECT COUNT(*) FROM gateway_credential c
			 JOIN gateway_provider p ON p.credential_id = c.id
			 WHERE p.id=$1::uuid`, providerIDs[s.name]).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s: expected exactly one Credential, got %d", s.name, count)
		}
	}

	var stillThere bool
	if err := conn.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM information_schema.columns
  WHERE table_name = 'gateway_provider' AND column_name = 'credential_env'
)`).Scan(&stillThere); err != nil {
		t.Fatal(err)
	}
	if stillThere {
		t.Fatal("0015 must drop gateway_provider.credential_env after the backfill")
	}
}
