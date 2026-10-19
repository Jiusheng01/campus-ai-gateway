package storage

import (
	"context"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// TestDailySettledQuotaAggregatesSettledRequestLogByTenantAndDay checks the
// aggregation a Quota Counter rebuild feeds on: one row per (tenant, UTC day)
// with the settled request count and token sum, bucketed exactly like the
// limiter's day keys.
//
// It runs against a real Postgres because grouping by a UTC day boundary is a
// property of the database — a bucketing bug here would silently file every
// request under the wrong day and the rebuild would re-arm a counter with the
// wrong baseline. It skips when no TEST_DATABASE_URL / local Postgres answers,
// matching the other request-log integration tests.
func TestDailySettledQuotaAggregatesSettledRequestLogByTenantAndDay(t *testing.T) {
	pg := testPostgres(t)
	clearRequestLogs(t, pg)
	clearTenantsAndKeys(t, pg)

	alpha := seedTenantAndKey(t, pg, "q-alpha", "q-alpha-key")
	beta := seedTenantAndKey(t, pg, "q-beta", "q-beta-key")

	// Fixed instants so the assertion does not depend on when the suite runs.
	day1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)  // 2026-10-01
	day2 := time.Date(2026, 10, 2, 23, 30, 0, 0, time.UTC) // 2026-10-02
	nextMonth := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	insertLogRows(t, pg, []logRow{
		{"q-a-d1-1", alpha, "gpt-4o", "openai", 200, 100, 0, false, day1},
		{"q-a-d1-2", alpha, "gpt-4o", "openai", 200, 200, 0, false, day1},
		{"q-a-d2-1", alpha, "gpt-4o", "openai", 200, 150, 0, false, day2},
		{"q-b-d1-1", beta, "gpt-4o", "openai", 200, 999, 0, false, day1},
		{"q-b-nm-1", beta, "gpt-4o", "openai", 200, 7, 0, false, nextMonth},
	})

	got, err := pg.DailySettledQuota(context.Background(),
		time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("DailySettledQuota: %v", err)
	}

	// Look up by (tenant, day): tenant ids are UUIDs, so ORDER BY tenant_id is
	// lexicographic, not creation order, and no test may rely on a position.
	byKey := make(map[string]model.QuotaSettledRow, len(got))
	for _, v := range got {
		byKey[v.TenantID+"|"+v.Day] = v
	}
	row := func(tenant, day string, req, tok int64) {
		t.Helper()
		v, ok := byKey[tenant+"|"+day]
		if !ok {
			t.Fatalf("no row for tenant=%s day=%s in %#v", tenant, day, got)
		}
		if v.Requests != req || v.Tokens != tok {
			t.Fatalf("row tenant=%s day=%s has requests=%d tokens=%d, want requests=%d tokens=%d", tenant, day, v.Requests, v.Tokens, req, tok)
		}
	}

	// Alpha's two day-1 requests merge into one row.
	row(alpha.tenantID, "20261001", 2, 300)
	row(alpha.tenantID, "20261002", 1, 150)
	row(beta.tenantID, "20261001", 1, 999)
	// 2026-11-01 falls on the half-open range boundary and must be excluded,
	// so the next-month row is not filed under October by accident.
	if len(byKey) != 3 {
		t.Fatalf("expected exactly 3 (tenant, day) rows, got %#v", got)
	}
}

// TestDailySettledQuotaRangeIsHalfOpen pins the boundary semantics: a row sitting
// exactly on the end of the range must not leak in, or a month-over-month
// rebuild would double-count the boundary.
func TestDailySettledQuotaRangeIsHalfOpen(t *testing.T) {
	pg := testPostgres(t)
	clearRequestLogs(t, pg)
	clearTenantsAndKeys(t, pg)

	fx := seedTenantAndKey(t, pg, "q-half", "q-half-key")
	atMidnightUTC := time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC)
	nextMonthStart := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	insertLogRows(t, pg, []logRow{
		{"q-h-1", fx, "gpt-4o", "openai", 200, 5, 0, false, atMidnightUTC},
		{"q-h-2", fx, "gpt-4o", "openai", 200, 6, 0, false, nextMonthStart},
	})

	// [2026-10-01, 2026-11-01): the first row is in, the boundary row is out.
	got, err := pg.DailySettledQuota(context.Background(),
		time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), nextMonthStart)
	if err != nil {
		t.Fatalf("DailySettledQuota: %v", err)
	}
	if len(got) != 1 || got[0].Tokens != 5 || got[0].Day != "20261031" {
		t.Fatalf("quarter boundary: got %#v, want just the pre-boundary row (5 tokens on 20261031)", got)
	}
}
