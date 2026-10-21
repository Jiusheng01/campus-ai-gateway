package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// The new read paths are SQL, and the properties they have to hold are
// properties of the database, not of Go code:
//
//   - that the time and identity predicates actually reach the planner as
//     typed comparisons rather than as an `''::uuid` cast that raises on the
//     first request. A Go-level test of the argument builder cannot see that.
//   - that the trend buckets land on the caller's local boundary. Bucketing in
//     UTC and bucketing in UTC+8 differ by eight hours of rows, and only a real
//     date_trunc in a real database can tell them apart.
//   - that revoking a key really overwrites the stored digest, so the
//     credential cannot be brought back by re-enabling the row.
//
// They share the migrated scratch database with the account tests: it is built
// from the real migrations and dropped on the way in, so nothing here touches
// the development database.

// logFixture is one tenant with one API key, ready for request rows to be
// attached.
type logFixture struct {
	tenantID string
	keyID    string
}

func seedTenantAndKey(t *testing.T, pg *Postgres, code, keyName string) logFixture {
	t.Helper()
	ctx := context.Background()
	tenant, err := pg.CreateTenant(ctx, model.Tenant{Code: code, Name: code, Status: 1})
	if err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	key, err := pg.CreateAPIKey(ctx, model.APIKeyAdmin{
		TenantID: tenant.ID,
		Name:     keyName,
		KeyPrefix: "campus_" + code,
		Status:   1,
	}, "digest-"+code+"-"+keyName)
	if err != nil {
		t.Fatalf("create key %s: %v", keyName, err)
	}
	return logFixture{tenantID: tenant.ID, keyID: key.ID}
}

// logRow is one gateway_request_log insert.
type logRow struct {
	requestID string
	fixture   logFixture
	model     string
	provider  string
	status    int
	tokens    int64
	latency   int64
	failover  bool
	createdAt time.Time
}

func insertLogRows(t *testing.T, pg *Postgres, rows []logRow) {
	t.Helper()
	ctx := context.Background()
	for _, r := range rows {
		_, err := pg.Pool.Exec(ctx, `
INSERT INTO gateway_request_log(
	request_id, tenant_id, api_key_id, requested_model, actual_model, provider,
	stream, input_tokens, output_tokens, total_tokens, latency_ms, status_code,
	failover, created_at
) VALUES ($1,$2::uuid,$3::uuid,$4,$4,$5,false,$6,0,$6,$7,$8,$9,$10)`,
			r.requestID, r.fixture.tenantID, r.fixture.keyID, r.model, r.provider,
			r.tokens, r.latency, r.status, r.failover, r.createdAt)
		if err != nil {
			t.Fatalf("insert log %s: %v", r.requestID, err)
		}
	}
}

func clearRequestLogs(t *testing.T, pg *Postgres) {
	t.Helper()
	if _, err := pg.Pool.Exec(context.Background(), `DELETE FROM gateway_request_log`); err != nil {
		t.Fatalf("clear request logs: %v", err)
	}
}

// TestSearchRequestLogsNarrowsByTenantKeyAndTime covers the three filters the
// console gained. Each is asserted by counting rows that must survive and rows
// that must not: a filter that silently matched everything would still return a
// non-empty page, so "it returned rows" proves nothing.
func TestSearchRequestLogsNarrowsByTenantKeyAndTime(t *testing.T) {
	pg := testPostgres(t)
	clearRequestLogs(t, pg)
	clearTenantsAndKeys(t, pg)

	alpha := seedTenantAndKey(t, pg, "alpha", "alpha-key")
	beta := seedTenantAndKey(t, pg, "beta", "beta-key")

	base := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	insertLogRows(t, pg, []logRow{
		{"r-alpha-old", alpha, "gpt-4o", "openai", 200, 100, 120, false, base},
		{"r-alpha-new", alpha, "gpt-4o", "openai", 200, 200, 130, false, base.Add(24 * time.Hour)},
		{"r-alpha-err", alpha, "gpt-4o", "openai", 502, 50, 900, true, base.Add(25 * time.Hour)},
		{"r-beta-old", beta, "gpt-4o", "openai", 200, 400, 140, false, base},
	})

	t.Run("tenant", func(t *testing.T) {
		got, err := pg.SearchRequestLogs(context.Background(), RequestLogFilter{
			Limit: 50, TenantID: alpha.tenantID, Failover: -1,
		})
		if err != nil {
			t.Fatalf("search by tenant: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("tenant filter returned %d rows, want 3", len(got))
		}
		for _, v := range got {
			if v.TenantID != alpha.tenantID {
				t.Fatalf("tenant filter leaked %s", v.RequestID)
			}
		}
	})

	t.Run("api key", func(t *testing.T) {
		got, err := pg.SearchRequestLogs(context.Background(), RequestLogFilter{
			Limit: 50, APIKeyID: beta.keyID, Failover: -1,
		})
		if err != nil {
			t.Fatalf("search by key: %v", err)
		}
		if len(got) != 1 || got[0].RequestID != "r-beta-old" {
			t.Fatalf("key filter returned %#v, want just r-beta-old", got)
		}
	})

	t.Run("time window", func(t *testing.T) {
		// Half-open on both ends as the console sends it: from the second row's
		// instant to the third's, which must include both and exclude the first.
		got, err := pg.SearchRequestLogs(context.Background(), RequestLogFilter{
			Limit: 50, From: base.Add(24 * time.Hour), To: base.Add(25 * time.Hour), Failover: -1,
		})
		if err != nil {
			t.Fatalf("search by time: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("time filter returned %d rows, want 2", len(got))
		}
	})

	t.Run("combined with an existing filter", func(t *testing.T) {
		got, err := pg.SearchRequestLogs(context.Background(), RequestLogFilter{
			Limit: 50, TenantID: alpha.tenantID, Failover: 1, From: base.Add(20 * time.Hour),
		})
		if err != nil {
			t.Fatalf("search combined: %v", err)
		}
		if len(got) != 1 || got[0].RequestID != "r-alpha-err" {
			t.Fatalf("combined filter returned %#v, want just r-alpha-err", got)
		}
	})

	t.Run("the failover zero value means false, not any", func(t *testing.T) {
		// The trap the struct documents: leaving Failover unset narrows to
		// rows that did not fail over. Pinning it here means a future change
		// that "fixes" the sentinel cannot do so silently.
		zero, err := pg.SearchRequestLogs(context.Background(), RequestLogFilter{Limit: 50, TenantID: alpha.tenantID})
		if err != nil {
			t.Fatalf("search with the zero failover: %v", err)
		}
		if len(zero) != 2 {
			t.Fatalf("zero failover returned %d rows, want the 2 that did not fail over", len(zero))
		}
		for _, v := range zero {
			if v.Failover {
				t.Fatalf("zero failover leaked a failover row: %s", v.RequestID)
			}
		}
	})

	t.Run("unset identity does not raise", func(t *testing.T) {
		// The regression this guards: a `tenant_id=''::uuid` style predicate
		// would fail the whole query the moment the filter was left blank.
		if _, err := pg.SearchRequestLogs(context.Background(), RequestLogFilter{Limit: 50, Failover: -1}); err != nil {
			t.Fatalf("unfiltered search: %v", err)
		}
	})
}

// TestSummarizeRequestLogsCoversTheWholeFilteredSet checks that the summary
// aggregates every matching row rather than the page on screen, and that it
// respects the same predicate as the table.
func TestSummarizeRequestLogsCoversTheWholeFilteredSet(t *testing.T) {
	pg := testPostgres(t)
	clearRequestLogs(t, pg)
	clearTenantsAndKeys(t, pg)

	alpha := seedTenantAndKey(t, pg, "sum-alpha", "sum-alpha-key")
	beta := seedTenantAndKey(t, pg, "sum-beta", "sum-beta-key")

	base := time.Now().UTC().Add(-6 * time.Hour).Truncate(time.Second)
	rows := []logRow{
		{"s-1", alpha, "gpt-4o", "openai", 200, 100, 100, false, base},
		{"s-2", alpha, "gpt-4o", "openai", 200, 200, 200, false, base.Add(time.Minute)},
		{"s-3", alpha, "gpt-4o", "openai", 500, 300, 300, true, base.Add(2 * time.Minute)},
		{"s-4", beta, "gpt-4o", "openai", 200, 9999, 50, false, base},
	}
	insertLogRows(t, pg, rows)

	got, err := pg.SummarizeRequestLogs(context.Background(), RequestLogFilter{TenantID: alpha.tenantID, Failover: -1})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if got.Requests != 3 {
		t.Fatalf("requests = %d, want 3", got.Requests)
	}
	if got.Errors != 1 {
		t.Fatalf("errors = %d, want 1", got.Errors)
	}
	if got.TotalTokens != 600 || got.InputTokens != 600 || got.OutputTokens != 0 {
		t.Fatalf("tokens = %d/%d/%d, want 600/600/0", got.TotalTokens, got.InputTokens, got.OutputTokens)
	}
	if got.FailoverCount != 1 {
		t.Fatalf("failover = %d, want 1", got.FailoverCount)
	}
	if got.AvgLatencyMS != 200 {
		t.Fatalf("avg latency = %v, want 200", got.AvgLatencyMS)
	}
	// PERCENTILE_CONT interpolates, so the 95th of {100,200,300} is 290.
	if got.P95LatencyMS != 290 {
		t.Fatalf("p95 latency = %v, want 290", got.P95LatencyMS)
	}

	// The same predicate the table uses: a limit on the page must not change
	// the summary, which is the whole reason it is a separate query.
	page, err := pg.SearchRequestLogs(context.Background(), RequestLogFilter{Limit: 1, TenantID: alpha.tenantID, Failover: -1})
	if err != nil {
		t.Fatalf("search page: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("page returned %d rows, want 1", len(page))
	}
	again, err := pg.SummarizeRequestLogs(context.Background(), RequestLogFilter{Limit: 1, TenantID: alpha.tenantID, Failover: -1})
	if err != nil {
		t.Fatalf("summarize with a limit: %v", err)
	}
	if again.Requests != got.Requests {
		t.Fatalf("summary changed with the page size: %d then %d", got.Requests, again.Requests)
	}
}

// TestRequestLogSeriesBucketsInTheCallersOffset is the test that would have
// passed with a UTC implementation and shipped the wrong chart.
//
// The three instants are chosen so the two offsets genuinely disagree about the
// grouping, not merely about the label: UTC files the first request under the
// 15th and the other two under the 16th, while UTC+8 files the first two under
// the 16th and the last under the 17th. A version that truncated in UTC would
// return the first grouping for both offsets.
func TestRequestLogSeriesBucketsInTheCallersOffset(t *testing.T) {
	pg := testPostgres(t)
	clearRequestLogs(t, pg)
	clearTenantsAndKeys(t, pg)
	fx := seedTenantAndKey(t, pg, "series", "series-key")

	// Fixed instants, so the assertions do not depend on when the suite runs.
	// 23:00Z on the 15th is 07:00 on the 16th for a reader in UTC+8.
	lateEvening := time.Date(2026, 3, 15, 23, 0, 0, 0, time.UTC)
	midAfternoon := time.Date(2026, 3, 16, 14, 0, 0, 0, time.UTC)
	lateAfternoon := time.Date(2026, 3, 16, 17, 0, 0, 0, time.UTC)
	insertLogRows(t, pg, []logRow{
		{"t-1", fx, "gpt-4o", "openai", 200, 10, 100, false, lateEvening},
		{"t-2", fx, "gpt-4o", "openai", 200, 20, 200, false, midAfternoon},
		{"t-3", fx, "gpt-4o", "openai", 500, 30, 300, false, lateAfternoon},
	})

	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 18, 0, 0, 0, 0, time.UTC)

	t.Run("utc", func(t *testing.T) {
		got, err := pg.RequestLogSeries(context.Background(), "", from, to, "day", 0, "YYYY-MM-DD")
		if err != nil {
			t.Fatalf("series: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("utc buckets = %#v, want two days", got)
		}
		if got[0].Bucket != "2026-03-15" || got[0].Requests != 1 || got[0].Tokens != 10 {
			t.Fatalf("utc first bucket = %#v, want 2026-03-15 with 1 request / 10 tokens", got[0])
		}
		if got[1].Bucket != "2026-03-16" || got[1].Requests != 2 || got[1].Tokens != 50 || got[1].Errors != 1 {
			t.Fatalf("utc second bucket = %#v, want 2026-03-16 with 2 requests / 50 tokens / 1 error", got[1])
		}
	})

	t.Run("utc+8", func(t *testing.T) {
		got, err := pg.RequestLogSeries(context.Background(), "", from, to, "day", 480, "YYYY-MM-DD")
		if err != nil {
			t.Fatalf("series: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("utc+8 buckets = %#v, want two days", got)
		}
		// The 23:00Z request joins the 14:00Z one on the 16th locally, and the
		// 17:00Z request moves to the 17th. Neither grouping is reachable by
		// truncating in UTC.
		if got[0].Bucket != "2026-03-16" || got[0].Requests != 2 || got[0].Tokens != 30 || got[0].Errors != 0 {
			t.Fatalf("utc+8 first bucket = %#v, want 2026-03-16 with 2 requests / 30 tokens / 0 errors", got[0])
		}
		if got[1].Bucket != "2026-03-17" || got[1].Requests != 1 || got[1].Errors != 1 {
			t.Fatalf("utc+8 second bucket = %#v, want 2026-03-17 with the failing request", got[1])
		}
	})

	t.Run("hourly labels", func(t *testing.T) {
		got, err := pg.RequestLogSeries(context.Background(), "",
			lateEvening.Add(-time.Hour), lateEvening.Add(time.Hour), "hour", 480, "YYYY-MM-DD HH24:00")
		if err != nil {
			t.Fatalf("hourly series: %v", err)
		}
		if len(got) != 1 || got[0].Bucket != "2026-03-16 07:00" {
			t.Fatalf("hourly buckets = %#v, want a single 2026-03-16 07:00", got)
		}
	})

	t.Run("the range is half open", func(t *testing.T) {
		// Two adjacent windows that share a boundary must not both count the
		// row sitting on it, or a paged trend double-counts every seam. With a
		// UTC offset both halves bucket by UTC day, so the first covers the
		// 15th and the second covers the 16th.
		first, err := pg.RequestLogSeries(context.Background(), "",
			from, midAfternoon, "day", 0, "YYYY-MM-DD")
		if err != nil {
			t.Fatalf("first half: %v", err)
		}
		second, err := pg.RequestLogSeries(context.Background(), "",
			midAfternoon, to, "day", 0, "YYYY-MM-DD")
		if err != nil {
			t.Fatalf("second half: %v", err)
		}
		if len(first) != 1 || first[0].Bucket != "2026-03-15" || first[0].Requests != 1 {
			t.Fatalf("first half = %#v, want only the request before the boundary", first)
		}
		if len(second) != 1 || second[0].Bucket != "2026-03-16" || second[0].Requests != 2 {
			t.Fatalf("second half = %#v, want the boundary request and the one after it", second)
		}
		var total int64
		for _, b := range append(append([]model.RequestLogBucket{}, first...), second...) {
			total += b.Requests
		}
		if total != 3 {
			t.Fatalf("the two halves counted %d requests in total, want each of the 3 exactly once", total)
		}
	})
}

// TestRevokeAPIKeyDestroysTheStoredDigest is the security property: after a
// revoke, re-enabling the row must not bring the credential back.
func TestRevokeAPIKeyDestroysTheStoredDigest(t *testing.T) {
	pg := testPostgres(t)
	clearRequestLogs(t, pg)
	clearTenantsAndKeys(t, pg)

	fx := seedTenantAndKey(t, pg, "revoke", "revoke-key")
	ctx := context.Background()

	before := storedDigest(t, pg, fx.keyID)
	if before == "" {
		t.Fatal("fixture key has no digest")
	}

	if err := pg.RevokeAPIKey(ctx, fx.keyID, "digest-after-revoke"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	after := storedDigest(t, pg, fx.keyID)
	if after == before {
		t.Fatal("revoke left the original digest in place; the credential survives")
	}
	if after != "digest-after-revoke" {
		t.Fatalf("digest = %q, want the replacement", after)
	}

	var status int
	if err := pg.Pool.QueryRow(ctx, `SELECT status FROM gateway_api_key WHERE id=$1::uuid`, fx.keyID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != 2 {
		t.Fatalf("status = %d, want 2 (disabled)", status)
	}

	// Re-enabling is allowed — it is an ordinary edit — but it must not restore
	// the old credential, which is the difference between revoke and disable.
	keys, err := pg.ListAPIKeys(ctx)
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	for _, k := range keys {
		if k.ID != fx.keyID {
			continue
		}
		k.Status = 1
		if _, err := pg.UpdateAPIKey(ctx, k.ID, k); err != nil {
			t.Fatalf("re-enable: %v", err)
		}
	}
	if again := storedDigest(t, pg, fx.keyID); again != after {
		t.Fatalf("re-enable changed the digest to %q", again)
	}
}

func TestRevokeAPIKeyReportsAMissingKey(t *testing.T) {
	pg := testPostgres(t)
	err := pg.RevokeAPIKey(context.Background(), "00000000-0000-0000-0000-000000000000", "digest")
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoking an unknown key returned %v, want pgx.ErrNoRows", err)
	}
}

// TestDeleteAPIKeyRemovesTheRow is the property the console's 删除 action
// promises, and the one revocation deliberately does not: the row is gone, and
// a second delete reports that nothing was there rather than succeeding.
func TestDeleteAPIKeyRemovesTheRow(t *testing.T) {
	pg := testPostgres(t)
	clearRequestLogs(t, pg)
	clearTenantsAndKeys(t, pg)

	fx := seedTenantAndKey(t, pg, "purge", "purge-key")
	ctx := context.Background()

	if err := pg.DeleteAPIKey(ctx, fx.keyID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var remaining int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_api_key WHERE id=$1::uuid`, fx.keyID).Scan(&remaining); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("key row still present after delete (count=%d)", remaining)
	}

	if err := pg.DeleteAPIKey(ctx, fx.keyID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("deleting a missing key returned %v, want pgx.ErrNoRows", err)
	}
}

// TestDeleteTenantRemovesItsKeys covers the order the foreign key forces: the
// tenant cannot be deleted while a key still references it, so the transaction
// removes the keys first and reports how many went with the tenant.
func TestDeleteTenantRemovesItsKeys(t *testing.T) {
	pg := testPostgres(t)
	clearRequestLogs(t, pg)
	clearTenantsAndKeys(t, pg)

	fx := seedTenantAndKey(t, pg, "purge-tenant", "purge-tenant-key")
	ctx := context.Background()

	deleted, err := pg.DeleteTenant(ctx, fx.tenantID)
	if err != nil {
		t.Fatalf("delete tenant: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted_keys = %d, want 1", deleted)
	}

	var tenantsLeft, keysLeft int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_tenant WHERE id=$1::uuid`, fx.tenantID).Scan(&tenantsLeft); err != nil {
		t.Fatalf("count tenants: %v", err)
	}
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_api_key WHERE tenant_id=$1::uuid`, fx.tenantID).Scan(&keysLeft); err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if tenantsLeft != 0 || keysLeft != 0 {
		t.Fatalf("after delete: tenants=%d keys=%d, want 0 and 0", tenantsLeft, keysLeft)
	}

	if _, err := pg.DeleteTenant(ctx, fx.tenantID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("deleting a missing tenant returned %v, want pgx.ErrNoRows", err)
	}
}

// TestListAPIKeysReportsCumulativeUsage checks the column the console shows
// next to the rate limits, including the case that matters most: a key that has
// served nothing must read as zero rather than disappear from the list.
func TestListAPIKeysReportsCumulativeUsage(t *testing.T) {
	pg := testPostgres(t)
	clearRequestLogs(t, pg)
	clearTenantsAndKeys(t, pg)

	busy := seedTenantAndKey(t, pg, "usage-busy", "usage-busy-key")
	idle := seedTenantAndKey(t, pg, "usage-idle", "usage-idle-key")

	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	insertLogRows(t, pg, []logRow{
		{"u-1", busy, "gpt-4o", "openai", 200, 100, 100, false, base},
		{"u-2", busy, "gpt-4o", "openai", 200, 250, 100, false, base},
		{"u-3", busy, "gpt-4o", "openai", 500, 400, 100, true, base},
	})

	keys, err := pg.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}

	seen := map[string]model.APIKeyAdmin{}
	for _, k := range keys {
		seen[k.ID] = k
	}

	got, ok := seen[busy.keyID]
	if !ok {
		t.Fatal("the busy key is missing from the list")
	}
	if got.UsageRequests != 3 || got.UsageTokens != 750 {
		t.Fatalf("busy usage = %d requests / %d tokens, want 3 / 750", got.UsageRequests, got.UsageTokens)
	}

	idleKey, ok := seen[idle.keyID]
	if !ok {
		t.Fatal("a key with no traffic was dropped from the list by the usage join")
	}
	if idleKey.UsageRequests != 0 || idleKey.UsageTokens != 0 {
		t.Fatalf("idle usage = %d / %d, want 0 / 0", idleKey.UsageRequests, idleKey.UsageTokens)
	}
}

// --- helpers ---------------------------------------------------------------

func storedDigest(t *testing.T, pg *Postgres, id string) string {
	t.Helper()
	var digest string
	if err := pg.Pool.QueryRow(context.Background(),
		`SELECT key_digest FROM gateway_api_key WHERE id=$1::uuid`, id).Scan(&digest); err != nil {
		t.Fatalf("read digest for %s: %v", id, err)
	}
	return digest
}

// clearTenantsAndKeys empties both tables between cases. request_log first,
// because it is the one that references the others.
func clearTenantsAndKeys(t *testing.T, pg *Postgres) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM gateway_request_log`,
		`DELETE FROM gateway_api_key`,
		`DELETE FROM gateway_tenant`,
	} {
		if _, err := pg.Pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", fmt.Sprintf("%.40s", stmt), err)
		}
	}
}
