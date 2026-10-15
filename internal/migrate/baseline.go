package migrate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// The baseline is the contract that lets the runner tell "this database is
// old but complete" apart from "this database drifted, and recording it as
// migrated would be a lie".
//
// It exists because of the bootstrap path: a database with no schema_migrations
// table is either empty or a legacy deployment. The migrations are replay-safe,
// so one run brings a legacy schema forward — but "the statements did not
// error" is not proof the schema reached the end state. A table can exist with
// a different shape (created by hand, or by a version this repository never
// shipped), and CREATE ... IF NOT EXISTS cannot see that. Before the bootstrap
// records anything, it checks that the objects the current migration set is
// supposed to leave behind are actually there.
//
// Maintenance rule: when a migration adds a table the runtime reads, or adds a
// column a later migration's markers depend on, extend these lists. They are
// deliberately small — a check of everything would be a schema diff, which is
// a different tool — but they cover one object per migration plus the
// distinctive late-history columns, which is what "the replay ran all the way
// forward" means in practice.

// baselineTables lists one table per migration in the set, spanning 0001 to
// 0008. The latest migration (0009) is covered by its column marker below,
// because it adds columns rather than a table.
var baselineTables = []string{
	"gateway_tenant",          // 0001
	"gateway_api_key",         // 0001
	"gateway_request_log",     // 0001
	"gateway_provider",        // 0002
	"gateway_model",           // 0002
	"gateway_model_route",     // 0003
	"gateway_security_event",  // 0005
	"gateway_admin_audit_log", // 0005
	"gateway_admin_user",      // 0007
	"gateway_admin_session",   // 0008
}

// baselineColumns pins columns that only a specific migration adds. A table
// existing with an older shape — created by hand, truncated by an incident, or
// left behind by a partial history — is the drift this catches: the table
// itself would pass an existence check.
var baselineColumns = map[string][]string{
	"gateway_request_log":   {"upstream_model", "failover"},        // 0004
	"gateway_tenant":        {"code", "name", "daily_token_limit"}, // 0001 + 0006
	"gateway_admin_user":    {"username", "auth_version"},          // 0007
	"gateway_admin_session": {"previous_refresh_hash"},             // 0009
}

// hasApplicationTables reports whether the database carries any table of the
// migration set. That is what distinguishes a legacy database — business tables
// present, bookkeeping absent — from an empty one.
func hasApplicationTables(ctx context.Context, conn *pgx.Conn) (bool, error) {
	var count int
	if err := conn.QueryRow(ctx,
		`SELECT COUNT(*) FROM information_schema.tables
		 WHERE table_schema = current_schema() AND table_name = ANY($1)`,
		baselineTables).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect existing tables: %w", err)
	}
	return count > 0, nil
}

// verifyBaseline checks that the database carries the end state the current
// migration set is expected to produce.
//
// It returns an error naming every missing object rather than the first one:
// an operator comparing the message against their schema should see the whole
// gap at once. The error is deliberately not wrapped in ErrChecksumMismatch —
// nothing about the files is wrong; the database is.
func verifyBaseline(ctx context.Context, conn *pgx.Conn) error {
	rows, err := conn.Query(ctx,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = current_schema() AND table_name = ANY($1)`,
		baselineTables)
	if err != nil {
		return fmt.Errorf("inspect baseline tables: %w", err)
	}
	present := make(map[string]bool, len(baselineTables))
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("scan baseline tables: %w", err)
		}
		present[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate baseline tables: %w", err)
	}

	var missingTables []string
	for _, name := range baselineTables {
		if !present[name] {
			missingTables = append(missingTables, name)
		}
	}

	var missingColumns []string
	for table, columns := range baselineColumns {
		for _, column := range columns {
			var exists bool
			if err := conn.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM information_schema.columns
					WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2)`,
				table, column).Scan(&exists); err != nil {
				return fmt.Errorf("inspect %s.%s: %w", table, column, err)
			}
			if !exists {
				missingColumns = append(missingColumns, table+"."+column)
			}
		}
	}

	if len(missingTables) == 0 && len(missingColumns) == 0 {
		return nil
	}
	msg := "database schema did not reach the expected baseline"
	if len(missingTables) > 0 {
		msg += "; missing tables: " + join(missingTables)
	}
	if len(missingColumns) > 0 {
		msg += "; missing columns: " + join(missingColumns)
	}
	return fmt.Errorf("%s", msg)
}

func join(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ", "
		}
		out += item
	}
	return out
}
