package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestTenantUsageStatsBucketsTheDayInUTCNotTheSessionZone pins the fix for a
// boundary bug. The quota numerator the console shows was derived with
// date_trunc('day'|'month', NOW()), which the database evaluates in the session's
// TimeZone, while the limiter's counters and a Quota Counter rebuild bucket on
// UTC. A deployment whose Postgres runs east or west of UTC therefore reported a
// "today" offset from the window the gate actually enforced.
//
// The pool is opened with a non-UTC session TimeZone — and the test asserts it
// really is non-UTC, so it cannot pass vacuously — then rows are placed densely
// around both the UTC day and the UTC month boundaries. A session-zone boundary
// would count a visibly different set; the expectation is a reference query
// whose boundary is spelled out in UTC.
func TestTenantUsageStatsBucketsTheDayInUTCNotTheSessionZone(t *testing.T) {
	ctx := context.Background()
	// Etc/GMT+8 is UTC-8 (POSIX inverts the sign) and has no DST, so the session
	// boundary sits a fixed eight hours off the UTC one.
	pool := timezoneTestPool(t, "Etc/GMT+8")
	pg := &Postgres{Pool: pool}

	var offsetSeconds int
	if err := pool.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM (NOW() - NOW() AT TIME ZONE 'UTC'))::int`).Scan(&offsetSeconds); err != nil {
		t.Fatalf("read the session offset: %v", err)
	}
	if offsetSeconds == 0 {
		t.Fatal("the session TimeZone is UTC, so the test could not tell a session-zone boundary from a UTC one")
	}

	clearTenantsAndKeys(t, pg)
	fx := seedTenantAndKey(t, pg, "utc-bucket", "utc-bucket-key")

	now := time.Now().UTC()
	utcDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	utcMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	// One row an hour, straddling each boundary by a wide margin, so whichever
	// way the session zone is offset its boundary falls inside the set with rows
	// on both sides.
	rows := append(boundaryRows(fx, "utc-day", utcDay), boundaryRows(fx, "utc-month", utcMonth)...)
	insertLogRows(t, pg, rows)

	got, err := pg.TenantUsageStats(ctx, fx.tenantID)
	if err != nil {
		t.Fatalf("TenantUsageStats: %v", err)
	}

	// The same windows, spelled out in UTC, as the reference.
	var wantDailyReqs, wantDailyTokens, wantMonthlyReqs, wantMonthlyTokens int64
	if err := pool.QueryRow(ctx, `
SELECT
	COUNT(*) FILTER (WHERE created_at >= date_trunc('day',   NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::bigint,
	COALESCE(SUM(COALESCE(total_tokens,0)) FILTER (WHERE created_at >= date_trunc('day',   NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),0)::bigint,
	COUNT(*) FILTER (WHERE created_at >= date_trunc('month', NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::bigint,
	COALESCE(SUM(COALESCE(total_tokens,0)) FILTER (WHERE created_at >= date_trunc('month', NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),0)::bigint
FROM gateway_request_log
WHERE tenant_id=$1::uuid`, fx.tenantID,
	).Scan(&wantDailyReqs, &wantDailyTokens, &wantMonthlyReqs, &wantMonthlyTokens); err != nil {
		t.Fatalf("reference query: %v", err)
	}

	if got.DailyRequests != wantDailyReqs || got.DailyTokens != wantDailyTokens {
		t.Fatalf("daily = %d requests / %d tokens, want %d / %d (the UTC day window, not the session zone's)",
			got.DailyRequests, got.DailyTokens, wantDailyReqs, wantDailyTokens)
	}
	if got.MonthlyRequests != wantMonthlyReqs || got.MonthlyTokens != wantMonthlyTokens {
		t.Fatalf("monthly = %d requests / %d tokens, want %d / %d (the UTC month window, not the session zone's)",
			got.MonthlyRequests, got.MonthlyTokens, wantMonthlyReqs, wantMonthlyTokens)
	}
}

// boundaryRows returns 33 rows, one an hour, spanning sixteen hours either side
// of center, each just past the half hour so no row lands exactly on a whole-hour
// boundary the database might truncate to.
func boundaryRows(fx logFixture, prefix string, center time.Time) []logRow {
	rows := make([]logRow, 0, 33)
	for k := -16; k <= 16; k++ {
		rows = append(rows, logRow{
			requestID: fmt.Sprintf("%s-%+d", prefix, k),
			fixture:   fx,
			model:     "gpt-4o",
			provider:  "openai",
			status:    200,
			tokens:    100,
			createdAt: center.Add(time.Duration(k)*time.Hour + 30*time.Minute),
		})
	}
	return rows
}

// timezoneTestPool opens a pool against the migrated test database whose session
// TimeZone is tz. A zone the server does not recognise falls back to UTC, so the
// caller asserts the offset it actually got rather than trusting the name.
func timezoneTestPool(t *testing.T, tz string) *pgxpool.Pool {
	t.Helper()
	url := testDatabaseURL()
	schemaOnce.Do(func() { schemaErr = buildTestDatabase(url) })
	if schemaErr != nil {
		t.Skipf("test PostgreSQL unavailable: %v (set TEST_DATABASE_URL to run the integration tests)", schemaErr)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse the test database URL: %v", err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = tz
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Skipf("test PostgreSQL unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
