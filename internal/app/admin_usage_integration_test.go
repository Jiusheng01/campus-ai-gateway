package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/jackc/pgx/v5"
)

// The usage endpoint's most important property is a property of the database,
// not of Go code: that its aggregates describe exactly the rows the detail page
// would list under the same filter. Only a real PostgreSQL can vouch for that —
// an in-memory fake would only confirm that the query and the fake agree with
// themselves. So, like the storage integration tests, these run against a fresh
// migrated scratch database and skip when nothing answers.
//
// They live in the app package because the seam under test is the HTTP handler
// (handleRequestLogsUsage), not the storage method: the contract is the JSON
// that leaves the endpoint.

// A different default database from the storage package's, so `go test ./.`
// running the two packages in parallel does not have them drop and recreate the
// same scratch database at once. TEST_DATABASE_URL overrides it; the tests skip
// when nothing answers.
const defaultUsageTestURL = "postgres://campus:campus@127.0.0.1:5432/campus_gateway_app_test?sslmode=disable"

var (
	usageSchemaOnce sync.Once
	usageSchemaErr  error
)

func usageTestURL() string {
	if v := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL")); v != "" {
		return v
	}
	return defaultUsageTestURL
}

// usagePostgres returns a pool against a freshly migrated test database, or skips.
func usagePostgres(t *testing.T) *storage.Postgres {
	t.Helper()
	url := usageTestURL()
	usageSchemaOnce.Do(func() { usageSchemaErr = buildUsageTestDatabase(url) })
	if usageSchemaErr != nil {
		t.Skipf("test PostgreSQL unavailable: %v (set TEST_DATABASE_URL to run the integration tests)", usageSchemaErr)
	}
	pg, err := storage.OpenPostgres(context.Background(), url)
	if err != nil {
		t.Skipf("test PostgreSQL unavailable: %v", err)
	}
	t.Cleanup(pg.Close)
	return pg
}

// buildUsageTestDatabase recreates the test database and applies the real
// migrations. Mirrors the storage package's bootstrap so both packages can
// test independent scratch databases.
func buildUsageTestDatabase(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return fmt.Errorf("parse TEST_DATABASE_URL: %w", err)
	}
	dbName := cfg.Database
	if dbName == "" {
		return errors.New("TEST_DATABASE_URL has no database name")
	}

	adminCfg := cfg.Copy()
	adminCfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+usageQuoteIdent(dbName)+` WITH (FORCE)`); err != nil {
		return fmt.Errorf("drop test database: %w", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+usageQuoteIdent(dbName)); err != nil {
		return fmt.Errorf("create test database: %w", err)
	}

	migrateCfg := cfg.Copy()
	migrateCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db, err := pgx.ConnectConfig(ctx, migrateCfg)
	if err != nil {
		return fmt.Errorf("connect to test database: %w", err)
	}
	defer func() { _ = db.Close(ctx) }()

	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil {
		return fmt.Errorf("locate migrations: %w", err)
	}
	if len(files) == 0 {
		return errors.New("no migrations found; run the tests from the repository root")
	}
	sort.Strings(files)
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read %s: %w", file, err)
		}
		if _, err := db.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", filepath.Base(file), err)
		}
	}
	return nil
}

func usageQuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// --- fixtures --------------------------------------------------------------

type usageFixture struct {
	tenantID string
	keyID    string
}

// usageSeedTenant creates one tenant with the given quota limits.
func usageSeedTenant(t *testing.T, pg *storage.Postgres, code string, daily, monthlyTokens, monthlyReqs int64) model.Tenant {
	t.Helper()
	ctx := context.Background()
	tenant, err := pg.CreateTenant(ctx, model.Tenant{
		Code: code, Name: code, Status: 1,
		// The limits are three-state pointers, but this fixture always states a
		// value (0 included): the tests using it are about the usage panel's
		// arithmetic, not about inheritance.
		DailyTokenLimit:     &daily,
		MonthlyTokenLimit:   &monthlyTokens,
		MonthlyRequestLimit: &monthlyReqs,
	})
	if err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return tenant
}

// usageSeedKey adds one API key under the tenant and returns its id.
func usageSeedKey(t *testing.T, pg *storage.Postgres, tenant model.Tenant, keyName string) string {
	t.Helper()
	ctx := context.Background()
	key, err := pg.CreateAPIKey(ctx, model.APIKeyAdmin{
		TenantID:  tenant.ID,
		Name:      keyName,
		KeyPrefix: "campus_" + tenant.Code + "_" + keyName,
		Status:    1,
	}, "digest-"+tenant.Code+"-"+keyName)
	if err != nil {
		t.Fatalf("create key %s: %v", keyName, err)
	}
	return key.ID
}

// usageSeedTenantAndKey creates a tenant with one key and returns the pair.
func usageSeedTenantAndKey(t *testing.T, pg *storage.Postgres, code, keyName string, daily, monthlyTokens, monthlyReqs int64) usageFixture {
	t.Helper()
	tenant := usageSeedTenant(t, pg, code, daily, monthlyTokens, monthlyReqs)
	return usageFixture{tenantID: tenant.ID, keyID: usageSeedKey(t, pg, tenant, keyName)}
}

type usageRow struct {
	requestID string
	fixture   usageFixture
	tokens    int64
	latency   int64
	status    int
	failover  bool
}

func usageInsertRows(t *testing.T, pg *storage.Postgres, rows []usageRow) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, r := range rows {
		_, err := pg.Pool.Exec(ctx, `
INSERT INTO gateway_request_log(
	request_id, tenant_id, api_key_id, requested_model, actual_model, provider,
	stream, input_tokens, output_tokens, total_tokens, latency_ms, status_code,
	failover, created_at
) VALUES ($1,$2::uuid,$3::uuid,'gpt-4o','gpt-4o','openai',false,$4,0,$4,$5,$6,$7,$8)`,
			r.requestID, r.fixture.tenantID, r.fixture.keyID, r.tokens, r.latency, r.status, r.failover, now)
		if err != nil {
			t.Fatalf("insert log %s: %v", r.requestID, err)
		}
	}
}

func usageClearTables(t *testing.T, pg *storage.Postgres) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM gateway_request_log`,
		`DELETE FROM gateway_api_key`,
		`DELETE FROM gateway_tenant`,
	} {
		if _, err := pg.Pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("clear: %v", err)
		}
	}
}

func usageGet(t *testing.T, s *Server, target string) model.RequestLogUsage {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleRequestLogsUsage(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("usage status = %d, body %s", rec.Code, rec.Body.String())
	}
	var out model.RequestLogUsage
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	return out
}

func approx(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 0.0001
}

// --- tests -----------------------------------------------------------------

// TestUsageEndpointMatchesTheDetailAggregationAndGroupsByKey is the whole point
// of the endpoint. Under one filter it must report totals that are exactly what
// the detail page aggregates, group the same rows by API key, and quote the
// tenant's quota against Postgres-derived day/month usage.
func TestUsageEndpointMatchesTheDetailAggregationAndGroupsByKey(t *testing.T) {
	pg := usagePostgres(t)
	usageClearTables(t, pg)
	// daily 1000, monthly-token 10000, monthly-request 20 — two keys on it.
	tenant := usageSeedTenant(t, pg, "usage-a", 1000, 10000, 20)
	// A second key on the same tenant so the by-key grouping is a real grouping.
	keyA := usageSeedKey(t, pg, tenant, "usage-a-key")
	keyB := usageSeedKey(t, pg, tenant, "usage-b-key")
	fx := usageFixture{tenantID: tenant.ID, keyID: keyA}
	fxB := usageFixture{tenantID: tenant.ID, keyID: keyB}

	s := &Server{pg: pg}

	usageInsertRows(t, pg, []usageRow{
		{"u-a-1", usageFixture{tenantID: fx.tenantID, keyID: fx.keyID}, 100, 100, 200, false},
		{"u-a-2", usageFixture{tenantID: fx.tenantID, keyID: fx.keyID}, 200, 200, 200, false},
		{"u-a-3", usageFixture{tenantID: fxB.tenantID, keyID: fxB.keyID}, 400, 50, 500, true},
	})

	out := usageGet(t, s, "/admin/request-logs/usage?tenant_id="+fx.tenantID)

	// Totals over the whole filtered set: 3 requests, 1 error, 700 tokens
	// (100+200+400), avg latency 116.67, one failover.
	tt := out.Totals
	if tt.Requests != 3 || tt.Errors != 1 {
		t.Fatalf("requests/errors = %d/%d, want 3/1", tt.Requests, tt.Errors)
	}
	if tt.InputTokens != 700 || tt.OutputTokens != 0 || tt.TotalTokens != 700 {
		t.Fatalf("tokens = %d/%d/%d, want 700/0/700", tt.InputTokens, tt.OutputTokens, tt.TotalTokens)
	}
	if tt.FailoverCount != 1 {
		t.Fatalf("failover = %d, want 1", tt.FailoverCount)
	}
	if !approx(tt.AvgLatencyMS, 116.6667) {
		t.Fatalf("avg latency = %v, want 116.67", tt.AvgLatencyMS)
	}

	// by_key: two keys, each with its own requests and tokens.
	if len(out.ByKey) != 2 {
		t.Fatalf("by_key has %d rows, want 2", len(out.ByKey))
	}
	byID := map[string]model.APIKeyUsage{}
	for _, k := range out.ByKey {
		byID[k.APIKeyID] = k
	}
	k1 := byID[fx.keyID]
	if k1.APIKeyID == "" || k1.Requests != 2 || k1.Tokens != 300 {
		t.Fatalf("key %s by_key = %#v, want 2 requests / 300 tokens", fx.keyID, k1)
	}
	k2 := byID[fxB.keyID]
	if k2.Requests != 1 || k2.Tokens != 400 {
		t.Fatalf("key %s by_key = %#v, want 1 request / 400 tokens", fxB.keyID, k2)
	}

	// Quota: all three rows landed today, so today's and this month's used for
	// this tenant is 700 tokens / 3 requests.
	dq := tt.Quota.DailyToken
	if dq == nil || dq.Used != 700 || dq.Limit != 1000 || !approx(dq.Pct, 70.0) {
		t.Fatalf("daily quota = %#v, want used 700 / limit 1000 / pct 70", dq)
	}
	mq := tt.Quota.MonthlyToken
	if mq == nil || mq.Used != 700 || mq.Limit != 10000 || !approx(mq.Pct, 7.0) {
		t.Fatalf("monthly token quota = %#v, want used 700 / limit 10000 / pct 7", mq)
	}
	mr := tt.Quota.MonthlyRequest
	if mr == nil || mr.Used != 3 || mr.Limit != 20 || !approx(mr.Pct, 15.0) {
		t.Fatalf("monthly request quota = %#v, want used 3 / limit 20 / pct 15", mr)
	}

	// Per-key share of the tenant's monthly token limit: 3% and 4%.
	if k1.QuotaShare == nil || !approx(*k1.QuotaShare, 3.0) {
		t.Fatalf("key %s quota_share = %v, want 3.0", fx.keyID, k1.QuotaShare)
	}
	if k2.QuotaShare == nil || !approx(*k2.QuotaShare, 4.0) {
		t.Fatalf("key %s quota_share = %v, want 4.0", fxB.keyID, k2.QuotaShare)
	}
}

// TestUsageEndpointUnsetLimitsAreNull pins the "not set" reading: a tenant
// without a daily or monthly-request limit must report null for those
// dimensions, never 0%, and a key in such a tenant still shares its token quota
// against the monthly token limit.
func TestUsageEndpointUnsetLimitsAreNull(t *testing.T) {
	pg := usagePostgres(t)
	usageClearTables(t, pg)
	// daily and monthly-request unset (0), monthly token 10000.
	fx := usageSeedTenantAndKey(t, pg, "usage-null", "usage-null-key", 0, 10000, 0)
	s := &Server{pg: pg}
	usageInsertRows(t, pg, []usageRow{
		{"n-1", usageFixture{tenantID: fx.tenantID, keyID: fx.keyID}, 500, 50, 200, false},
	})

	out := usageGet(t, s, "/admin/request-logs/usage?tenant_id="+fx.tenantID)

	if out.Totals.Quota.DailyToken != nil {
		t.Fatalf("daily_token = %#v, want null (no daily limit)", out.Totals.Quota.DailyToken)
	}
	if out.Totals.Quota.MonthlyRequest != nil {
		t.Fatalf("monthly_request = %#v, want null (no monthly request limit)", out.Totals.Quota.MonthlyRequest)
	}
	mq := out.Totals.Quota.MonthlyToken
	if mq == nil || mq.Used != 500 || mq.Limit != 10000 || !approx(mq.Pct, 5.0) {
		t.Fatalf("monthly token quota = %#v, want used 500 / limit 10000 / pct 5", mq)
	}
	if len(out.ByKey) != 1 {
		t.Fatalf("by_key has %d rows, want 1", len(out.ByKey))
	}
	if out.ByKey[0].QuotaShare == nil || !approx(*out.ByKey[0].QuotaShare, 5.0) {
		t.Fatalf("quota_share = %v, want 5.0", out.ByKey[0].QuotaShare)
	}
}

// TestUsageEndpointMixedTenantQuotaIsNull locks the multi-tenant decision: a
// filter that spans tenants has no single denominator, so the quota field and
// every by-key share must be null rather than a made-up percentage. The by-key
// grouping itself still reports the rows correctly.
func TestUsageEndpointMixedTenantQuotaIsNull(t *testing.T) {
	pg := usagePostgres(t)
	usageClearTables(t, pg)
	a := usageSeedTenantAndKey(t, pg, "mix-a", "mix-a-key", 1000, 10000, 20)
	b := usageSeedTenantAndKey(t, pg, "mix-b", "mix-b-key", 1000, 10000, 20)
	s := &Server{pg: pg}
	usageInsertRows(t, pg, []usageRow{
		{"m-1", a, 100, 10, 200, false},
		{"m-2", b, 200, 10, 200, false},
	})

	// No tenant filter -> both tenants in scope.
	out := usageGet(t, s, "/admin/request-logs/usage")

	if out.Totals.Requests != 2 || out.Totals.TotalTokens != 300 {
		t.Fatalf("totals = %d requests / %d tokens, want 2 / 300", out.Totals.Requests, out.Totals.TotalTokens)
	}
	if out.Totals.Quota.DailyToken != nil || out.Totals.Quota.MonthlyToken != nil || out.Totals.Quota.MonthlyRequest != nil {
		t.Fatalf("mixed-tenant quota = %#v, want all null", out.Totals.Quota)
	}
	if len(out.ByKey) != 2 {
		t.Fatalf("by_key has %d rows, want 2", len(out.ByKey))
	}
	for _, k := range out.ByKey {
		if k.QuotaShare != nil {
			t.Fatalf("mixed-tenant key %s quota_share = %v, want null", k.APIKeyID, *k.QuotaShare)
		}
	}
}

// TestUsageEndpointResolvesTheTenantFromItsOwnRows covers the no-tenant-filter
// single-tenant case: when every row under the filter belongs to one tenant,
// the endpoint can still quote that tenant's quota.
func TestUsageEndpointResolvesTheTenantFromItsOwnRows(t *testing.T) {
	pg := usagePostgres(t)
	usageClearTables(t, pg)
	fx := usageSeedTenantAndKey(t, pg, "solo", "solo-key", 1000, 10000, 20)
	s := &Server{pg: pg}
	usageInsertRows(t, pg, []usageRow{
		{"s-1", usageFixture{tenantID: fx.tenantID, keyID: fx.keyID}, 100, 10, 200, false},
	})

	out := usageGet(t, s, "/admin/request-logs/usage")

	dq := out.Totals.Quota.DailyToken
	if dq == nil || dq.Used != 100 || dq.Limit != 1000 {
		t.Fatalf("auto-resolved daily quota = %#v, want used 100 / limit 1000", dq)
	}
}

// TestUsageEndpointGuards covers the same guards the sibling request-log
// endpoints share: only GET is served, and a malformed range is rejected before
// any database work.
func TestUsageEndpointGuards(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleRequestLogsUsage(rec, httptest.NewRequest(http.MethodPost, "/admin/request-logs/usage", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}

	pg := usagePostgres(t)
	s = &Server{pg: pg}
	rec = httptest.NewRecorder()
	s.handleRequestLogsUsage(rec, httptest.NewRequest(http.MethodGet, "/admin/request-logs/usage?from=soon", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed from status = %d, want 400", rec.Code)
	}
}
