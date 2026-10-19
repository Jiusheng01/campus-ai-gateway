package storage

import (
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// These exercise the model quota inheritance walk directly. The walk is the
// heart of the feature -- everything else is plumbing -- and its rules are
// exactly the ones that are hard to see in a running system: a NULL that means
// inherit, a 0 that means unlimited, and a level that must NOT get a counter of
// its own.

func i64(v int64) *int64 { return &v }

// chainOf builds a root-first tenant chain. The names matter: they are what the
// rejection label uses.
func chainOf(nodes ...[2]string) []model.TenantQuota {
	out := make([]model.TenantQuota, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, model.TenantQuota{TenantID: n[0], Name: n[1]})
	}
	return out
}

// labels is a compact projection for assertions.
func labels(levels []model.ModelQuotaLevel) []string {
	out := make([]string, 0, len(levels))
	for _, lv := range levels {
		out = append(out, lv.Label)
	}
	return out
}

func TestResolveModelQuotaLevelsWithNothingConfigured(t *testing.T) {
	levels := resolveModelQuotaLevels(
		chainOf([2]string{"school", "学校"}, [2]string{"dept", "计算机学院"}),
		"campus-smart", modelQuotaRow{}, false, nil,
	)
	if len(levels) != 0 {
		t.Fatalf("got %v, want no levels: nothing is configured", labels(levels))
	}
}

// The shared pool is the root, so it is emitted first and its value becomes the
// starting point for the walk.
func TestResolveModelQuotaLevelsEmitsTheSharedPoolFirst(t *testing.T) {
	levels := resolveModelQuotaLevels(
		chainOf([2]string{"school", "学校"}),
		"campus-smart",
		modelQuotaRow{daily: i64(1000), monthly: i64(20000)}, true,
		nil,
	)
	if len(levels) != 1 {
		t.Fatalf("got %v, want just the shared pool", labels(levels))
	}
	if levels[0].Label != "模型:campus-smart(全校)" {
		t.Fatalf("label = %q, want the shared pool label", levels[0].Label)
	}
	if levels[0].TenantID != "" {
		t.Fatalf("tenant = %q, want empty: the pool belongs to no tenant", levels[0].TenantID)
	}
	if levels[0].DailyTokenLimit != 1000 || levels[0].MonthlyTokenLimit != 20000 {
		t.Fatalf("limits = %d/%d, want 1000/20000", levels[0].DailyTokenLimit, levels[0].MonthlyTokenLimit)
	}
}

// A level is emitted only when the subject holds a row for this model. A tenant
// that merely inherits the shared pool's value is already bounded by the pool's
// own counter, so a counter of its own would add keys and no constraint.
func TestResolveModelQuotaLevelsSkipsTenantsWithoutARow(t *testing.T) {
	levels := resolveModelQuotaLevels(
		chainOf([2]string{"school", "学校"}, [2]string{"dept", "计算机学院"}),
		"campus-smart",
		modelQuotaRow{daily: i64(1000)}, true,
		map[string]modelQuotaRow{}, // neither tenant configures this model
	)
	if len(levels) != 1 {
		t.Fatalf("got %v, want only the shared pool: inherited levels get no counter", labels(levels))
	}
}

// A tenant row overrides the shared pool for that tenant, and only for that
// tenant. This is the "narrow it for one department" case.
func TestResolveModelQuotaLevelsNarrowsForOneTenant(t *testing.T) {
	levels := resolveModelQuotaLevels(
		chainOf([2]string{"school", "学校"}, [2]string{"dept", "计算机学院"}),
		"campus-smart",
		modelQuotaRow{daily: i64(1000), monthly: i64(20000)}, true,
		map[string]modelQuotaRow{
			"dept": {daily: i64(300), monthly: i64(6000)},
		},
	)
	if len(levels) != 2 {
		t.Fatalf("got %v, want the shared pool and the department", labels(levels))
	}
	if levels[1].Label != "租户:计算机学院/模型:campus-smart" {
		t.Fatalf("label = %q, want the department level", levels[1].Label)
	}
	if levels[1].TenantID != "dept" {
		t.Fatalf("tenant = %q, want dept", levels[1].TenantID)
	}
	if levels[1].DailyTokenLimit != 300 || levels[1].MonthlyTokenLimit != 6000 {
		t.Fatalf("limits = %d/%d, want 300/6000", levels[1].DailyTokenLimit, levels[1].MonthlyTokenLimit)
	}
}

// A NULL limit means "inherit this dimension", and it is per dimension: a row
// that sets only the monthly cap keeps the daily one it was given.
func TestResolveModelQuotaLevelsInheritsPerDimension(t *testing.T) {
	levels := resolveModelQuotaLevels(
		chainOf([2]string{"school", "学校"}),
		"campus-smart",
		modelQuotaRow{daily: i64(1000), monthly: i64(20000)}, true,
		map[string]modelQuotaRow{
			"school": {daily: nil, monthly: i64(500)},
		},
	)
	if len(levels) != 2 {
		t.Fatalf("got %v, want the shared pool and the school", labels(levels))
	}
	// daily was NULL on the school's row, so the shared pool's 1000 carries down;
	// monthly was explicit, so it replaces 20000.
	if levels[1].DailyTokenLimit != 1000 {
		t.Fatalf("daily = %d, want the inherited 1000", levels[1].DailyTokenLimit)
	}
	if levels[1].MonthlyTokenLimit != 500 {
		t.Fatalf("monthly = %d, want the explicit 500", levels[1].MonthlyTokenLimit)
	}
}

// An explicit 0 is a value, not an absence: it means "unlimited for this
// subject" and it stops the inheritance for that dimension. It must not be
// emitted as a level of its own, because a level with no cap has nothing to
// enforce and would only create a pair of idle counters.
func TestResolveModelQuotaLevelsTreatsExplicitZeroAsUnlimited(t *testing.T) {
	levels := resolveModelQuotaLevels(
		chainOf([2]string{"school", "学校"}),
		"campus-smart",
		modelQuotaRow{daily: i64(1000)}, true,
		map[string]modelQuotaRow{
			"school": {daily: i64(0), monthly: i64(0)},
		},
	)
	if len(levels) != 1 {
		t.Fatalf("got %v, want only the shared pool: an uncapped level gets no counter", labels(levels))
	}
}

// A tenant setting 0 cannot escape the shared pool. 0 removes that tenant's own
// cap; the campus-wide cap is a separate level and still applies. This is the
// inclusive semantics the tenant axis already uses, and it is the single most
// surprising rule here -- hence its own test.
func TestResolveModelQuotaLevelsCannotEscapeTheSharedPool(t *testing.T) {
	levels := resolveModelQuotaLevels(
		chainOf([2]string{"school", "学校"}),
		"campus-smart",
		modelQuotaRow{daily: i64(1000)}, true,
		map[string]modelQuotaRow{
			"school": {daily: i64(0), monthly: i64(0)},
		},
	)
	if levels[0].Label != "模型:campus-smart(全校)" {
		t.Fatalf("the shared pool must survive a tenant's explicit zero, got %v", labels(levels))
	}
	if levels[0].DailyTokenLimit != 1000 {
		t.Fatalf("shared pool daily = %d, want 1000", levels[0].DailyTokenLimit)
	}
}

// A row with both limits NULL sets nothing, so it emits nothing. Writing such a
// row is how a subject goes back to inheriting without deleting the row.
func TestResolveModelQuotaLevelsIgnoresARowWithBothLimitsNull(t *testing.T) {
	levels := resolveModelQuotaLevels(
		chainOf([2]string{"school", "学校"}),
		"campus-smart",
		modelQuotaRow{}, false,
		map[string]modelQuotaRow{
			"school": {daily: nil, monthly: nil},
		},
	)
	if len(levels) != 0 {
		t.Fatalf("got %v, want nothing: a row with no limits constrains nothing", labels(levels))
	}
}

// A tenant row is meaningful even with no shared pool: the chain is rooted at
// the pool when there is one, and at the deepest configured tenant when there is
// not.
func TestResolveModelQuotaLevelsWorksWithoutASharedPool(t *testing.T) {
	levels := resolveModelQuotaLevels(
		chainOf([2]string{"school", "学校"}, [2]string{"dept", "计算机学院"}),
		"campus-smart",
		modelQuotaRow{}, false,
		map[string]modelQuotaRow{
			"dept": {daily: i64(300)},
		},
	)
	if len(levels) != 1 {
		t.Fatalf("got %v, want just the department", labels(levels))
	}
	if levels[0].TenantID != "dept" {
		t.Fatalf("tenant = %q, want dept", levels[0].TenantID)
	}
}

// The walk is root first, so an ancestor's value must be visible to its
// descendant when the descendant sets only one dimension.
func TestResolveModelQuotaLevelsWalksRootFirst(t *testing.T) {
	levels := resolveModelQuotaLevels(
		chainOf([2]string{"school", "学校"}, [2]string{"dept", "计算机学院"}, [2]string{"teacher", "张老师"}),
		"campus-smart",
		modelQuotaRow{}, false,
		map[string]modelQuotaRow{
			"school":  {daily: i64(900), monthly: i64(9000)},
			"teacher": {daily: nil, monthly: i64(50)},
		},
	)
	if len(levels) != 2 {
		t.Fatalf("got %v, want the school and the teacher", labels(levels))
	}
	if levels[0].TenantID != "school" || levels[1].TenantID != "teacher" {
		t.Fatalf("order = %v, want root first", labels(levels))
	}
	// The teacher inherits the school's daily 900 across the unconfigured
	// department in between.
	if levels[1].DailyTokenLimit != 900 {
		t.Fatalf("teacher daily = %d, want the school's 900", levels[1].DailyTokenLimit)
	}
	if levels[1].MonthlyTokenLimit != 50 {
		t.Fatalf("teacher monthly = %d, want its own 50", levels[1].MonthlyTokenLimit)
	}
}

// An empty tenant chain still resolves the shared pool. This is what bounds a
// root tenant: it has no ancestor to defer to, but the campus-wide pool is the
// root of the model chain and must still act as its ceiling. Returning nothing
// here would let a root tenant allocate itself any amount of a model.
func TestResolveModelQuotaLevelsResolvesThePoolForAnEmptyChain(t *testing.T) {
	levels := resolveModelQuotaLevels(
		nil,
		"campus-smart",
		modelQuotaRow{daily: i64(1000), monthly: i64(20000)}, true,
		nil,
	)
	if len(levels) != 1 {
		t.Fatalf("got %v, want the shared pool", labels(levels))
	}
	if levels[0].Label != "模型:campus-smart(全校)" {
		t.Fatalf("label = %q, want the shared pool label", levels[0].Label)
	}
	if levels[0].DailyTokenLimit != 1000 {
		t.Fatalf("daily = %d, want 1000", levels[0].DailyTokenLimit)
	}
}

// Aliases are trimmed and an empty one is refused rather than silently creating
// a pool that no request can match.
func TestNormalizeAlias(t *testing.T) {
	if _, err := normalizeAlias("   "); err == nil {
		t.Fatal("a blank alias must be refused")
	}
	got, err := normalizeAlias("  campus-smart  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "campus-smart" {
		t.Fatalf("alias = %q, want it trimmed", got)
	}
	if _, err := normalizeAlias(string(make([]byte, 129))); err == nil {
		t.Fatal("an over-long alias must be refused")
	}
}
