package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type testEnv struct {
	limiter *Limiter
	rdb     *redis.Client
	mr      *miniredis.Miniredis
}

func newTestEnv(t *testing.T) testEnv {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = rdb.Close()
		mr.Close()
	})
	return testEnv{limiter: New(rdb), rdb: rdb, mr: mr}
}

func readCounter(t *testing.T, rdb *redis.Client, key string) int64 {
	t.Helper()
	v, err := rdb.Get(context.Background(), key).Int64()
	if errors.Is(err, redis.Nil) {
		return 0
	}
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return v
}

// --- helpers -----------------------------------------------------------------

// tokenLevel builds a chain entry that caps tokens only.
func tokenLevel(id string, daily, monthly int64) model.TenantQuota {
	return model.TenantQuota{TenantID: id, DailyTokenLimit: daily, MonthlyTokenLimit: monthly}
}

// requestLevel builds a chain entry that caps requests only.
func requestLevel(id string, monthly int64) model.TenantQuota {
	return model.TenantQuota{TenantID: id, MonthlyRequestLimit: monthly}
}

func dayKey(id string) string {
	return fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", id, tenantDayBucket())
}

func monthKey(id string) string {
	return fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", id, tenantMonthBucket())
}

func requestsKey(id string) string {
	return fmt.Sprintf("gw:quota:tenant:%s:requests:%s", id, tenantMonthBucket())
}

// reserveTenant reserves and fails the test on an unexpected rejection.
func reserveTenant(t *testing.T, env testEnv, amount int64, chain ...model.TenantQuota) *TenantChainReservation {
	t.Helper()
	res, allowed, rejected, err := env.limiter.ReserveTenantTokensChain(context.Background(), chain, amount)
	if err != nil {
		t.Fatalf("reserve tenant tokens: %v", err)
	}
	if !allowed {
		t.Fatalf("reserve tenant tokens: unexpectedly rejected by %q (amount=%d)", rejected, amount)
	}
	if res == nil {
		t.Fatalf("reserve tenant tokens: got nil reservation for amount=%d", amount)
	}
	return res
}

// --- token reservation -------------------------------------------------------

// A chain whose levels all have no token cap must not touch Redis at all -- this
// is the branch that keeps an unconfigured deployment from ever reserving, and
// therefore from ever leaking.
func TestReserveTenantTokensSkipsWhenNoLimitsConfigured(t *testing.T) {
	env := newTestEnv(t)

	res, allowed, rejected, err := env.limiter.ReserveTenantTokensChain(
		context.Background(), []model.TenantQuota{tokenLevel("tenant-1", 0, 0)}, 500,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatalf("expected unlimited tenant to be allowed, rejected by %q", rejected)
	}
	if res != nil {
		t.Fatal("expected nil reservation when no limits are configured")
	}
	if keys := env.mr.Keys(); len(keys) != 0 {
		t.Fatalf("expected no Redis keys, got %v", keys)
	}
}

func TestReserveTenantTokensRejectsOverDailyLimitWithoutWriting(t *testing.T) {
	env := newTestEnv(t)

	_, allowed, _, err := env.limiter.ReserveTenantTokensChain(
		context.Background(), []model.TenantQuota{tokenLevel("tenant-1", 50, 10000)}, 100,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected rejection when the estimate exceeds the daily limit")
	}
	if keys := env.mr.Keys(); len(keys) != 0 {
		t.Fatalf("rejected reservation must not write counters, got %v", keys)
	}
}

// The inclusive-hierarchy property: one reservation lands on the leaf and on
// every ancestor, so a parent's counter is the sum of its subtree by
// construction and needs no separate rollup.
func TestReserveTenantTokensChainWritesEveryLevel(t *testing.T) {
	env := newTestEnv(t)

	res := reserveTenant(t, env, 100,
		tokenLevel("school", 100000, 1000000),
		tokenLevel("teacher", 10000, 100000),
		tokenLevel("student", 1000, 10000),
	)

	for _, id := range []string{"school", "teacher", "student"} {
		if got := readCounter(t, env.rdb, dayKey(id)); got != 100 {
			t.Fatalf("daily counter for %s = %d, want 100", id, got)
		}
		if got := readCounter(t, env.rdb, monthKey(id)); got != 100 {
			t.Fatalf("monthly counter for %s = %d, want 100", id, got)
		}
	}
	if len(res.keys) != 6 {
		t.Fatalf("reservation tracks %d keys, want 6 (daily+monthly for 3 levels)", len(res.keys))
	}
}

// The critical ordering guarantee. A rejection must be decided before any write,
// otherwise a student repeatedly hitting their own cap would keep draining their
// teacher's and their department's budgets while being told "no".
func TestReserveTenantTokensChainRejectionByAncestorWritesNothing(t *testing.T) {
	env := newTestEnv(t)

	_, allowed, rejected, err := env.limiter.ReserveTenantTokensChain(context.Background(), []model.TenantQuota{
		tokenLevel("school", 100000, 1000000),
		tokenLevel("teacher", 500, 100000), // the ancestor that runs out first
		tokenLevel("student", 1000, 10000),
	}, 600)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected rejection: the estimate exceeds the teacher's daily limit")
	}
	if rejected != "teacher" {
		t.Fatalf("rejected by %q, want %q", rejected, "teacher")
	}
	if keys := env.mr.Keys(); len(keys) != 0 {
		t.Fatalf("a rejection must not write any level, got %v", keys)
	}
}

// The leaf is checked too, and is reported as the cause when it is the one that
// runs out.
func TestReserveTenantTokensChainRejectionByLeafIsReported(t *testing.T) {
	env := newTestEnv(t)

	_, allowed, rejected, err := env.limiter.ReserveTenantTokensChain(context.Background(), []model.TenantQuota{
		tokenLevel("school", 100000, 1000000),
		tokenLevel("student", 200, 10000),
	}, 300)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected rejection: the estimate exceeds the student's daily limit")
	}
	if rejected != "student" {
		t.Fatalf("rejected by %q, want %q", rejected, "student")
	}
}

// A level with no cap is skipped entirely rather than being given a pair of
// counters, so an unlimited middle level does not slow the chain down.
func TestReserveTenantTokensChainSkipsUncappedLevels(t *testing.T) {
	env := newTestEnv(t)

	res := reserveTenant(t, env, 100,
		tokenLevel("school", 100000, 1000000),
		tokenLevel("dept", 0, 0), // uncapped: must not appear in the chain
		tokenLevel("student", 1000, 10000),
	)

	if len(res.keys) != 4 {
		t.Fatalf("reservation tracks %d keys, want 4 (the uncapped level is skipped)", len(res.keys))
	}
	if n := env.rdb.Exists(context.Background(), dayKey("dept")).Val(); n != 0 {
		t.Fatal("an uncapped level must not get a counter")
	}
}

// A single-level chain must behave exactly like the old flat tenant check.
func TestReserveTenantTokensChainSingleLevel(t *testing.T) {
	env := newTestEnv(t)

	res := reserveTenant(t, env, 100, tokenLevel("tenant-1", 1000, 10000))

	if got := readCounter(t, env.rdb, res.keys[0]); got != 100 {
		t.Fatalf("after reserve: daily = %d, want 100", got)
	}
	if got := readCounter(t, env.rdb, res.keys[1]); got != 100 {
		t.Fatalf("after reserve: monthly = %d, want 100", got)
	}
}

// The happy path: the estimate is corrected down to the real usage, at every
// level.
func TestTenantChainReconcileCorrectsEstimateToActualUsage(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := reserveTenant(t, env, 100,
		tokenLevel("school", 100000, 1000000),
		tokenLevel("student", 1000, 10000),
	)

	if err := res.Reconcile(ctx, 40); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, id := range []string{"school", "student"} {
		if got := readCounter(t, env.rdb, dayKey(id)); got != 40 {
			t.Fatalf("after reconcile: daily for %s = %d, want 40", id, got)
		}
		if got := readCounter(t, env.rdb, monthKey(id)); got != 40 {
			t.Fatalf("after reconcile: monthly for %s = %d, want 40", id, got)
		}
	}
}

func TestTenantChainRefundReleasesTheFullEstimate(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := reserveTenant(t, env, 100, tokenLevel("tenant-1", 1000, 10000))

	if err := res.Refund(ctx); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if got := readCounter(t, env.rdb, res.keys[0]); got != 0 {
		t.Fatalf("after refund: daily = %d, want 0", got)
	}
	if got := readCounter(t, env.rdb, res.keys[1]); got != 0 {
		t.Fatalf("after refund: monthly = %d, want 0", got)
	}
}

// The core regression guard. A request can be settled twice -- for example the
// rejection path refunds explicitly and the deferred settle then refunds again.
// Only the first settle may take effect, otherwise the counter is driven below
// the real usage and the tenant receives free quota.
func TestTenantChainReconcileThenRefundIsIdempotent(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := reserveTenant(t, env, 100, tokenLevel("tenant-1", 1000, 10000))

	if err := res.Reconcile(ctx, 40); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := res.Refund(ctx); err != nil {
		t.Fatalf("refund after reconcile: %v", err)
	}

	if got := readCounter(t, env.rdb, res.keys[0]); got != 40 {
		t.Fatalf("daily = %d, want 40 (the reconcile must win, not be undone)", got)
	}
	if got := readCounter(t, env.rdb, res.keys[1]); got != 40 {
		t.Fatalf("monthly = %d, want 40 (the reconcile must win, not be undone)", got)
	}
}

func TestTenantChainRefundThenReconcileIsIdempotent(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := reserveTenant(t, env, 100, tokenLevel("tenant-1", 1000, 10000))

	if err := res.Refund(ctx); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if err := res.Reconcile(ctx, 40); err != nil {
		t.Fatalf("reconcile after refund: %v", err)
	}

	if got := readCounter(t, env.rdb, res.keys[0]); got != 0 {
		t.Fatalf("daily = %d, want 0 (the refund must win, not be undone)", got)
	}
}

func TestTenantChainDoubleRefundIsIdempotent(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := reserveTenant(t, env, 100, tokenLevel("tenant-1", 1000, 10000))

	if err := res.Refund(ctx); err != nil {
		t.Fatalf("first refund: %v", err)
	}
	if err := res.Refund(ctx); err != nil {
		t.Fatalf("second refund: %v", err)
	}

	if got := readCounter(t, env.rdb, res.keys[0]); got != 0 {
		t.Fatalf("daily = %d, want 0 (second refund must be a no-op)", got)
	}
}

// A reservation can outlive its bucket. A plain DECRBY would recreate the key
// as a negative number, which reads as free quota for the rest of the period.
func TestTenantChainRefundAfterBucketExpiryDoesNotGoNegative(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := reserveTenant(t, env, 100,
		tokenLevel("school", 100000, 1000000),
		tokenLevel("student", 1000, 10000),
	)

	env.mr.FastForward(41 * 24 * time.Hour)

	if err := res.Refund(ctx); err != nil {
		t.Fatalf("refund after expiry: %v", err)
	}

	for _, key := range res.keys {
		if n := env.rdb.Exists(ctx, key).Val(); n != 0 {
			t.Fatalf("expired key %s was resurrected as %d", key, readCounter(t, env.rdb, key))
		}
	}
}

// The reconcile path can also decrease, and must clamp the same way.
func TestTenantChainReconcileAfterBucketExpiryDoesNotGoNegative(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := reserveTenant(t, env, 100, tokenLevel("tenant-1", 1000, 10000))

	env.mr.FastForward(41 * 24 * time.Hour)

	if err := res.Reconcile(ctx, 10); err != nil {
		t.Fatalf("reconcile after expiry: %v", err)
	}

	for _, key := range res.keys {
		if n := env.rdb.Exists(ctx, key).Val(); n != 0 {
			t.Fatalf("expired key %s was resurrected as %d", key, readCounter(t, env.rdb, key))
		}
	}
}

// --- monthly request cap -----------------------------------------------------

func TestAllowTenantRequestChainCountsEveryLevel(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	chain := []model.TenantQuota{
		requestLevel("school", 100),
		requestLevel("student", 5),
	}

	for i := 0; i < 5; i++ {
		allowed, rejected, err := env.limiter.AllowTenantRequestChain(ctx, chain)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if !allowed {
			t.Fatalf("call %d rejected by %v, want allowed", i, rejected)
		}
	}

	allowed, rejected, err := env.limiter.AllowTenantRequestChain(ctx, chain)
	if err != nil {
		t.Fatalf("sixth call: %v", err)
	}
	if allowed {
		t.Fatal("expected the sixth call to be rejected by the student's cap")
	}
	if rejected == nil || rejected.TenantID != "student" {
		t.Fatalf("rejected by %v, want the student level", rejected)
	}
	// The ancestor must have been charged for the five accepted calls and no
	// more: the rejected one is decided before any write.
	if got := readCounter(t, env.rdb, requestsKey("school")); got != 5 {
		t.Fatalf("school request counter = %d, want 5 (a rejected call must not count)", got)
	}
}

func TestAllowTenantRequestChainRejectionByAncestor(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	chain := []model.TenantQuota{
		requestLevel("school", 2),
		requestLevel("student", 100),
	}

	for i := 0; i < 2; i++ {
		allowed, rejected, err := env.limiter.AllowTenantRequestChain(ctx, chain)
		if err != nil || !allowed {
			t.Fatalf("call %d: allowed=%v rejected=%v err=%v", i, allowed, rejected, err)
		}
	}

	allowed, rejected, err := env.limiter.AllowTenantRequestChain(ctx, chain)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if allowed {
		t.Fatal("expected the school's cap to reject the third call")
	}
	if rejected == nil || rejected.TenantID != "school" {
		t.Fatalf("rejected by %v, want the school level", rejected)
	}
}

func TestAllowTenantRequestChainSkipsWhenUncapped(t *testing.T) {
	env := newTestEnv(t)

	allowed, rejected, err := env.limiter.AllowTenantRequestChain(
		context.Background(), []model.TenantQuota{requestLevel("tenant-1", 0)},
	)
	if err != nil || !allowed {
		t.Fatalf("expected an uncapped chain to be allowed, got allowed=%v rejected=%v err=%v", allowed, rejected, err)
	}
	if keys := env.mr.Keys(); len(keys) != 0 {
		t.Fatalf("an uncapped chain must not write counters, got %v", keys)
	}
}

// --- per-key TPM -------------------------------------------------------------

func reserveTPM(t *testing.T, env testEnv, keyID string, amount, limit int64) *TPMReservation {
	t.Helper()
	res, allowed, err := env.limiter.ReserveTPM(context.Background(), keyID, amount, limit)
	if err != nil {
		t.Fatalf("reserve tpm: %v", err)
	}
	if !allowed {
		t.Fatalf("reserve tpm: unexpectedly rejected (amount=%d limit=%d)", amount, limit)
	}
	if res == nil {
		t.Fatalf("reserve tpm: got nil reservation for amount=%d", amount)
	}
	return res
}

func TestTPMReserveOverLimitRollsBack(t *testing.T) {
	env := newTestEnv(t)

	_, allowed, err := env.limiter.ReserveTPM(context.Background(), "key-1", 100, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected rejection when the estimate exceeds the tpm limit")
	}

	key := "gw:rl:key:key-1:tpm:" + minuteBucket()
	if got := readCounter(t, env.rdb, key); got != 0 {
		t.Fatalf("tpm counter = %d after a rejected reserve, want 0", got)
	}
}

func TestTPMReconcileThenRefundIsIdempotent(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := reserveTPM(t, env, "key-1", 100, 10000)

	if got := readCounter(t, env.rdb, res.key); got != 100 {
		t.Fatalf("after reserve: tpm = %d, want 100", got)
	}
	if err := res.Reconcile(ctx, 30); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := res.Refund(ctx); err != nil {
		t.Fatalf("refund after reconcile: %v", err)
	}

	if got := readCounter(t, env.rdb, res.key); got != 30 {
		t.Fatalf("tpm = %d, want 30 (the reconcile must win)", got)
	}
}

func TestTPMRefundAfterBucketExpiryDoesNotGoNegative(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	res := reserveTPM(t, env, "key-1", 100, 10000)

	env.mr.FastForward(3 * time.Minute)

	if err := res.Refund(ctx); err != nil {
		t.Fatalf("refund after expiry: %v", err)
	}
	if n := env.rdb.Exists(ctx, res.key).Val(); n != 0 {
		t.Fatalf("expired tpm key was resurrected as %d", readCounter(t, env.rdb, res.key))
	}
}

// Nil reservations are reachable: /v1/models returns before reserving, and the
// rejection paths return with only one of the two reservations populated. The
// deferred settle calls both unconditionally, so both must tolerate nil.
func TestNilReservationsAreSafeToSettle(t *testing.T) {
	ctx := context.Background()

	var tpm *TPMReservation
	if err := tpm.Reconcile(ctx, 10); err != nil {
		t.Fatalf("nil TPMReconcile: %v", err)
	}
	if err := tpm.Refund(ctx); err != nil {
		t.Fatalf("nil TPMRefund: %v", err)
	}

	var tenant *TenantChainReservation
	if err := tenant.Reconcile(ctx, 10); err != nil {
		t.Fatalf("nil Reconcile: %v", err)
	}
	if err := tenant.Refund(ctx); err != nil {
		t.Fatalf("nil Refund: %v", err)
	}
}
