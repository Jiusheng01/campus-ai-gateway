package migrate

// Final Schema Contract
//
// This file is the mechanism that stops a drifted legacy database from being
// silently marked "migrated". See the package doc for the threat model: a
// legacy deployment whose gateway_api_key table only has (id, tenant_id) — the
// shape an early hand-built script produced — passes a "the table exists" check
// but fails every runtime query that selects key_digest. The old baseline only
// checked a handful of tables and a few columns, so it called that database
// current.
//
// The contract below is the complete set of objects the current migration set
// is supposed to leave behind, derived by hand from migrations/0001..0009. It
// covers, per table: every column with its PostgreSQL data type and
// nullability, the primary key, the security-critical unique constraints, the
// foreign keys, and the runtime indexes the data plane depends on. It does not
// pin irrelevant details such as a constraint's auto-generated OID or the
// exact default expression — those never change whether the application SQL
// runs.
//
// Maintenance rule: when a migration adds a table, a column the runtime reads,
// a unique constraint, a foreign key, or an index the request path filters on,
// extend the contract below. It is deliberately explicit (no SQL text parsing,
// no reflection on the running binary) so a wrong edit fails a test instead of
// letting a drifted database through.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// contractColumn is one expected column of a contract table.
type contractColumn struct {
	Name     string
	DataType string // information_schema.columns.data_type, verbatim
	NotNull  bool   // true => the column must be NOT NULL
}

// contractTable is the expected end state of one business table.
type contractTable struct {
	Name    string
	Columns []contractColumn
}

// contractFK is one expected foreign key (single-column, matching every FK this
// repository declares).
type contractFK struct {
	Table    string
	Column   string
	RefTable string
}

// contractIndex is one expected named index the runtime relies on. Unique is
// true when the index must also enforce uniqueness (a CREATE UNIQUE INDEX that
// is not backed by a pg_constraint row, e.g. a composite or functional unique
// index).
type contractIndex struct {
	Table  string
	Name   string
	Unique bool
}

// finalSchemaContract returns the objects the current migration set must leave
// behind. The data types are the exact strings PostgreSQL reports in
// information_schema.columns.data_type.
func finalSchemaContract() (tables []contractTable, uniques []contractUniqueKey, fks []contractFK, indexes []contractIndex) {
	tables = []contractTable{
		{
			Name: "gateway_tenant",
			Columns: []contractColumn{
				{"id", "uuid", true},
				{"code", "character varying", true},
				{"name", "character varying", true},
				{"status", "smallint", true},
				{"created_at", "timestamp with time zone", true},
				{"updated_at", "timestamp with time zone", true},
				{"daily_token_limit", "bigint", true},
				{"monthly_token_limit", "bigint", true},
				{"monthly_request_limit", "bigint", true},
				{"monthly_budget_micros", "bigint", true},
			},
		},
		{
			Name: "gateway_api_key",
			Columns: []contractColumn{
				{"id", "uuid", true},
				{"tenant_id", "uuid", true},
				{"name", "character varying", true},
				{"key_prefix", "character varying", true},
				{"key_digest", "character varying", true},
				{"status", "smallint", true},
				{"expire_at", "timestamp with time zone", false},
				{"last_used_at", "timestamp with time zone", false},
				{"allowed_models", "jsonb", true},
				{"allowed_ips", "jsonb", true},
				{"rpm_limit", "integer", true},
				{"tpm_limit", "bigint", true},
				{"max_concurrency", "integer", true},
				{"route_group", "character varying", false},
				{"created_at", "timestamp with time zone", true},
				{"updated_at", "timestamp with time zone", true},
			},
		},
		{
			Name: "gateway_request_log",
			Columns: []contractColumn{
				{"id", "bigint", true},
				{"request_id", "character varying", true},
				{"tenant_id", "uuid", false},
				{"api_key_id", "uuid", false},
				{"client_ip", "character varying", false},
				{"requested_model", "character varying", false},
				{"actual_model", "character varying", false},
				{"provider", "character varying", false},
				{"stream", "boolean", true},
				{"input_tokens", "bigint", false},
				{"output_tokens", "bigint", false},
				{"total_tokens", "bigint", false},
				{"latency_ms", "bigint", false},
				{"status_code", "integer", false},
				{"error_code", "character varying", false},
				{"created_at", "timestamp with time zone", true},
				{"upstream_model", "character varying", false},
				{"route_priority", "integer", false},
				{"attempts", "integer", false},
				{"failover", "boolean", false},
			},
		},
		{
			Name: "gateway_provider",
			Columns: []contractColumn{
				{"id", "uuid", true},
				{"name", "character varying", true},
				{"base_url", "text", true},
				{"protocol", "character varying", true},
				{"credential_env", "character varying", true},
				{"status", "smallint", true},
				{"created_at", "timestamp with time zone", true},
				{"updated_at", "timestamp with time zone", true},
			},
		},
		{
			Name: "gateway_model",
			Columns: []contractColumn{
				{"id", "uuid", true},
				{"alias", "character varying", true},
				{"provider_id", "uuid", true},
				{"upstream_model", "character varying", true},
				{"status", "smallint", true},
				{"created_at", "timestamp with time zone", true},
				{"updated_at", "timestamp with time zone", true},
			},
		},
		{
			Name: "gateway_model_route",
			Columns: []contractColumn{
				{"id", "uuid", true},
				{"model_id", "uuid", true},
				{"provider_id", "uuid", true},
				{"upstream_model", "character varying", true},
				{"priority", "integer", true},
				{"weight", "integer", true},
				{"timeout_ms", "integer", true},
				{"status", "smallint", true},
				{"created_at", "timestamp with time zone", true},
				{"updated_at", "timestamp with time zone", true},
			},
		},
		{
			Name: "gateway_admin_audit_log",
			Columns: []contractColumn{
				{"id", "bigint", true},
				{"request_id", "character varying", false},
				{"actor", "character varying", true},
				{"action", "character varying", true},
				{"resource", "character varying", true},
				{"status_code", "integer", true},
				{"client_ip", "character varying", false},
				{"created_at", "timestamp with time zone", true},
			},
		},
		{
			Name: "gateway_security_event",
			Columns: []contractColumn{
				{"id", "bigint", true},
				{"request_id", "character varying", false},
				{"tenant_id", "uuid", false},
				{"api_key_id", "uuid", false},
				{"event_type", "character varying", true},
				{"client_ip", "character varying", false},
				{"requested_model", "character varying", false},
				{"detail", "character varying", false},
				{"created_at", "timestamp with time zone", true},
			},
		},
		{
			Name: "gateway_admin_user",
			Columns: []contractColumn{
				{"id", "uuid", true},
				{"username", "character varying", true},
				{"password_hash", "character varying", true},
				{"display_name", "character varying", true},
				{"roles", "jsonb", true},
				{"status", "smallint", true},
				{"auth_version", "bigint", true},
				{"last_login_at", "timestamp with time zone", false},
				{"created_at", "timestamp with time zone", true},
				{"updated_at", "timestamp with time zone", true},
			},
		},
		{
			Name: "gateway_admin_session",
			Columns: []contractColumn{
				{"sid", "uuid", true},
				{"user_id", "uuid", true},
				{"version", "bigint", true},
				{"user_auth_version", "bigint", true},
				{"status", "smallint", true},
				{"refresh_hash", "character varying", true},
				{"login_method", "character varying", true},
				{"ip", "character varying", true},
				{"user_agent", "character varying", true},
				{"created_at", "timestamp with time zone", true},
				{"last_active_at", "timestamp with time zone", true},
				{"expires_at", "timestamp with time zone", true},
				{"revoked_at", "timestamp with time zone", false},
				{"revoke_reason", "character varying", true},
				{"previous_refresh_hash", "character varying", true},
				{"previous_refresh_valid_until", "timestamp with time zone", false},
			},
		},
	}

	// uniques are the primary-key and UNIQUE-constraint column groups that
	// PostgreSQL records in pg_constraint (contype p or u). Lower-cased and
	// sorted so a reordered constraint still matches. The two unique indexes
	// that are NOT backed by a pg_constraint row (a functional unique index and
	// a composite unique index) are checked separately below.
	uniques = []contractUniqueKey{
		{"gateway_tenant", "id"},
		{"gateway_tenant", "code"},
		{"gateway_api_key", "id"},
		{"gateway_api_key", "key_digest"},
		{"gateway_request_log", "id"},
		{"gateway_request_log", "request_id"},
		{"gateway_provider", "id"},
		{"gateway_provider", "name"},
		{"gateway_model", "id"},
		{"gateway_model", "alias"},
		{"gateway_model_route", "id"},
		{"gateway_admin_audit_log", "id"},
		{"gateway_security_event", "id"},
		{"gateway_admin_user", "id"},
		{"gateway_admin_session", "sid"},
	}

	fks = []contractFK{
		{"gateway_api_key", "tenant_id", "gateway_tenant"},
		{"gateway_model", "provider_id", "gateway_provider"},
		{"gateway_model_route", "model_id", "gateway_model"},
		{"gateway_model_route", "provider_id", "gateway_provider"},
		{"gateway_admin_session", "user_id", "gateway_admin_user"},
	}

	indexes = []contractIndex{
		{Table: "gateway_api_key", Name: "idx_gateway_api_key_tenant"},
		{Table: "gateway_admin_session", Name: "idx_gateway_admin_session_user"},
		{Table: "gateway_admin_session", Name: "idx_gateway_admin_session_expires"},
		{Table: "gateway_model_route", Name: "idx_gateway_model_route_provider"},
		{Table: "gateway_model_route", Name: "uq_gateway_model_route_binding", Unique: true},
		{Table: "gateway_request_log", Name: "idx_gateway_request_log_provider_created"},
		{Table: "gateway_admin_user", Name: "idx_gateway_admin_user_username_lower", Unique: true},
	}

	return tables, uniques, fks, indexes
}

// contractUniqueKey is a (table, column-group) pair. The column group is a
// single comma-joined, sorted list so it can be compared as a string.
type contractUniqueKey struct {
	Table  string
	Column string // single column for a PK/UNIQUE on one column
}

// schemaQuerier is satisfied by both *pgx.Conn and pgx.Tx, so verification can
// run either against a bare connection or inside the bootstrap transaction
// (where the just-applied DDL is visible to the catalog view).
type schemaQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// CheckFinalSchema is the exported entry point used by callers and tests that
// want to assert the contract after the schema has been built some other way
// (for example, a test that applies the migrations and then drops an index to
// prove drift is detected).
func CheckFinalSchema(ctx context.Context, conn *pgx.Conn) error {
	return verifyFinalSchema(ctx, conn)
}

// verifyFinalSchema checks the connected database against the contract. It
// returns an error that names every missing or drifted object — table, column,
// type, nullability, unique constraint, foreign key or index — rather than the
// first one, so an operator comparing the message against their schema sees the
// whole gap at once.
func verifyFinalSchema(ctx context.Context, q schemaQuerier) error {
	tables, uniques, fks, indexes := finalSchemaContract()

	var missing []string

	// --- columns: existence, type, nullability -----------------------------
	tableNames := make([]string, 0, len(tables))
	for _, t := range tables {
		tableNames = append(tableNames, t.Name)
	}

	colRows, err := q.Query(ctx, `
		SELECT table_name, column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = ANY($1)`,
		tableNames)
	if err != nil {
		return fmt.Errorf("introspect columns: %w", err)
	}
	actualCols := map[string]map[string]contractColumn{}
	for colRows.Next() {
		var table, col, dt, nullable string
		if err := colRows.Scan(&table, &col, &dt, &nullable); err != nil {
			colRows.Close()
			return fmt.Errorf("scan column: %w", err)
		}
		if actualCols[table] == nil {
			actualCols[table] = map[string]contractColumn{}
		}
		actualCols[table][col] = contractColumn{
			Name:     col,
			DataType: dt,
			NotNull:  nullable == "NO",
		}
	}
	colRows.Close()

	for _, t := range tables {
		got, ok := actualCols[t.Name]
		if !ok {
			missing = append(missing, fmt.Sprintf("table %s: missing (expected present, actual absent)", t.Name))
			continue
		}
		for _, want := range t.Columns {
			ac, present := got[want.Name]
			if !present {
				missing = append(missing, fmt.Sprintf(
					"%s.%s: missing column (expected present, actual absent)", t.Name, want.Name))
				continue
			}
			if !sameType(ac.DataType, want.DataType) {
				missing = append(missing, fmt.Sprintf(
					"table %s column %s: expected type %s, actual %s",
					t.Name, want.Name, want.DataType, ac.DataType))
			}
			if ac.NotNull != want.NotNull {
				missing = append(missing, fmt.Sprintf(
					"table %s column %s: expected %s, actual %s",
					t.Name, want.Name,
					nullability(want.NotNull), nullability(ac.NotNull)))
			}
		}
	}

	// --- primary key / UNIQUE constraints (pg_constraint) ------------------
	uniRows, err := q.Query(ctx, `
		SELECT c.conrelid::regclass::text AS table_name,
		       array_agg(a.attname ORDER BY u.ord)::text[] AS cols
		FROM pg_constraint c
		JOIN LATERAL unnest(c.conkey) WITH ORDINALITY u(attnum, ord) ON true
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = u.attnum
		WHERE c.contype IN ('p','u') AND c.conrelid::regclass::text = ANY($1)
		GROUP BY c.oid, c.conrelid`,
		tableNames)
	if err != nil {
		return fmt.Errorf("introspect unique constraints: %w", err)
	}
	actualUnique := map[string]map[string]bool{}
	for uniRows.Next() {
		var table string
		var cols []string
		if err := uniRows.Scan(&table, &cols); err != nil {
			uniRows.Close()
			return fmt.Errorf("scan unique constraint: %w", err)
		}
		if actualUnique[table] == nil {
			actualUnique[table] = map[string]bool{}
		}
		actualUnique[table][canonicalGroup(cols)] = true
	}
	uniRows.Close()

	for _, u := range uniques {
		if !actualUnique[u.Table][u.Column] {
			missing = append(missing, fmt.Sprintf(
				"table %s: missing unique/primary key on (%s)", u.Table, u.Column))
		}
	}

	// The admin username index is a functional UNIQUE index on lower(username);
	// it is not a pg_constraint row, so it is verified from pg_indexes.
	funcRows, err := q.Query(ctx, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND tablename = 'gateway_admin_user'
		  AND indexdef ILIKE '%unique%'
		  AND indexdef ILIKE '%lower(%'
		  AND indexdef ILIKE '%username%'`)
	if err != nil {
		return fmt.Errorf("introspect functional unique index: %w", err)
	}
	hasLower := false
	for funcRows.Next() {
		var n string
		if err := funcRows.Scan(&n); err != nil {
			funcRows.Close()
			return fmt.Errorf("scan functional unique index: %w", err)
		}
		if n != "" {
			hasLower = true
		}
	}
	funcRows.Close()
	if !hasLower {
		missing = append(missing, "table gateway_admin_user: missing unique index on lower(username)")
	}

	// --- foreign keys ------------------------------------------------------
	fkRows, err := q.Query(ctx, `
		SELECT c.conrelid::regclass::text AS table_name,
		       a.attname AS column_name,
		       c.confrelid::regclass::text AS ref_table
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f' AND c.conrelid::regclass::text = ANY($1)`,
		tableNames)
	if err != nil {
		return fmt.Errorf("introspect foreign keys: %w", err)
	}
	actualFK := map[string]map[string]string{} // table -> column -> refTable
	for fkRows.Next() {
		var table, col, ref string
		if err := fkRows.Scan(&table, &col, &ref); err != nil {
			fkRows.Close()
			return fmt.Errorf("scan foreign key: %w", err)
		}
		if actualFK[table] == nil {
			actualFK[table] = map[string]string{}
		}
		actualFK[table][col] = ref
	}
	fkRows.Close()

	for _, f := range fks {
		ref, ok := actualFK[f.Table][f.Column]
		if !ok {
			missing = append(missing, fmt.Sprintf(
				"table %s: missing foreign key %s -> %s", f.Table, f.Column, f.RefTable))
			continue
		}
		if ref != f.RefTable {
			missing = append(missing, fmt.Sprintf(
				"table %s foreign key %s: expected reference to %s, actual %s",
				f.Table, f.Column, f.RefTable, ref))
		}
	}

	// --- named indexes -----------------------------------------------------
	idxNames := make([]string, 0, len(indexes))
	for _, i := range indexes {
		idxNames = append(idxNames, i.Name)
	}
	idxRows, err := q.Query(ctx, `
		SELECT tablename, indexname, indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema() AND indexname = ANY($1)`,
		idxNames)
	if err != nil {
		return fmt.Errorf("introspect indexes: %w", err)
	}
	actualIdx := map[string]struct {
		table string
		def   string
	}{}
	for idxRows.Next() {
		var table, name, def string
		if err := idxRows.Scan(&table, &name, &def); err != nil {
			idxRows.Close()
			return fmt.Errorf("scan index: %w", err)
		}
		actualIdx[name] = struct {
			table string
			def   string
		}{table, def}
	}
	idxRows.Close()

	for _, i := range indexes {
		got, ok := actualIdx[i.Name]
		if !ok {
			missing = append(missing, fmt.Sprintf("missing index %s on %s", i.Name, i.Table))
			continue
		}
		if got.table != i.Table {
			missing = append(missing, fmt.Sprintf(
				"index %s: expected on table %s, actual %s", i.Name, i.Table, got.table))
		}
		if i.Unique && !strings.Contains(strings.ToUpper(got.def), "UNIQUE") {
			missing = append(missing, fmt.Sprintf("index %s: expected to enforce uniqueness", i.Name))
		}
	}

	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("final schema contract not satisfied: %s", strings.Join(missing, "; "))
}

// sameType reports whether two information_schema data types describe the same
// column type. Only base types are compared; length/precision differences are
// deliberate to avoid false failures from cosmetic differences, while still
// catching the security-relevant cases (e.g. bigint vs text).
func sameType(actual, want string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		switch s {
		case "character varying", "varchar":
			return "varchar"
		case "timestamp with time zone", "timestamptz":
			return "timestamptz"
		case "timestamp without time zone", "timestamp":
			return "timestamp"
		case "int", "integer":
			return "integer"
		case "int2", "smallint":
			return "smallint"
		case "int8", "bigint":
			return "bigint"
		case "bool", "boolean":
			return "boolean"
		}
		return s
	}
	return norm(actual) == norm(want)
}

func nullability(notNull bool) string {
	if notNull {
		return "NOT NULL"
	}
	return "nullable"
}

// canonicalGroup sorts and joins column names so a constraint's columns match
// regardless of their declared order.
func canonicalGroup(cols []string) string {
	c := make([]string, len(cols))
	copy(c, cols)
	sort.Strings(c)
	return strings.Join(c, ",")
}
