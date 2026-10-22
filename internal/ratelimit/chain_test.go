package ratelimit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The chain checks are the part of the limiter that cannot be verified by
// reading Go code: whether a limit is inclusive across a hierarchy, whether a
// rejection leaves the cache untouched, and which level a rejection names are
// all observable only in Redis. These tests run against a real server and skip
// when none answers.

func chainTestLimiter(t *testing.T) (*Limiter, func(keys ...string)) {
	t.Helper()
	client := redisTestClient(t)
	limiter := New(client)
	cleanup := func(keys ...string) {
		t.Cleanup(func() {
			_ = client.Del(context.Background(), keys...).Err()
		})
	}
	return limiter, cleanup
}

func int64Ptr(v int64) *int64 { return &v }

func mustExists(t *testing.T, l *Limiter, key string) int64 {
	t.Helper()
	got, err := l.rdb.Exists(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("exists %s: %v", key, err)
	}
	return got
}

func mustValue(t *testing.T, l *Limiter, key string) int64 {
	t.Helper()
	got, err := l.rdb.Get(context.Background(), key).Int64()
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	return got
}

// The parent cap bounds the whole subtree: the child inherits (nil) and gets no
// counter of its own, while the parent's counter sees every child request and
// names itself in the rejection.
func TestRequestChainCountsSubtreeUsageAtTheCappedAncestor(t *testing.T) {
	limiter, cleanup := chainTestLimiter(t)
	ctx := context.Background()

	root := fmt.Sprintf("chain-root-%d", time.Now().UnixNano())
	child := root + "-child"
	month := tenantMonthBucket()
	rootKey := fmt.Sprintf("gw:quota:tenant:%s:requests:%s", root, month)
	childKey := fmt.Sprintf("gw:quota:tenant:%s:requests:%s", child, month)
	cleanup(rootKey, childKey)

	chain := []model.TenantQuota{
		{TenantID: root, Name: "计算机学院", MonthlyRequestLimit: int64Ptr(2)},
		{TenantID: child, Name: "软件系"},
	}

	for i := 0; i < 2; i++ {
		allowed, rejected, err := limiter.AllowTenantRequestChain(ctx, chain)
		if err != nil {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
		if !allowed || rejected != nil {
			t.Fatalf("attempt %d refused with %+v", i+1, rejected)
		}
	}
	allowed, rejected, err := limiter.AllowTenantRequestChain(ctx, chain)
	if err != nil {
		t.Fatalf("third attempt: %v", err)
	}
	if allowed {
		t.Fatal("the third request must be refused by the parent's cap")
	}
	if rejected == nil || rejected.TenantID != root || rejected.Label != "租户:计算机学院" {
		t.Fatalf("rejection should name the capped ancestor, got %+v", rejected)
	}
	if got := mustValue(t, limiter, rootKey); got != 2 {
		t.Fatalf("parent counter = %d, want 2; a refused request must not be charged", got)
	}
	if mustExists(t, limiter, childKey) != 0 {
		t.Fatal("an inheriting child must not get a counter of its own")
	}
}

// Reserving at every level is what makes the hierarchy inclusive. A child's
// usage is charged to the parent in the same atomic call, and a settlement
// adjusts every level by the same delta so the levels cannot drift apart.
func TestTokenChainChargesEveryCappedLevelAndSettlesThemTogether(t *testing.T) {
	limiter, cleanup := chainTestLimiter(t)
	ctx := context.Background()

	root := fmt.Sprintf("chain-token-%d", time.Now().UnixNano())
	child := root + "-child"
	day := tenantDayBucket()
	month := tenantMonthBucket()
	rootMonthKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", root, month)
	childDayKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", child, day)
	childMonthKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", child, month)
	cleanup(rootMonthKey, childDayKey, childMonthKey)

	chain := []model.TenantQuota{
		{TenantID: root, Name: "计算机学院", MonthlyTokenLimit: int64Ptr(1000)},
		{TenantID: child, Name: "软件系", DailyTokenLimit: int64Ptr(800), MonthlyTokenLimit: int64Ptr(800)},
	}

	reservation, allowed, rejected, err := limiter.ReserveTokenChain(ctx, chain, nil, 100)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if !allowed || rejected != nil || reservation == nil {
		t.Fatalf("reserve refused: allowed=%v rejection=%+v", allowed, rejected)
	}
	for _, key := range []string{rootMonthKey, childDayKey, childMonthKey} {
		if got := mustValue(t, limiter, key); got != 100 {
			t.Fatalf("%s = %d, want 100", key, got)
		}
	}
	rootDayKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", root, day)
	if mustExists(t, limiter, rootDayKey) != 0 {
		t.Fatal("an uncapped daily dimension must not get a counter")
	}

	// The upstream reported more than the estimate: every level moves by the
	// same delta, so the parent's total still equals the subtree's.
	if err := reservation.Reconcile(ctx, 160); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, key := range []string{rootMonthKey, childDayKey, childMonthKey} {
		if got := mustValue(t, limiter, key); got != 160 {
			t.Fatalf("%s after reconcile = %d, want 160", key, got)
		}
	}
}

// The model axis joins the same atomic script as the tenant axis. A rejection
// from the campus-wide pool must leave the tenant's own counters untouched:
// half-charging a request the gateway then refuses would make the tenant pay
// for traffic it never received.
func TestModelPoolRejectionWritesNothingToTheTenantCounters(t *testing.T) {
	limiter, cleanup := chainTestLimiter(t)
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	tenant := fmt.Sprintf("chain-model-tenant-%d", suffix)
	alias := fmt.Sprintf("chain-model-alias-%d", suffix)
	day := tenantDayBucket()
	tenantKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", tenant, day)
	poolKey := fmt.Sprintf("gw:quota:model:%s:tokens:day:%s", alias, day)
	cleanup(tenantKey, poolKey)

	chain := []model.TenantQuota{
		{TenantID: tenant, Name: "软件系", DailyTokenLimit: int64Ptr(1000)},
	}
	levels := []model.ModelQuotaLevel{
		{Label: fmt.Sprintf("模型:%s(全校)", alias), ModelAlias: alias, DailyTokenLimit: int64Ptr(50)},
	}

	reservation, allowed, rejected, err := limiter.ReserveTokenChain(ctx, chain, levels, 60)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if allowed || reservation != nil {
		t.Fatal("a request over the pool cap must be refused")
	}
	if rejected == nil || rejected.ModelAlias != alias || rejected.TenantID != "" {
		t.Fatalf("rejection should name the system pool, got %+v", rejected)
	}
	if mustExists(t, limiter, tenantKey) != 0 {
		t.Fatal("the tenant counter was charged for a refused request")
	}
	if mustExists(t, limiter, poolKey) != 0 {
		t.Fatal("the model pool counter was charged for a refused request")
	}
}

// Both three-state readings that mean "no cap" — inherit (NULL) and unlimited
// (0) — must reserve nothing and write nothing. A counter created for an
// uncapped dimension would start a later cap change from an invisible baseline.
func TestUncappedChainReservesNothing(t *testing.T) {
	limiter, cleanup := chainTestLimiter(t)
	ctx := context.Background()

	tenant := fmt.Sprintf("chain-nocap-%d", time.Now().UnixNano())
	alias := "chain-nocap-model"
	month := tenantMonthBucket()
	day := tenantDayBucket()
	keys := []string{
		fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", tenant, month),
		fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", tenant, day),
		fmt.Sprintf("gw:quota:model:%s:tokens:month:%s", alias, month),
	}
	cleanup(keys...)

	chain := []model.TenantQuota{
		{TenantID: tenant, Name: "继承", MonthlyTokenLimit: nil},
		{TenantID: tenant + "-unlimited", Name: "不限", DailyTokenLimit: int64Ptr(0), MonthlyTokenLimit: int64Ptr(0)},
	}
	levels := []model.ModelQuotaLevel{
		{Label: "模型:" + alias, ModelAlias: alias, MonthlyTokenLimit: int64Ptr(0)},
	}

	reservation, allowed, rejected, err := limiter.ReserveTokenChain(ctx, chain, levels, 500)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if !allowed || rejected != nil || reservation != nil {
		t.Fatalf("uncapped chain must admit without a reservation: allowed=%v rejection=%+v reservation=%+v", allowed, rejected, reservation)
	}
	for _, key := range keys {
		if mustExists(t, limiter, key) != 0 {
			t.Fatalf("uncapped chain wrote %s", key)
		}
	}
}
