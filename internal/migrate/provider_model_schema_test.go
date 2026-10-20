package migrate

import (
	"context"
	"testing"
)

// 0016 gives every Provider its own list of Provider Models (ADR-0018). The
// list is a fact about the schema, not about a handler, so it is pinned here:
// the table exists, it belongs to exactly one Provider, the same upstream model
// cannot be listed twice for one Provider, and removing the Provider removes its
// list rather than leaving orphans behind.
func TestProviderModelTableIsPartOfTheFinalSchema(t *testing.T) {
	url := isolatedDatabase(t)
	ctx := context.Background()
	if _, err := Apply(ctx, url, repoMigrations); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	conn := connect(t, url)
	defer func() { _ = conn.Close(ctx) }()

	columns := map[string]struct {
		dataType string
		notNull  bool
	}{}
	rows, err := conn.Query(ctx, `
SELECT column_name, data_type, is_nullable
FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = 'gateway_provider_model'`)
	if err != nil {
		t.Fatalf("introspect gateway_provider_model: %v", err)
	}
	for rows.Next() {
		var name, dataType, nullable string
		if err := rows.Scan(&name, &dataType, &nullable); err != nil {
			rows.Close()
			t.Fatalf("scan column: %v", err)
		}
		columns[name] = struct {
			dataType string
			notNull  bool
		}{dataType, nullable == "NO"}
	}
	rows.Close()

	for name, want := range map[string]struct {
		dataType string
		notNull  bool
	}{
		"provider_id":    {"uuid", true},
		"upstream_model": {"character varying", true},
		"source":         {"character varying", true},
		"status":         {"smallint", true},
		"created_at":     {"timestamp with time zone", true},
		"updated_at":     {"timestamp with time zone", true},
	} {
		got, ok := columns[name]
		if !ok {
			t.Fatalf("gateway_provider_model.%s is missing", name)
		}
		if got.dataType != want.dataType || got.notNull != want.notNull {
			t.Fatalf("gateway_provider_model.%s = %s notNull=%t, want %s notNull=%t",
				name, got.dataType, got.notNull, want.dataType, want.notNull)
		}
	}

	var uniqueCols []string
	if err := conn.QueryRow(ctx, `
SELECT array_agg(a.attname ORDER BY u.ord)::text[]
FROM pg_constraint c
JOIN LATERAL unnest(c.conkey) WITH ORDINALITY u(attnum, ord) ON true
JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = u.attnum
WHERE c.conrelid = 'gateway_provider_model'::regclass
  AND c.contype = 'u'
GROUP BY c.oid`).Scan(&uniqueCols); err != nil {
		t.Fatalf("gateway_provider_model must have a unique constraint: %v", err)
	}
	if len(uniqueCols) != 2 || uniqueCols[0] != "provider_id" || uniqueCols[1] != "upstream_model" {
		t.Fatalf("unique constraint covers (%v), want (provider_id, upstream_model)", uniqueCols)
	}

	var refTable, deleteCode string
	if err := conn.QueryRow(ctx, `
SELECT c.confrelid::regclass::text, c.confdeltype::text
FROM pg_constraint c
JOIN pg_attribute src ON src.attrelid = c.conrelid AND src.attnum = c.conkey[1]
WHERE c.conrelid = 'gateway_provider_model'::regclass
  AND c.contype = 'f' AND src.attname = 'provider_id'`).Scan(&refTable, &deleteCode); err != nil {
		t.Fatalf("gateway_provider_model.provider_id must be a foreign key: %v", err)
	}
	if refTable != "gateway_provider" || deleteCode != "c" {
		t.Fatalf("provider_id references %s ON DELETE %s, want gateway_provider ON DELETE CASCADE", refTable, deleteCode)
	}
}
