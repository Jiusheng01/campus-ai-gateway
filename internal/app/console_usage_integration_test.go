package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

// The personal usage endpoint's central rule is a database property: the owner
// predicate is the session account, and no query parameter can widen it. These
// tests therefore run against a migrated Postgres, like the other console
// suites, and assert the JSON that leaves the handler.

type consoleUsageRow struct {
	requestID string
	ownerID   string
	tenantID  string
	keyID     string
	model     string
	input     int64
	output    int64
	status    int
	at        time.Time
}

func consoleUsageInsert(t *testing.T, pg *storage.Postgres, rows []consoleUsageRow) {
	t.Helper()
	ctx := context.Background()
	for _, r := range rows {
		_, err := pg.Pool.Exec(ctx, `
INSERT INTO gateway_request_log(
	request_id, tenant_id, api_key_id, owner_user_id, requested_model, actual_model,
	provider, stream, input_tokens, output_tokens, total_tokens, latency_ms, status_code,
	failover, created_at
) VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5,$5,'console-test',false,$6,$7,$8,10,$9,false,$10)`,
			r.requestID, r.tenantID, r.keyID, r.ownerID, r.model,
			r.input, r.output, r.input+r.output, r.status, r.at,
		)
		if err != nil {
			t.Fatalf("insert Request Record %s: %v", r.requestID, err)
		}
	}
}

// consoleUsageKey creates a key under the Tenant and snapshots its owner, as a
// personal key created through the console would carry.
func consoleUsageKey(t *testing.T, pg *storage.Postgres, tenantID, ownerID, name string) string {
	t.Helper()
	ctx := context.Background()
	key, err := pg.CreateAPIKey(ctx, model.APIKeyAdmin{
		TenantID:      tenantID,
		Name:          name,
		KeyPrefix:     "sk-" + name,
		Status:        1,
		AllowedModels: []string{model.AllModelsWildcard},
	}, "digest-"+name)
	if err != nil {
		t.Fatalf("create key %s: %v", name, err)
	}
	if _, err := pg.Pool.Exec(ctx,
		`UPDATE gateway_api_key SET owner_user_id=$1::uuid WHERE id=$2::uuid`, ownerID, key.ID); err != nil {
		t.Fatalf("set owner on key %s: %v", name, err)
	}
	return key.ID
}

func consoleUsageCall(t *testing.T, s *Server, token, query string) (*httptest.ResponseRecorder, model.ConsoleUsage) {
	t.Helper()
	target := "http://gateway.local/api/console/usage"
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("X-Request-Id", "console-usage-test")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/usage = %d: %s", rec.Code, rec.Body.String())
	}
	var out model.ConsoleUsage
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode usage response %q: %v", rec.Body.String(), err)
	}
	return rec, out
}

// Two Accounts in one Tenant: A's endpoint reports A's traffic only, while the
// shared quota it displays is the Tenant's whole consumption. That is exactly
// the distinction the page exists to make — my usage, our budget.
func TestConsoleUsageCountsOnlyTheSessionsOwnRecords(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)

	tenant := usageSeedTenant(t, pg, "personal-usage", 1000, 10000, 20)
	alice := tenantAssignmentAccount(t, pg, uniqueAccountName("usage-alice"), []string{RoleMember})
	bob := tenantAssignmentAccount(t, pg, uniqueAccountName("usage-bob"), []string{RoleMember})
	consoleKeysAssign(t, pg, alice.ID, tenant.ID)
	consoleKeysAssign(t, pg, bob.ID, tenant.ID)

	aliceKey := consoleUsageKey(t, pg, tenant.ID, alice.ID, "usage-alice-key")
	bobKey := consoleUsageKey(t, pg, tenant.ID, bob.ID, "usage-bob-key")

	now := time.Now().UTC()
	consoleUsageInsert(t, pg, []consoleUsageRow{
		// Alice: two calls to model-a (one failure) and one to model-b.
		{"alice-1", alice.ID, tenant.ID, aliceKey, "model-a", 10, 5, 200, now.Add(-3 * time.Hour)},
		{"alice-2", alice.ID, tenant.ID, aliceKey, "model-a", 20, 0, 500, now.Add(-2 * time.Hour)},
		{"alice-3", alice.ID, tenant.ID, aliceKey, "model-b", 7, 3, 200, now.Add(-time.Hour)},
		// Bob: heavier traffic in the same Tenant. It must contribute to the
		// shared quota but never to Alice's totals or models.
		{"bob-1", bob.ID, tenant.ID, bobKey, "model-c", 100, 100, 200, now.Add(-90 * time.Minute)},
		{"bob-2", bob.ID, tenant.ID, bobKey, "model-c", 50, 50, 500, now.Add(-80 * time.Minute)},
	})

	token := tenantAssignmentToken(t, s, alice.Username)
	_, out := consoleUsageCall(t, s, token, "")

	// Alice's totals: 3 requests, 2 successes, 1 failure, 37 input + 8 output.
	if out.Totals.Requests != 3 || out.Totals.Successes != 2 || out.Totals.Failures != 1 {
		t.Fatalf("totals requests/successes/failures = %d/%d/%d, want 3/2/1",
			out.Totals.Requests, out.Totals.Successes, out.Totals.Failures)
	}
	if out.Totals.Successes+out.Totals.Failures != out.Totals.Requests {
		t.Fatalf("successes+failures = %d, requests = %d", out.Totals.Successes+out.Totals.Failures, out.Totals.Requests)
	}
	if out.Totals.InputTokens != 37 || out.Totals.OutputTokens != 8 || out.Totals.TotalTokens != 45 {
		t.Fatalf("tokens = %d/%d/%d, want 37/8/45",
			out.Totals.InputTokens, out.Totals.OutputTokens, out.Totals.TotalTokens)
	}
	if !approx(out.Totals.ErrorRate, 100.0/3.0) {
		t.Fatalf("error_rate = %v, want %.4f", out.Totals.ErrorRate, 100.0/3.0)
	}

	// The per-model rows are real aggregates, not placeholders.
	if len(out.ByModel) != 2 {
		t.Fatalf("by_model has %d rows, want 2: %+v", len(out.ByModel), out.ByModel)
	}
	byAlias := map[string]model.ConsoleUsageModel{}
	for _, m := range out.ByModel {
		byAlias[m.Alias] = m
	}
	if a := byAlias["model-a"]; a.Requests != 2 || a.InputTokens != 30 || a.OutputTokens != 5 || a.TotalTokens != 35 {
		t.Fatalf("model-a = %+v, want 2 requests / 30 in / 5 out / 35 total", a)
	}
	if b := byAlias["model-b"]; b.Requests != 1 || b.TotalTokens != 10 {
		t.Fatalf("model-b = %+v, want 1 request / 10 total", b)
	}
	if _, ok := byAlias["model-c"]; ok {
		t.Fatal("another member's model appeared in the per-model breakdown")
	}

	// The series is Alice's too, and shares the totals' window.
	var seriesRequests, seriesTokens, seriesErrors int64
	for _, p := range out.Series {
		seriesRequests += p.Requests
		seriesTokens += p.Tokens
		seriesErrors += p.Errors
	}
	if seriesRequests != out.Totals.Requests || seriesTokens != out.Totals.TotalTokens || seriesErrors != out.Totals.Failures {
		t.Fatalf("series sums = %d/%d/%d, want %d/%d/%d",
			seriesRequests, seriesTokens, seriesErrors,
			out.Totals.Requests, out.Totals.TotalTokens, out.Totals.Failures)
	}

	// The Tenant's shared quota is the one figure that includes Bob: 5 requests
	// and 345 tokens across both members, quoted as aggregate bands only.
	if out.TenantQuota == nil {
		t.Fatal("an assigned account must see the Tenant's shared quota")
	}
	if out.TenantQuota.TenantID != tenant.ID || out.TenantQuota.TenantName != tenant.Name {
		t.Fatalf("tenant_quota identity = %q/%q", out.TenantQuota.TenantID, out.TenantQuota.TenantName)
	}
	dq := out.TenantQuota.DailyToken
	if dq == nil || dq.Used != 345 || dq.Limit != 1000 || !approx(dq.Pct, 34.5) {
		t.Fatalf("daily token quota = %+v, want used 345 / limit 1000 / pct 34.5", dq)
	}
	mq := out.TenantQuota.MonthlyRequest
	if mq == nil || mq.Used != 5 || mq.Limit != 20 || !approx(mq.Pct, 25) {
		t.Fatalf("monthly request quota = %+v, want used 5 / limit 20 / pct 25", mq)
	}

	if out.CostAvailable {
		t.Fatal("cost_available must be false: the gateway records tokens, not money")
	}

	// No other member's identifiers may leak through the quota block.
	raw := strings.ToLower(consoleUsageJSON(t, out))
	for _, forbidden := range []string{strings.ToLower(bob.ID), strings.ToLower(bobKey), strings.ToLower(bob.Username)} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("response leaked another member's identifier %q", forbidden)
		}
	}
}

// A caller that names a Tenant or key in the query gets the same numbers: the
// handler does not read those parameters, so they can neither widen nor narrow
// the owner's set.
func TestConsoleUsageIgnoresClientSuppliedTenantAndKey(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)

	tenant := usageSeedTenant(t, pg, "personal-usage-forged", 0, 0, 0)
	alice := tenantAssignmentAccount(t, pg, uniqueAccountName("usage-forged-alice"), []string{RoleMember})
	bob := tenantAssignmentAccount(t, pg, uniqueAccountName("usage-forged-bob"), []string{RoleMember})
	consoleKeysAssign(t, pg, alice.ID, tenant.ID)
	consoleKeysAssign(t, pg, bob.ID, tenant.ID)
	aliceKey := consoleUsageKey(t, pg, tenant.ID, alice.ID, "forged-alice-key")
	bobKey := consoleUsageKey(t, pg, tenant.ID, bob.ID, "forged-bob-key")

	now := time.Now().UTC()
	consoleUsageInsert(t, pg, []consoleUsageRow{
		{"forged-alice", alice.ID, tenant.ID, aliceKey, "model-a", 4, 6, 200, now.Add(-time.Hour)},
		{"forged-bob", bob.ID, tenant.ID, bobKey, "model-b", 400, 600, 200, now.Add(-time.Hour)},
	})

	token := tenantAssignmentToken(t, s, alice.Username)
	_, plain := consoleUsageCall(t, s, token, "")
	_, forged := consoleUsageCall(t, s, token,
		"tenant_id="+tenant.ID+"&api_key_id="+bobKey+"&owner_user_id="+bob.ID)

	if plain.Totals != forged.Totals {
		t.Fatalf("query parameters changed the totals: plain %+v, forged %+v", plain.Totals, forged.Totals)
	}
	if plain.Totals.Requests != 1 || plain.Totals.TotalTokens != 10 {
		t.Fatalf("totals = %+v, want Alice's single 10-token request", plain.Totals)
	}
	if len(forged.ByModel) != 1 || forged.ByModel[0].Alias != "model-a" {
		t.Fatalf("by_model = %+v, want only Alice's model-a", forged.ByModel)
	}
}

// The period is the caller's choice and it is reflected in the figures. The
// default is the last 24 hours in hourly buckets, matching the dashboard.
func TestConsoleUsagePeriodSelectorBoundsTheWindow(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)

	tenant := usageSeedTenant(t, pg, "personal-usage-period", 0, 0, 0)
	member := tenantAssignmentAccount(t, pg, uniqueAccountName("usage-period"), []string{RoleMember})
	consoleKeysAssign(t, pg, member.ID, tenant.ID)
	key := consoleUsageKey(t, pg, tenant.ID, member.ID, "period-key")

	now := time.Now().UTC()
	consoleUsageInsert(t, pg, []consoleUsageRow{
		{"period-recent", member.ID, tenant.ID, key, "model-a", 10, 10, 200, now.Add(-time.Hour)},
		{"period-old", member.ID, tenant.ID, key, "model-a", 20, 20, 200, now.Add(-48 * time.Hour)},
	})

	token := tenantAssignmentToken(t, s, member.Username)

	_, def := consoleUsageCall(t, s, token, "")
	if def.Bucket != "hour" {
		t.Fatalf("default bucket = %q, want hour", def.Bucket)
	}
	from, err := time.Parse(time.RFC3339, def.From)
	if err != nil {
		t.Fatalf("from %q: %v", def.From, err)
	}
	to, err := time.Parse(time.RFC3339, def.To)
	if err != nil {
		t.Fatalf("to %q: %v", def.To, err)
	}
	if to.Sub(from) != 24*time.Hour {
		t.Fatalf("default window = %v, want 24h", to.Sub(from))
	}
	if def.Totals.Requests != 1 || def.Totals.TotalTokens != 20 {
		t.Fatalf("default totals = %+v, want only the recent request", def.Totals)
	}

	wide := "from=" + now.Add(-72*time.Hour).Format(time.RFC3339) +
		"&to=" + now.Add(time.Minute).Format(time.RFC3339) + "&bucket=day"
	_, out := consoleUsageCall(t, s, token, wide)
	if out.Bucket != "day" {
		t.Fatalf("bucket = %q, want day", out.Bucket)
	}
	if out.Totals.Requests != 2 || out.Totals.TotalTokens != 60 {
		t.Fatalf("wide totals = %+v, want both requests / 60 tokens", out.Totals)
	}
}

func consoleUsageJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
