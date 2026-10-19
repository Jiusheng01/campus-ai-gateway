package ratelimit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/redis/go-redis/v9"
)

// TestRebuildQuotaCountersWritesTheLogDerivedValuesToRedis is the primary
// assertion of a Quota Counter rebuild: after it runs, the counters the gate
// reads must equal the values derived from the settled request log, not zero
// and not whatever stale counter was sitting in the cache.
//
// It runs against a real Redis because writing the correct key/value is a
// property of Redis (SET vs INCRBY, the reset semantics, the format of the
// period keys), not of Go code. It skips when no server answers, per the
// registration test convention.
func TestRebuildQuotaCountersWritesTheLogDerivedValuesToRedis(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()

	const (
		tenantA = "aaaaaaaa-0000-0000-0000-000000000001"
		tenantB = "bbbbbbbb-0000-0000-0000-000000000002"
	)
	// Two tenants, one with two settled days and one with a single day. The
	// input is the aggregated request log (the durable truth); the rebuild must
	// not invent anything beyond it.
	daily := []model.QuotaSettledRow{
		{TenantID: tenantA, Day: "20261001", Requests: 3, Tokens: 150},
		{TenantID: tenantA, Day: "20261002", Requests: 2, Tokens: 250},
		{TenantID: tenantB, Day: "20261002", Requests: 5, Tokens: 700},
	}
	// Independent literal for the exact period keys the limiter reads, so a
	// refactor of the helper functions cannot silently change the wire format.
	dailyKeyA01 := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", tenantA, "20261001")
	dailyKeyA02 := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", tenantA, "20261002")
	dailyKeyB02 := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", tenantB, "20261002")
	monthlyKeyA := fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", tenantA, "202610")
	monthlyKeyB := fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", tenantB, "202610")
	reqKeyA := fmt.Sprintf("gw:quota:tenant:%s:requests:%s", tenantA, "202610")
	reqKeyB := fmt.Sprintf("gw:quota:tenant:%s:requests:%s", tenantB, "202610")

	for _, k := range []string{dailyKeyA01, dailyKeyA02, dailyKeyB02, monthlyKeyA, monthlyKeyB, reqKeyA, reqKeyB} {
		t.Cleanup(func(k string) func() {
			return func() { _ = client.Del(context.Background(), k).Err() }
		}(k))
	}

	limiter := New(client)
	if _, err := limiter.RebuildQuotaCounters(ctx, daily); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	// Day counters are set directly, one per settled day.
	if got := mustGet(t, client, dailyKeyA01); got != 150 {
		t.Fatalf("day key %s = %d, want 150", dailyKeyA01, got)
	}
	if got := mustGet(t, client, dailyKeyA02); got != 250 {
		t.Fatalf("day key %s = %d, want 250", dailyKeyA02, got)
	}
	if got := mustGet(t, client, dailyKeyB02); got != 700 {
		t.Fatalf("day key %s = %d, want 700", dailyKeyB02, got)
	}

	// Month counters are the sum of the settled days, and request counters are
	// the sum of the settled request counts. The monthly value is what proves
	// the "rebuild over multi-day history aggregates correctly" property.
	if got := mustGet(t, client, monthlyKeyA); got != 400 {
		t.Fatalf("monthly token key %s = %d, want 400 (150+250)", monthlyKeyA, got)
	}
	if got := mustGet(t, client, monthlyKeyB); got != 700 {
		t.Fatalf("monthly token key %s = %d, want 700", monthlyKeyB, got)
	}
	if got := mustGet(t, client, reqKeyA); got != 5 {
		t.Fatalf("monthly request key %s = %d, want 5 (3+2)", reqKeyA, got)
	}
	if got := mustGet(t, client, reqKeyB); got != 5 {
		t.Fatalf("monthly request key %s = %d, want 5", reqKeyB, got)
	}
}

// TestRebuildQuotaCountersResetsTheCache tests the "write back / reset" half
// of the semantic: a stale or inflated live counter (e.g. an in-flight
// reservation that never settled) must be replaced by the log-derived value,
// not accumulated on top of it.
func TestRebuildQuotaCountersResetsTheCache(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()

	const tenant = "cccccccc-0000-0000-0000-000000000003"
	dailyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", tenant, "20261005")
	monthlyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", tenant, "202610")
	t.Cleanup(func() {
		_ = client.Del(ctx, dailyKey, monthlyKey).Err()
	})

	// A counter that drifted 9999 tokens over the settled truth: the cache holds
	// reserved-but-never-settled usage, or was corrupted. The rebuild must reset
	// it to the log value, not add to it.
	if err := client.Set(ctx, dailyKey, 9999, 0).Err(); err != nil {
		t.Fatalf("seed daily: %v", err)
	}
	if err := client.Set(ctx, monthlyKey, 9999, 0).Err(); err != nil {
		t.Fatalf("seed monthly: %v", err)
	}

	limiter := New(client)
	if _, err := limiter.RebuildQuotaCounters(ctx, []model.QuotaSettledRow{
		{TenantID: tenant, Day: "20261005", Requests: 1, Tokens: 42},
	}); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if got := mustGet(t, client, dailyKey); got != 42 {
		t.Fatalf("daily key after rebuild = %d, want 42 (stale 9999 must be reset, not accumulated)", got)
	}
	if got := mustGet(t, client, monthlyKey); got != 42 {
		t.Fatalf("monthly key after rebuild = %d, want 42", got)
	}
}

func mustGet(t *testing.T, client *redis.Client, key string) int64 {
	t.Helper()
	v, err := client.Get(context.Background(), key).Int64()
	if err != nil {
		t.Fatalf("read key %s: %v", key, err)
	}
	return v
}

// TestRebuildQuotaCountersRunsWhenTheCounterIsMissing pins the "counter
// missing" trigger: when the gate finds the counter it is about to touch absent
// and a rebuild source is configured, it reconstructs the settled baseline from
// the log before reading it. Without this the gate would admit against zero
// after a cache flush and let a tenant over-run its quota.
func TestRebuildQuotaCountersRunsWhenTheCounterIsMissing(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()

	const tenant = "dddddddd-0000-0000-0000-000000000004"
	day := time.Now().UTC().Format("20060102")
	month := time.Now().UTC().Format("200601")
	dailyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", tenant, day)
	monthlyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", tenant, month)
	t.Cleanup(func() { _ = client.Del(ctx, dailyKey, monthlyKey).Err() })

	limiter := New(client)
	limiter.SetQuotaRebuildSource(func(context.Context) ([]model.QuotaSettledRow, error) {
		return []model.QuotaSettledRow{{TenantID: tenant, Day: day, Requests: 7, Tokens: 500}}, nil
	})

	// The key is absent, so the gate must consult the source before reading.
	if _, allowed, err := limiter.ReserveTenantTokens(ctx, tenant, 10, 1_000_000, 1_000_000); err != nil || !allowed {
		t.Fatalf("reserve: allowed=%v err=%v", allowed, err)
	}
	// 500 settled from the log + 10 just reserved. A gate that skipped the
	// rebuild would read 10.
	if got := mustGet(t, client, dailyKey); got != 510 {
		t.Fatalf("daily key = %d, want 510 (500 rebuilt from the log + 10 reserved)", got)
	}
	if got := mustGet(t, client, monthlyKey); got != 510 {
		t.Fatalf("monthly key = %d, want 510", got)
	}
}

// TestRebuildQuotaCountersLeavesPresentCountersAlone is the other half of the
// trigger: a counter that is already present must be trusted, not overwritten
// on every request. The source is deliberately poisoned so that any consult
// would be visible in the result.
func TestRebuildQuotaCountersLeavesPresentCountersAlone(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()

	const tenant = "eeeeeeee-0000-0000-0000-000000000005"
	day := time.Now().UTC().Format("20060102")
	month := time.Now().UTC().Format("200601")
	dailyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", tenant, day)
	monthlyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", tenant, month)
	t.Cleanup(func() { _ = client.Del(ctx, dailyKey, monthlyKey).Err() })

	if err := client.Set(ctx, dailyKey, 100, 0).Err(); err != nil {
		t.Fatalf("seed daily: %v", err)
	}
	if err := client.Set(ctx, monthlyKey, 100, 0).Err(); err != nil {
		t.Fatalf("seed monthly: %v", err)
	}

	limiter := New(client)
	consulted := false
	limiter.SetQuotaRebuildSource(func(context.Context) ([]model.QuotaSettledRow, error) {
		consulted = true
		return []model.QuotaSettledRow{{TenantID: tenant, Day: day, Tokens: 999999}}, nil
	})

	if _, allowed, err := limiter.ReserveTenantTokens(ctx, tenant, 5, 1_000_000, 1_000_000); err != nil || !allowed {
		t.Fatalf("reserve: allowed=%v err=%v", allowed, err)
	}
	if consulted {
		t.Fatal("the rebuild source must not be consulted while the counter is present")
	}
	if got := mustGet(t, client, dailyKey); got != 105 {
		t.Fatalf("daily key = %d, want 105 (100 present + 5 reserved, no rebuild)", got)
	}
}

// TestRebuildQuotaCountersDoesNotRebuildEveryRequest pins the fix for a rebuild
// storm. A tenant the log has no settled rows for has no counter because it has
// nothing to count, not because the cache was cleared; a missing key alone must
// not rebuild on every request. The rebuild arms the counter with its measured
// baseline — zero — so the next probe finds it present. The request here is
// larger than the whole limit, so the gate refuses it and never INCRBYs; without
// the baseline the counter would stay absent forever and reconstitute the whole
// month on each attempt.
func TestRebuildQuotaCountersDoesNotRebuildEveryRequest(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()

	const tenant = "ffffffff-0000-0000-0000-000000000006"
	day := time.Now().UTC().Format("20060102")
	month := time.Now().UTC().Format("200601")
	dailyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", tenant, day)
	monthlyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", tenant, month)
	t.Cleanup(func() { _ = client.Del(ctx, dailyKey, monthlyKey).Err() })
	// The baseline is armed per tenant key, so another test's counters cannot
	// disturb this one; only this tenant's keys matter.
	_ = client.Del(ctx, dailyKey, monthlyKey).Err()

	limiter := New(client)
	calls := 0
	limiter.SetQuotaRebuildSource(func(context.Context) ([]model.QuotaSettledRow, error) {
		calls++
		return nil, nil // this tenant has settled nothing this month
	})

	for i := 0; i < 3; i++ {
		if _, allowed, err := limiter.ReserveTenantTokens(ctx, tenant, 200, 100, 100); err != nil || allowed {
			t.Fatalf("attempt %d: allowed=%v err=%v, want denied", i, allowed, err)
		}
	}
	if calls != 1 {
		t.Fatalf("rebuild source consulted %d times, want 1: a legitimately absent counter must not rebuild on every request", calls)
	}
}

// TestRebuildQuotaCountersRebuildsAfterAGenuineClear is the other half of the
// trigger. The armed baseline is not a permanent "trust me" flag: when the cache
// really is cleared — the counter and the baseline go together — the next
// request must rebuild from the log again.
func TestRebuildQuotaCountersRebuildsAfterAGenuineClear(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()

	const tenant = "99999999-0000-0000-0000-000000000009"
	day := time.Now().UTC().Format("20060102")
	month := time.Now().UTC().Format("200601")
	dailyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", tenant, day)
	monthlyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", tenant, month)
	t.Cleanup(func() { _ = client.Del(ctx, dailyKey, monthlyKey).Err() })
	_ = client.Del(ctx, dailyKey, monthlyKey).Err()

	limiter := New(client)
	calls := 0
	limiter.SetQuotaRebuildSource(func(context.Context) ([]model.QuotaSettledRow, error) {
		calls++
		return []model.QuotaSettledRow{{TenantID: tenant, Day: day, Requests: 1, Tokens: 500}}, nil
	})

	if _, allowed, err := limiter.ReserveTenantTokens(ctx, tenant, 10, 1_000_000, 1_000_000); err != nil || !allowed {
		t.Fatalf("first reserve: allowed=%v err=%v", allowed, err)
	}
	if calls != 1 {
		t.Fatalf("rebuild source consulted %d times, want 1", calls)
	}
	if got := mustGet(t, client, dailyKey); got != 510 {
		t.Fatalf("daily key = %d, want 510 (500 settled + 10 reserved)", got)
	}

	// A flush takes the counter and the baseline at once.
	if err := client.Del(ctx, dailyKey, monthlyKey).Err(); err != nil {
		t.Fatalf("clear cache: %v", err)
	}

	if _, allowed, err := limiter.ReserveTenantTokens(ctx, tenant, 10, 1_000_000, 1_000_000); err != nil || !allowed {
		t.Fatalf("second reserve: allowed=%v err=%v", allowed, err)
	}
	if calls != 2 {
		t.Fatalf("rebuild source consulted %d times, want 2: a cleared cache must rebuild again", calls)
	}
	if got := mustGet(t, client, dailyKey); got != 510 {
		t.Fatalf("daily key after a real clear = %d, want 510", got)
	}
}
