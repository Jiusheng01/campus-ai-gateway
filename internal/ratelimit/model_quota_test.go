package ratelimit

import (
	"context"
	"fmt"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// --- helpers -----------------------------------------------------------------

func modelDayKey(alias string) string {
	return fmt.Sprintf("gw:quota:model:%s:tokens:day:%s", alias, tenantDayBucket())
}

func modelMonthKey(alias string) string {
	return fmt.Sprintf("gw:quota:model:%s:tokens:month:%s", alias, tenantMonthBucket())
}

func tenantModelDayKey(tenantID, alias string) string {
	return fmt.Sprintf("gw:quota:tenant:%s:model:%s:tokens:day:%s", tenantID, alias, tenantDayBucket())
}

func tenantModelMonthKey(tenantID, alias string) string {
	return fmt.Sprintf("gw:quota:tenant:%s:model:%s:tokens:month:%s", tenantID, alias, tenantMonthBucket())
}

// modelLevel builds a resolved model chain entry.
func modelLevel(label, tenantID, alias string, daily, monthly int64) model.ModelQuotaLevel {
	return model.ModelQuotaLevel{
		Label:             label,
		TenantID:          tenantID,
		ModelAlias:        alias,
		DailyTokenLimit:   daily,
		MonthlyTokenLimit: monthly,
	}
}

// combined mirrors what the data plane does: the tenant chain and the model
// chain are reserved in a single call, tenant levels first.
func combined(tenantChain []model.TenantQuota, modelLevels []model.ModelQuotaLevel) []QuotaLevel {
	out := TenantQuotaLevels(tenantChain)
	return append(out, ModelQuotaLevels(modelLevels)...)
}

func reserveCombined(t *testing.T, env testEnv, amount int64, levels []QuotaLevel) (*TokenChainReservation, bool, *QuotaRejection) {
	t.Helper()
	res, allowed, rejection, err := env.limiter.ReserveTokenChain(context.Background(), levels, amount)
	if err != nil {
		t.Fatalf("reserve token chain: %v", err)
	}
	return res, allowed, rejection
}

// --- key layout --------------------------------------------------------------

// The three bucket namespaces must stay distinct. If a tenant's total usage and
// its per-model usage ever landed on the same key, a per-model cap would silently
// spend the tenant's whole budget -- and the mistake would only show up as a
// tenant mysteriously running out early.
func TestQuotaLevelKeyLayoutIsUnambiguous(t *testing.T) {
	tenant := QuotaLevel{TenantID: "t1"}
	pool := QuotaLevel{ModelAlias: "campus-smart"}
	both := QuotaLevel{TenantID: "t1", ModelAlias: "campus-smart"}

	day := "20261013"
	got := map[string]string{
		"tenant": tenant.redisKey("day", day),
		"pool":   pool.redisKey("day", day),
		"both":   both.redisKey("day", day),
	}
	want := map[string]string{
		"tenant": "gw:quota:tenant:t1:tokens:day:20261013",
		"pool":   "gw:quota:model:campus-smart:tokens:day:20261013",
		"both":   "gw:quota:tenant:t1:model:campus-smart:tokens:day:20261013",
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s key = %q, want %q", k, got[k], v)
		}
	}
	seen := map[string]string{}
	for k, v := range got {
		if prev, dup := seen[v]; dup {
			t.Fatalf("%s and %s share the key %q", k, prev, v)
		}
		seen[v] = k
	}
}

// The tenant key must remain byte-for-byte what the gateway wrote before the
// model axis existed, otherwise every live deployment's counters reset to zero
// on upgrade and a tenant that had already spent its budget gets a fresh one.
func TestTenantQuotaKeyIsUnchanged(t *testing.T) {
	lv := QuotaLevel{TenantID: "tenant-1"}
	// The bucket is spelled out rather than taken from dayKey()/monthKey(): the
	// subject of this test is the literal layout, and a bucket derived from the
	// wall clock would make the assertion pass only on the day it was written.
	if got, want := lv.redisKey("day", "20260101"), "gw:quota:tenant:tenant-1:tokens:day:20260101"; got != want {
		t.Fatalf("tenant day key = %q, want the historical %q", got, want)
	}
	if got, want := lv.redisKey("month", "202601"), "gw:quota:tenant:tenant-1:tokens:month:202601"; got != want {
		t.Fatalf("tenant month key = %q, want the historical %q", got, want)
	}
}

// --- system pool -------------------------------------------------------------

// The shared pool is the root of the model chain: every tenant's usage of that
// model lands on it, so it is the one cap no tenant can escape.
func TestSystemPoolIsChargedByEveryReservation(t *testing.T) {
	env := newTestEnv(t)

	levels := combined(
		[]model.TenantQuota{tokenLevel("school", 100000, 1000000)},
		[]model.ModelQuotaLevel{modelLevel("模型:campus-smart(全校)", "", "campus-smart", 1000, 0)},
	)

	reserveCombined(t, env, 300, levels)
	reserveCombined(t, env, 200, levels)

	if got := readCounter(t, env.rdb, modelDayKey("campus-smart")); got != 500 {
		t.Fatalf("shared pool counter = %d, want 500", got)
	}
	if got := readCounter(t, env.rdb, dayKey("school")); got != 500 {
		t.Fatalf("tenant counter = %d, want 500", got)
	}
}

func TestSystemPoolRejectionNamesThePoolAndNotATenant(t *testing.T) {
	env := newTestEnv(t)

	levels := combined(
		[]model.TenantQuota{tokenLevel("school", 100000, 1000000)},
		[]model.ModelQuotaLevel{modelLevel("模型:campus-smart(全校)", "", "campus-smart", 100, 0)},
	)

	_, allowed, rejection := reserveCombined(t, env, 100, levels)
	if !allowed {
		t.Fatal("the first reservation should fit exactly")
	}

	_, allowed, rejection = reserveCombined(t, env, 1, levels)
	if allowed {
		t.Fatal("expected the shared pool to reject the second reservation")
	}
	if rejection == nil {
		t.Fatal("a rejection must name the level that refused")
	}
	if rejection.Label != "模型:campus-smart(全校)" {
		t.Fatalf("rejected label = %q, want the pool label", rejection.Label)
	}
	// The pool belongs to no tenant. Reporting one would file the event against
	// an innocent department.
	if rejection.TenantID != "" {
		t.Fatalf("rejected tenant = %q, want empty for a system-wide pool", rejection.TenantID)
	}
	if rejection.ModelAlias != "campus-smart" {
		t.Fatalf("rejected model = %q, want campus-smart", rejection.ModelAlias)
	}
}

// A rejection on either axis must write nothing at all, on either axis. This is
// the property the single-script design exists to provide: a caller who keeps
// retrying past the shared pool must not be burning down their own counters (or
// anyone else's) while being told no.
func TestRejectionOnEitherAxisWritesNothing(t *testing.T) {
	env := newTestEnv(t)

	// The tenant is capped at 100; the model pool is generous. The rejection
	// must come from the tenant and must leave the model pool untouched.
	levels := combined(
		[]model.TenantQuota{tokenLevel("school", 100, 1000)},
		[]model.ModelQuotaLevel{modelLevel("模型:campus-smart(全校)", "", "campus-smart", 100000, 0)},
	)

	_, allowed, _ := reserveCombined(t, env, 100, levels)
	if !allowed {
		t.Fatal("the first reservation should fit exactly")
	}
	_, allowed, rejection := reserveCombined(t, env, 1, levels)
	if allowed {
		t.Fatal("expected the tenant cap to reject")
	}
	if rejection == nil || rejection.Label != "租户:school" {
		t.Fatalf("rejected = %v, want the tenant level", rejection)
	}
	if got := readCounter(t, env.rdb, modelDayKey("campus-smart")); got != 100 {
		t.Fatalf("shared pool counter = %d, want 100 (a rejected request must not charge it)", got)
	}
	if got := readCounter(t, env.rdb, dayKey("school")); got != 100 {
		t.Fatalf("tenant counter = %d, want 100", got)
	}
}

// When both the shared pool and the tenant's own level would reject, the pool is
// reported. It being exhausted affects everyone, so it is the more actionable
// fact -- and the caller cannot infer it from their own numbers.
func TestSharedPoolIsReportedWhenBothWouldReject(t *testing.T) {
	env := newTestEnv(t)

	levels := combined(
		[]model.TenantQuota{tokenLevel("school", 100000, 1000000)},
		[]model.ModelQuotaLevel{
			modelLevel("模型:campus-smart(全校)", "", "campus-smart", 100, 0),
			modelLevel("租户:计算机学院/模型:campus-smart", "dept-1", "campus-smart", 100, 0),
		},
	)

	_, allowed, _ := reserveCombined(t, env, 100, levels)
	if !allowed {
		t.Fatal("the first reservation should fit exactly")
	}
	_, allowed, rejection := reserveCombined(t, env, 1, levels)
	if allowed {
		t.Fatal("expected a rejection")
	}
	if rejection == nil || rejection.Label != "模型:campus-smart(全校)" {
		t.Fatalf("rejected = %v, want the shared pool (it is placed first for exactly this reason)", rejection)
	}
}

// --- tenant model level ------------------------------------------------------

func TestTenantModelLevelIsChargedAndNamed(t *testing.T) {
	env := newTestEnv(t)

	levels := combined(
		[]model.TenantQuota{tokenLevel("dept-1", 100000, 1000000)},
		[]model.ModelQuotaLevel{
			modelLevel("模型:campus-smart(全校)", "", "campus-smart", 100000, 0),
			modelLevel("租户:计算机学院/模型:campus-smart", "dept-1", "campus-smart", 200, 0),
		},
	)

	_, allowed, _ := reserveCombined(t, env, 200, levels)
	if !allowed {
		t.Fatal("200 should fit the department's own 200 cap")
	}
	if got := readCounter(t, env.rdb, tenantModelDayKey("dept-1", "campus-smart")); got != 200 {
		t.Fatalf("department model counter = %d, want 200", got)
	}

	_, allowed, rejection := reserveCombined(t, env, 1, levels)
	if allowed {
		t.Fatal("expected the department's model cap to reject")
	}
	if rejection == nil || rejection.TenantID != "dept-1" {
		t.Fatalf("rejected = %v, want tenant dept-1", rejection)
	}
	if rejection.Label != "租户:计算机学院/模型:campus-smart" {
		t.Fatalf("rejected label = %q, want the tenant model label", rejection.Label)
	}
}

// Each model has its own pool. A model that has run out must not stop a
// different model from being called -- mixing them is exactly the mistake the
// token unit makes tempting.
func TestModelPoolsAreIndependent(t *testing.T) {
	env := newTestEnv(t)
	chain := []model.TenantQuota{tokenLevel("school", 100000, 1000000)}

	cheap := combined(chain, []model.ModelQuotaLevel{
		modelLevel("模型:qwen(全校)", "", "qwen", 100, 0),
	})
	expensive := combined(chain, []model.ModelQuotaLevel{
		modelLevel("模型:campus-smart(全校)", "", "campus-smart", 100, 0),
	})

	reserveCombined(t, env, 100, cheap)
	if _, allowed, _ := reserveCombined(t, env, 1, cheap); allowed {
		t.Fatal("expected qwen's own pool to reject")
	}

	// The expensive model has its own, untouched budget.
	if _, allowed, rejection := reserveCombined(t, env, 100, expensive); !allowed {
		t.Fatalf("campus-smart should be unaffected by qwen's exhaustion, rejected by %v", rejection)
	}
	if got := readCounter(t, env.rdb, modelDayKey("campus-smart")); got != 100 {
		t.Fatalf("campus-smart pool = %d, want 100", got)
	}
	if got := readCounter(t, env.rdb, modelDayKey("qwen")); got != 100 {
		t.Fatalf("qwen pool = %d, want 100", got)
	}
}

// --- settle ------------------------------------------------------------------

// Reconcile and Refund walk the reservation's keys, so the model keys must be
// part of that list. If they were not, every settled request would leave its
// full estimate charged to the model pool and the pool would drain far faster
// than the real usage.
func TestReconcileAdjustsModelKeys(t *testing.T) {
	env := newTestEnv(t)

	levels := combined(
		[]model.TenantQuota{tokenLevel("school", 100000, 1000000)},
		[]model.ModelQuotaLevel{modelLevel("模型:campus-smart(全校)", "", "campus-smart", 100000, 0)},
	)

	res, allowed, _ := reserveCombined(t, env, 1000, levels)
	if !allowed {
		t.Fatal("reservation should have been allowed")
	}
	if err := res.Reconcile(context.Background(), 400); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	for _, key := range []string{dayKey("school"), modelDayKey("campus-smart")} {
		if got := readCounter(t, env.rdb, key); got != 400 {
			t.Fatalf("%s = %d, want 400 after reconcile", key, got)
		}
	}
	for _, key := range []string{monthKey("school"), modelMonthKey("campus-smart")} {
		if got := readCounter(t, env.rdb, key); got != 400 {
			t.Fatalf("%s = %d, want 400 after reconcile", key, got)
		}
	}
}

func TestRefundClearsModelKeys(t *testing.T) {
	env := newTestEnv(t)

	levels := combined(
		[]model.TenantQuota{tokenLevel("school", 100000, 1000000)},
		[]model.ModelQuotaLevel{
			modelLevel("模型:campus-smart(全校)", "", "campus-smart", 100000, 0),
			modelLevel("租户:计算机学院/模型:campus-smart", "dept-1", "campus-smart", 100000, 0),
		},
	)

	res, allowed, _ := reserveCombined(t, env, 700, levels)
	if !allowed {
		t.Fatal("reservation should have been allowed")
	}
	if err := res.Refund(context.Background()); err != nil {
		t.Fatalf("refund: %v", err)
	}

	for _, key := range []string{
		dayKey("school"), monthKey("school"),
		modelDayKey("campus-smart"), modelMonthKey("campus-smart"),
		tenantModelDayKey("dept-1", "campus-smart"), tenantModelMonthKey("dept-1", "campus-smart"),
	} {
		if got := readCounter(t, env.rdb, key); got != 0 {
			t.Fatalf("%s = %d, want 0 after refund", key, got)
		}
	}
}

// A settle must be idempotent across both axes: a double refund would hand the
// tenant and the model pool free quota at the same time.
func TestSettleIsIdempotentAcrossAxes(t *testing.T) {
	env := newTestEnv(t)

	levels := combined(
		[]model.TenantQuota{tokenLevel("school", 100000, 1000000)},
		[]model.ModelQuotaLevel{modelLevel("模型:campus-smart(全校)", "", "campus-smart", 100000, 0)},
	)

	res, allowed, _ := reserveCombined(t, env, 500, levels)
	if !allowed {
		t.Fatal("reservation should have been allowed")
	}
	if err := res.Refund(context.Background()); err != nil {
		t.Fatalf("first refund: %v", err)
	}
	if err := res.Refund(context.Background()); err != nil {
		t.Fatalf("second refund: %v", err)
	}
	if err := res.Reconcile(context.Background(), 100); err != nil {
		t.Fatalf("reconcile after refund: %v", err)
	}

	if got := readCounter(t, env.rdb, modelDayKey("campus-smart")); got != 0 {
		t.Fatalf("shared pool = %d, want 0: a settled reservation must not be settled twice", got)
	}
	if got := readCounter(t, env.rdb, dayKey("school")); got != 0 {
		t.Fatalf("tenant = %d, want 0", got)
	}
}

// --- uncapped ----------------------------------------------------------------

// Nothing configured must mean nothing written, on either axis. This is what
// keeps an unconfigured deployment byte-for-byte identical to the version
// before the model axis existed.
func TestUncappedModelLevelsWriteNoKeys(t *testing.T) {
	env := newTestEnv(t)

	levels := combined(
		[]model.TenantQuota{tokenLevel("school", 0, 0)},
		[]model.ModelQuotaLevel{modelLevel("模型:campus-smart(全校)", "", "campus-smart", 0, 0)},
	)

	res, allowed, _ := reserveCombined(t, env, 500, levels)
	if !allowed {
		t.Fatal("an uncapped chain must be allowed")
	}
	if res != nil {
		t.Fatal("expected no reservation when nothing is capped")
	}
	if keys := env.mr.Keys(); len(keys) != 0 {
		t.Fatalf("expected no Redis keys, got %v", keys)
	}
}

// --- adapters ----------------------------------------------------------------

// The tenant label prefers the name, because the id in a security event tells an
// operator nothing. The id stays the fallback for chains built without names.
func TestTenantQuotaLevelsLabelsPreferName(t *testing.T) {
	named := TenantQuotaLevels([]model.TenantQuota{
		{TenantID: "3f2a-...", Name: "计算机学院"},
	})
	if named[0].Label != "租户:计算机学院" {
		t.Fatalf("label = %q, want the tenant name", named[0].Label)
	}

	anonymous := TenantQuotaLevels([]model.TenantQuota{{TenantID: "tenant-1"}})
	if anonymous[0].Label != "租户:tenant-1" {
		t.Fatalf("label = %q, want the id as a fallback", anonymous[0].Label)
	}
}

// The adapters must not invent caps. A level with no limit stays uncapped, so
// activeQuotaLevels drops it and no counter is created.
func TestAdaptersPreserveLimits(t *testing.T) {
	tenant := TenantQuotaLevels([]model.TenantQuota{
		{TenantID: "t1", DailyTokenLimit: 10, MonthlyTokenLimit: 20},
	})
	if tenant[0].DailyLimit != 10 || tenant[0].MonthlyLimit != 20 {
		t.Fatalf("tenant level = %+v, want 10/20", tenant[0])
	}

	mdl := ModelQuotaLevels([]model.ModelQuotaLevel{
		{TenantID: "t1", ModelAlias: "m", DailyTokenLimit: 30, MonthlyTokenLimit: 40},
	})
	if mdl[0].DailyLimit != 30 || mdl[0].MonthlyLimit != 40 {
		t.Fatalf("model level = %+v, want 30/40", mdl[0])
	}
	if mdl[0].ModelAlias != "m" || mdl[0].TenantID != "t1" {
		t.Fatalf("model level identity = %+v, want t1/m", mdl[0])
	}
}
