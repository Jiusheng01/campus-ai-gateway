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
// is supposed to leave behind, derived by hand from migrations/0001..0010. It
// covers, per table: every column with its PostgreSQL data type, bounded length,
// nullability and default; each primary key, unique constraint, foreign key and
// CHECK definition; and every named index including uniqueness, key expression
// and sort direction. It deliberately ignores only identity details such as a
// constraint's auto-generated OID.
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
	Table      string
	Column     string
	RefTable   string
	RefColumn  string
	DeleteCode string // pg_constraint.confdeltype: a=no action, c=cascade
}

// contractIndex is one expected named index the runtime relies on. Unique is
// true when the index must also enforce uniqueness (a CREATE UNIQUE INDEX that
// is not backed by a pg_constraint row, e.g. a composite or functional unique
// index).
type contractIndex struct {
	Table     string
	Name      string
	Unique    bool
	Keys      []string
	CreateSQL string // test-only restoration of the exact migration definition
}

type contractCheck struct {
	Table      string
	Name       string
	Definition string
}

// finalSchemaContract returns the objects the current migration set must leave
// behind. The data types are the exact strings PostgreSQL reports in
// information_schema.columns.data_type.
func finalSchemaContract() (tables []contractTable, keys []contractKey, fks []contractFK, indexes []contractIndex, checks []contractCheck) {
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
	keys = []contractKey{
		{"gateway_tenant", "id", "p"},
		{"gateway_tenant", "code", "u"},
		{"gateway_api_key", "id", "p"},
		{"gateway_api_key", "key_digest", "u"},
		{"gateway_request_log", "id", "p"},
		{"gateway_request_log", "request_id", "u"},
		{"gateway_provider", "id", "p"},
		{"gateway_provider", "name", "u"},
		{"gateway_model", "id", "p"},
		{"gateway_model", "alias", "u"},
		{"gateway_model_route", "id", "p"},
		{"gateway_admin_audit_log", "id", "p"},
		{"gateway_security_event", "id", "p"},
		{"gateway_admin_user", "id", "p"},
		{"gateway_admin_session", "sid", "p"},
	}

	fks = []contractFK{
		{"gateway_api_key", "tenant_id", "gateway_tenant", "id", "a"},
		{"gateway_model", "provider_id", "gateway_provider", "id", "a"},
		{"gateway_model_route", "model_id", "gateway_model", "id", "c"},
		{"gateway_model_route", "provider_id", "gateway_provider", "id", "a"},
		{"gateway_admin_session", "user_id", "gateway_admin_user", "id", "c"},
	}

	indexes = []contractIndex{
		{"gateway_api_key", "idx_gateway_api_key_tenant", false, []string{"tenant_id"}, `CREATE INDEX idx_gateway_api_key_tenant ON gateway_api_key(tenant_id)`},
		{"gateway_request_log", "idx_request_log_created", false, []string{"created_at DESC"}, `CREATE INDEX idx_request_log_created ON gateway_request_log(created_at DESC)`},
		{"gateway_model", "idx_gateway_model_provider", false, []string{"provider_id"}, `CREATE INDEX idx_gateway_model_provider ON gateway_model(provider_id)`},
		{"gateway_provider", "idx_gateway_provider_status", false, []string{"status"}, `CREATE INDEX idx_gateway_provider_status ON gateway_provider(status)`},
		{"gateway_model", "idx_gateway_model_status", false, []string{"status"}, `CREATE INDEX idx_gateway_model_status ON gateway_model(status)`},
		{"gateway_model_route", "uq_gateway_model_route_binding", true, []string{"model_id", "provider_id", "upstream_model"}, `CREATE UNIQUE INDEX uq_gateway_model_route_binding ON gateway_model_route(model_id, provider_id, upstream_model)`},
		{"gateway_model_route", "idx_gateway_model_route_model", false, []string{"model_id", "priority", "status"}, `CREATE INDEX idx_gateway_model_route_model ON gateway_model_route(model_id, priority, status)`},
		{"gateway_model_route", "idx_gateway_model_route_provider", false, []string{"provider_id"}, `CREATE INDEX idx_gateway_model_route_provider ON gateway_model_route(provider_id)`},
		{"gateway_request_log", "idx_gateway_request_log_provider_created", false, []string{"provider", "created_at DESC"}, `CREATE INDEX idx_gateway_request_log_provider_created ON gateway_request_log(provider, created_at DESC)`},
		{"gateway_request_log", "idx_gateway_request_log_failover_created", false, []string{"failover", "created_at DESC"}, `CREATE INDEX idx_gateway_request_log_failover_created ON gateway_request_log(failover, created_at DESC)`},
		{"gateway_request_log", "idx_gateway_request_log_tenant_created", false, []string{"tenant_id", "created_at DESC"}, `CREATE INDEX idx_gateway_request_log_tenant_created ON gateway_request_log(tenant_id, created_at DESC)`},
		{"gateway_request_log", "idx_gateway_request_log_api_key_created", false, []string{"api_key_id", "created_at DESC"}, `CREATE INDEX idx_gateway_request_log_api_key_created ON gateway_request_log(api_key_id, created_at DESC)`},
		{"gateway_admin_audit_log", "idx_gateway_admin_audit_created", false, []string{"created_at DESC"}, `CREATE INDEX idx_gateway_admin_audit_created ON gateway_admin_audit_log(created_at DESC)`},
		{"gateway_admin_audit_log", "idx_gateway_admin_audit_actor_created", false, []string{"actor", "created_at DESC"}, `CREATE INDEX idx_gateway_admin_audit_actor_created ON gateway_admin_audit_log(actor, created_at DESC)`},
		{"gateway_security_event", "idx_gateway_security_event_created", false, []string{"created_at DESC"}, `CREATE INDEX idx_gateway_security_event_created ON gateway_security_event(created_at DESC)`},
		{"gateway_security_event", "idx_gateway_security_event_type_created", false, []string{"event_type", "created_at DESC"}, `CREATE INDEX idx_gateway_security_event_type_created ON gateway_security_event(event_type, created_at DESC)`},
		{"gateway_admin_user", "idx_gateway_admin_user_username_lower", true, []string{"lower(username::text)"}, `CREATE UNIQUE INDEX idx_gateway_admin_user_username_lower ON gateway_admin_user(lower(username))`},
		{"gateway_admin_user", "idx_gateway_admin_user_status", false, []string{"status"}, `CREATE INDEX idx_gateway_admin_user_status ON gateway_admin_user(status)`},
		{"gateway_admin_session", "idx_gateway_admin_session_user", false, []string{"user_id", "created_at DESC"}, `CREATE INDEX idx_gateway_admin_session_user ON gateway_admin_session(user_id, created_at DESC)`},
		{"gateway_admin_session", "idx_gateway_admin_session_expires", false, []string{"expires_at"}, `CREATE INDEX idx_gateway_admin_session_expires ON gateway_admin_session(expires_at)`},
	}

	checks = []contractCheck{
		{"gateway_model_route", "gateway_model_route_priority_chk", "CHECK (priority >= 1)"},
		{"gateway_model_route", "gateway_model_route_weight_chk", "CHECK (weight >= 0)"},
		{"gateway_model_route", "gateway_model_route_timeout_chk", "CHECK (timeout_ms > 0)"},
	}

	return tables, keys, fks, indexes, checks
}

// finalColumnLengths pins every bounded character column. A bare VARCHAR and a
// VARCHAR(1) both report data_type=character varying, but neither is equivalent
// to the migration's VARCHAR(64) contract.
func finalColumnLengths() map[string]int {
	return map[string]int{
		"gateway_tenant.code": 64, "gateway_tenant.name": 128,
		"gateway_api_key.name": 128, "gateway_api_key.key_prefix": 32, "gateway_api_key.key_digest": 128, "gateway_api_key.route_group": 64,
		"gateway_request_log.request_id": 64, "gateway_request_log.client_ip": 64, "gateway_request_log.requested_model": 128,
		"gateway_request_log.actual_model": 128, "gateway_request_log.provider": 128, "gateway_request_log.error_code": 64,
		"gateway_request_log.upstream_model": 128,
		"gateway_provider.name":              128, "gateway_provider.protocol": 32, "gateway_provider.credential_env": 128,
		"gateway_model.alias": 128, "gateway_model.upstream_model": 128,
		"gateway_model_route.upstream_model": 128,
		"gateway_admin_audit_log.request_id": 64, "gateway_admin_audit_log.actor": 128, "gateway_admin_audit_log.action": 64,
		"gateway_admin_audit_log.resource": 256, "gateway_admin_audit_log.client_ip": 64,
		"gateway_security_event.request_id": 64, "gateway_security_event.event_type": 64, "gateway_security_event.client_ip": 64,
		"gateway_security_event.requested_model": 128, "gateway_security_event.detail": 512,
		"gateway_admin_user.username": 64, "gateway_admin_user.password_hash": 255, "gateway_admin_user.display_name": 128,
		"gateway_admin_session.refresh_hash": 128, "gateway_admin_session.login_method": 32, "gateway_admin_session.ip": 64,
		"gateway_admin_session.user_agent": 256, "gateway_admin_session.revoke_reason": 64,
		"gateway_admin_session.previous_refresh_hash": 128,
	}
}

// finalColumnDefaults includes every default declared by migrations 0001-0009.
// Columns absent from this map are required to have no default. This matters for
// INSERTs that intentionally omit status, auth_version, timestamps and other
// server-owned fields.
func finalColumnDefaults() map[string]string {
	return map[string]string{
		"gateway_tenant.id": "gen_random_uuid()", "gateway_tenant.status": "1",
		"gateway_tenant.created_at": "now()", "gateway_tenant.updated_at": "now()",
		"gateway_tenant.daily_token_limit": "0", "gateway_tenant.monthly_token_limit": "0",
		"gateway_tenant.monthly_request_limit": "0", "gateway_tenant.monthly_budget_micros": "0",

		"gateway_api_key.id": "gen_random_uuid()", "gateway_api_key.status": "1",
		"gateway_api_key.allowed_models": "'[]'::jsonb", "gateway_api_key.allowed_ips": "'[]'::jsonb",
		"gateway_api_key.rpm_limit": "60", "gateway_api_key.tpm_limit": "100000",
		"gateway_api_key.max_concurrency": "10", "gateway_api_key.created_at": "now()", "gateway_api_key.updated_at": "now()",

		"gateway_request_log.id":     "nextval('gateway_request_log_id_seq'::regclass)",
		"gateway_request_log.stream": "false", "gateway_request_log.input_tokens": "0",
		"gateway_request_log.output_tokens": "0", "gateway_request_log.total_tokens": "0",
		"gateway_request_log.latency_ms": "0", "gateway_request_log.created_at": "now()",

		"gateway_provider.id": "gen_random_uuid()", "gateway_provider.protocol": "'openai-chat'::character varying",
		"gateway_provider.status": "1", "gateway_provider.created_at": "now()", "gateway_provider.updated_at": "now()",
		"gateway_model.id": "gen_random_uuid()", "gateway_model.status": "1",
		"gateway_model.created_at": "now()", "gateway_model.updated_at": "now()",

		"gateway_model_route.id": "gen_random_uuid()", "gateway_model_route.priority": "1",
		"gateway_model_route.weight": "100", "gateway_model_route.timeout_ms": "60000",
		"gateway_model_route.status": "1", "gateway_model_route.created_at": "now()", "gateway_model_route.updated_at": "now()",

		"gateway_admin_audit_log.id":    "nextval('gateway_admin_audit_log_id_seq'::regclass)",
		"gateway_admin_audit_log.actor": "''::character varying", "gateway_admin_audit_log.resource": "''::character varying",
		"gateway_admin_audit_log.status_code": "0", "gateway_admin_audit_log.created_at": "now()",
		"gateway_security_event.id":         "nextval('gateway_security_event_id_seq'::regclass)",
		"gateway_security_event.created_at": "now()",

		"gateway_admin_user.id": "gen_random_uuid()", "gateway_admin_user.display_name": "''::character varying",
		"gateway_admin_user.roles": "'[\"auditor\"]'::jsonb", "gateway_admin_user.status": "1",
		"gateway_admin_user.auth_version": "1", "gateway_admin_user.created_at": "now()", "gateway_admin_user.updated_at": "now()",

		"gateway_admin_session.sid": "gen_random_uuid()", "gateway_admin_session.version": "1",
		"gateway_admin_session.user_auth_version": "1", "gateway_admin_session.status": "1",
		"gateway_admin_session.login_method": "'password'::character varying",
		"gateway_admin_session.ip":           "''::character varying", "gateway_admin_session.user_agent": "''::character varying",
		"gateway_admin_session.created_at": "now()", "gateway_admin_session.last_active_at": "now()",
		"gateway_admin_session.revoke_reason":         "''::character varying",
		"gateway_admin_session.previous_refresh_hash": "''::character varying",
	}
}

// contractKey distinguishes PRIMARY KEY from UNIQUE. Treating both as merely
// unique columns lets a weaker UNIQUE constraint impersonate a primary key.
type contractKey struct {
	Table  string
	Column string
	Kind   string // PostgreSQL pg_constraint.contype: p or u
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
	tables, keys, fks, indexes, checks := finalSchemaContract()
	lengths := finalColumnLengths()
	defaults := finalColumnDefaults()

	var missing []string

	// --- columns: existence, type, nullability -----------------------------
	tableNames := make([]string, 0, len(tables))
	for _, t := range tables {
		tableNames = append(tableNames, t.Name)
	}

	colRows, err := q.Query(ctx, `
		SELECT table_name, column_name, data_type, is_nullable,
		       COALESCE(character_maximum_length, 0), COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = ANY($1)`,
		tableNames)
	if err != nil {
		return fmt.Errorf("introspect columns: %w", err)
	}
	type actualColumn struct {
		contractColumn
		MaxLength int
		Default   string
	}
	actualCols := map[string]map[string]actualColumn{}
	for colRows.Next() {
		var table, col, dt, nullable, defaultExpr string
		var maxLength int
		if err := colRows.Scan(&table, &col, &dt, &nullable, &maxLength, &defaultExpr); err != nil {
			colRows.Close()
			return fmt.Errorf("scan column: %w", err)
		}
		if actualCols[table] == nil {
			actualCols[table] = map[string]actualColumn{}
		}
		actualCols[table][col] = actualColumn{
			contractColumn: contractColumn{Name: col, DataType: dt, NotNull: nullable == "NO"},
			MaxLength:      maxLength,
			Default:        defaultExpr,
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
			object := t.Name + "." + want.Name
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
			if wantLength := lengths[object]; ac.MaxLength != wantLength {
				missing = append(missing, fmt.Sprintf(
					"table %s column %s: expected character length %d, actual %d",
					t.Name, want.Name, wantLength, ac.MaxLength))
			}
			if wantDefault := defaults[object]; !sameDefault(ac.Default, wantDefault) {
				missing = append(missing, fmt.Sprintf(
					"table %s column %s: expected default %s, actual %s",
					t.Name, want.Name, displayDefault(wantDefault), displayDefault(ac.Default)))
			}
		}
	}

	// --- primary key / UNIQUE constraints (pg_constraint) ------------------
	uniRows, err := q.Query(ctx, `
		SELECT c.conrelid::regclass::text AS table_name, c.contype::text,
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
	actualKeys := map[string]map[string]map[string]bool{} // table -> kind -> columns
	for uniRows.Next() {
		var table, kind string
		var cols []string
		if err := uniRows.Scan(&table, &kind, &cols); err != nil {
			uniRows.Close()
			return fmt.Errorf("scan unique constraint: %w", err)
		}
		if actualKeys[table] == nil {
			actualKeys[table] = map[string]map[string]bool{}
		}
		if actualKeys[table][kind] == nil {
			actualKeys[table][kind] = map[string]bool{}
		}
		actualKeys[table][kind][canonicalGroup(cols)] = true
	}
	uniRows.Close()

	for _, k := range keys {
		if !actualKeys[k.Table][k.Kind][k.Column] {
			kind := "unique constraint"
			if k.Kind == "p" {
				kind = "primary key"
			}
			missing = append(missing, fmt.Sprintf(
				"table %s: missing %s on (%s)", k.Table, kind, k.Column))
		}
	}

	// --- foreign keys ------------------------------------------------------
	fkRows, err := q.Query(ctx, `
		SELECT c.conrelid::regclass::text AS table_name,
		       src.attname AS column_name,
		       c.confrelid::regclass::text AS ref_table,
		       dst.attname AS ref_column,
		       c.confdeltype::text
		FROM pg_constraint c
		JOIN pg_attribute src ON src.attrelid = c.conrelid AND src.attnum = c.conkey[1]
		JOIN pg_attribute dst ON dst.attrelid = c.confrelid AND dst.attnum = c.confkey[1]
		WHERE c.contype = 'f' AND c.conrelid::regclass::text = ANY($1)`,
		tableNames)
	if err != nil {
		return fmt.Errorf("introspect foreign keys: %w", err)
	}
	type actualForeignKey struct{ refTable, refColumn, deleteCode string }
	actualFK := map[string]map[string]actualForeignKey{}
	for fkRows.Next() {
		var table, col, refTable, refColumn, deleteCode string
		if err := fkRows.Scan(&table, &col, &refTable, &refColumn, &deleteCode); err != nil {
			fkRows.Close()
			return fmt.Errorf("scan foreign key: %w", err)
		}
		if actualFK[table] == nil {
			actualFK[table] = map[string]actualForeignKey{}
		}
		actualFK[table][col] = actualForeignKey{refTable, refColumn, deleteCode}
	}
	fkRows.Close()

	for _, f := range fks {
		actual, ok := actualFK[f.Table][f.Column]
		if !ok {
			missing = append(missing, fmt.Sprintf(
				"table %s: missing foreign key %s -> %s(%s) ON DELETE %s",
				f.Table, f.Column, f.RefTable, f.RefColumn, deleteAction(f.DeleteCode)))
			continue
		}
		if actual.refTable != f.RefTable || actual.refColumn != f.RefColumn || actual.deleteCode != f.DeleteCode {
			missing = append(missing, fmt.Sprintf(
				"table %s foreign key %s: expected reference to %s(%s) ON DELETE %s, actual %s(%s) ON DELETE %s",
				f.Table, f.Column, f.RefTable, f.RefColumn, deleteAction(f.DeleteCode),
				actual.refTable, actual.refColumn, deleteAction(actual.deleteCode)))
		}
	}

	// --- named indexes -----------------------------------------------------
	idxNames := make([]string, 0, len(indexes))
	for _, i := range indexes {
		idxNames = append(idxNames, i.Name)
	}
	idxRows, err := q.Query(ctx, `
		SELECT tbl.relname, idx.relname, pi.indisunique,
		       array_agg(
		         pg_get_indexdef(pi.indexrelid, ord, true) ||
		         CASE WHEN (pi.indoption[ord-1] & 1) = 1 THEN ' DESC' ELSE '' END
		         ORDER BY ord
		       )::text[]
		FROM pg_index pi
		JOIN pg_class idx ON idx.oid = pi.indexrelid
		JOIN pg_class tbl ON tbl.oid = pi.indrelid
		JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
		JOIN LATERAL generate_series(1, pi.indnkeyatts) ord ON true
		WHERE ns.nspname = current_schema() AND idx.relname = ANY($1)
		GROUP BY tbl.relname, idx.relname, pi.indisunique`,
		idxNames)
	if err != nil {
		return fmt.Errorf("introspect indexes: %w", err)
	}
	type actualIndex struct {
		table  string
		unique bool
		keys   []string
	}
	actualIdx := map[string]actualIndex{}
	for idxRows.Next() {
		var table, name string
		var unique bool
		var keys []string
		if err := idxRows.Scan(&table, &name, &unique, &keys); err != nil {
			idxRows.Close()
			return fmt.Errorf("scan index: %w", err)
		}
		actualIdx[name] = actualIndex{table, unique, keys}
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
		if got.unique != i.Unique {
			missing = append(missing, fmt.Sprintf("index %s: expected unique=%t, actual %t", i.Name, i.Unique, got.unique))
		}
		if !sameStringSlice(got.keys, i.Keys) {
			missing = append(missing, fmt.Sprintf("index %s: expected keys (%s), actual (%s)",
				i.Name, strings.Join(i.Keys, ", "), strings.Join(got.keys, ", ")))
		}
	}

	// --- CHECK constraints --------------------------------------------------
	checkRows, err := q.Query(ctx, `
		SELECT c.conrelid::regclass::text, c.conname, pg_get_constraintdef(c.oid, true)
		FROM pg_constraint c
		WHERE c.contype = 'c' AND c.conrelid::regclass::text = ANY($1)`, tableNames)
	if err != nil {
		return fmt.Errorf("introspect check constraints: %w", err)
	}
	actualChecks := map[string]map[string]string{}
	for checkRows.Next() {
		var table, name, definition string
		if err := checkRows.Scan(&table, &name, &definition); err != nil {
			checkRows.Close()
			return fmt.Errorf("scan check constraint: %w", err)
		}
		if actualChecks[table] == nil {
			actualChecks[table] = map[string]string{}
		}
		actualChecks[table][name] = definition
	}
	checkRows.Close()
	for _, check := range checks {
		actual, ok := actualChecks[check.Table][check.Name]
		if !ok {
			missing = append(missing, fmt.Sprintf("table %s: missing check constraint %s", check.Table, check.Name))
			continue
		}
		if normalizeSQL(actual) != normalizeSQL(check.Definition) {
			missing = append(missing, fmt.Sprintf("check constraint %s: expected %s, actual %s", check.Name, check.Definition, actual))
		}
	}

	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("final schema contract not satisfied: %s", strings.Join(missing, "; "))
}

func sameDefault(actual, want string) bool {
	return normalizeSQL(actual) == normalizeSQL(want)
}

func displayDefault(s string) string {
	if strings.TrimSpace(s) == "" {
		return "<none>"
	}
	return s
}

func deleteAction(code string) string {
	switch code {
	case "a":
		return "NO ACTION"
	case "c":
		return "CASCADE"
	case "r":
		return "RESTRICT"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	default:
		return code
	}
}

func sameStringSlice(actual, want []string) bool {
	if len(actual) != len(want) {
		return false
	}
	for i := range actual {
		if normalizeSQL(actual[i]) != normalizeSQL(want[i]) {
			return false
		}
	}
	return true
}

func normalizeSQL(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

// sameType reports whether two information_schema base type names describe the
// same column type. Length is checked separately from
// character_maximum_length, so VARCHAR(1) cannot impersonate VARCHAR(64).
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
