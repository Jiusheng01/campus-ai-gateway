package app

import (
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// ptr is the pointer helper the tri-state cases need: nil means "inherit" and
// must be distinguishable from a pointer to 0 ("unlimited").
func ptr(v int64) *int64 { return &v }

// testTenantID is the subject of a tenant-level row. A nil tenant id would mean
// the shared pool instead, which is a different set of rules.
const testTenantID = "11111111-1111-1111-1111-111111111111"

// modelQuotaBase is a tenant-level allocation that passes every rule, so each
// case only has to state the one thing it is exercising.
func modelQuotaBase() modelQuotaCheck {
	tenantID := testTenantID
	return modelQuotaCheck{
		Allocation: model.ModelQuotaAllocation{
			TenantID:          &tenantID,
			ModelAlias:        "campus-smart",
			DailyTokenLimit:   ptr(100),
			MonthlyTokenLimit: ptr(1000),
		},
		CeilingDaily:   500,
		CeilingMonthly: 5000,
		ChainDepth:     4,
	}
}

func TestValidateModelQuotaAcceptsAWellFormedTenantRow(t *testing.T) {
	if msg := validateModelQuotaAllocation(modelQuotaBase()); msg != "" {
		t.Fatalf("expected the row to be accepted, got %q", msg)
	}
}

// The alias is the row's whole identity, so an empty one would create a pool no
// request can ever match: configured, and silently inert.
func TestValidateModelQuotaRequiresAnAlias(t *testing.T) {
	for _, alias := range []string{"", "   "} {
		c := modelQuotaBase()
		c.Allocation.ModelAlias = alias
		if msg := validateModelQuotaAllocation(c); !strings.Contains(msg, "model alias is required") {
			t.Fatalf("alias %q: got %q, want a message naming the alias", alias, msg)
		}
	}
}

func TestValidateModelQuotaRejectsAnOverlongAlias(t *testing.T) {
	c := modelQuotaBase()
	c.Allocation.ModelAlias = strings.Repeat("a", 129)
	if msg := validateModelQuotaAllocation(c); !strings.Contains(msg, "too long") {
		t.Fatalf("got %q, want a message about the length", msg)
	}
	// 128 is the column width, so exactly 128 must still fit.
	c.Allocation.ModelAlias = strings.Repeat("a", 128)
	if msg := validateModelQuotaAllocation(c); msg != "" {
		t.Fatalf("a 128-character alias must be accepted, got %q", msg)
	}
}

func TestValidateModelQuotaNamesTheNegativeDimension(t *testing.T) {
	c := modelQuotaBase()
	c.Allocation.DailyTokenLimit = ptr(-1)
	if msg := validateModelQuotaAllocation(c); !strings.Contains(msg, "daily_token_limit") {
		t.Fatalf("got %q, want the offending field named", msg)
	}

	c = modelQuotaBase()
	c.Allocation.MonthlyTokenLimit = ptr(-1)
	if msg := validateModelQuotaAllocation(c); !strings.Contains(msg, "monthly_token_limit") {
		t.Fatalf("got %q, want the offending field named", msg)
	}
}

// A child may narrow what its parent holds but never widen it. The limiter walks
// every level, so an excess would never actually be spendable -- but the numbers
// an operator reads would be wrong, and "I allocated it and it does not work" is
// what makes people stop trusting a quota system.
func TestValidateModelQuotaRejectsExceedingTheCeiling(t *testing.T) {
	c := modelQuotaBase()
	c.Allocation.DailyTokenLimit = ptr(501) // ceiling is 500
	if msg := validateModelQuotaAllocation(c); !strings.Contains(msg, "exceeds what the parent level holds") {
		t.Fatalf("got %q, want an over-ceiling message", msg)
	}

	// Exactly the ceiling is allowed: it is a cap, not an exclusive bound.
	c.Allocation.DailyTokenLimit = ptr(500)
	if msg := validateModelQuotaAllocation(c); msg != "" {
		t.Fatalf("the ceiling itself must be allowed, got %q", msg)
	}
}

// 0 on the parent means "unlimited", not "zero available". Treating it as a
// ceiling of zero would make every child allocation impossible the moment the
// parent was set to unlimited -- the opposite of the intent.
func TestValidateModelQuotaTreatsAZeroCeilingAsUnlimited(t *testing.T) {
	c := modelQuotaBase()
	c.CeilingDaily, c.CeilingMonthly = 0, 0
	c.Allocation.DailyTokenLimit = ptr(9_000_000)
	c.Allocation.MonthlyTokenLimit = ptr(9_000_000)
	if msg := validateModelQuotaAllocation(c); msg != "" {
		t.Fatalf("an uncapped parent must not constrain the child, got %q", msg)
	}
}

// Inheriting can never exceed anything, so a null limit must skip the ceiling
// check entirely rather than being read as zero.
func TestValidateModelQuotaAllowsInheritingUnderATightCeiling(t *testing.T) {
	c := modelQuotaBase()
	c.Allocation.DailyTokenLimit = nil
	c.Allocation.MonthlyTokenLimit = nil
	if msg := validateModelQuotaAllocation(c); msg != "" {
		t.Fatalf("inheriting must always be allowed, got %q", msg)
	}
}

// A quota for a model the tenant cannot call is a number nobody can reach.
// Rejecting it turns a silent no-op into a message.
func TestValidateModelQuotaRejectsAModelOutsideTheAllowlist(t *testing.T) {
	c := modelQuotaBase()
	c.AccessRestricted = true
	c.Access = map[string]struct{}{"qwen": {}}
	if msg := validateModelQuotaAllocation(c); !strings.Contains(msg, "not allowed for this tenant") {
		t.Fatalf("got %q, want an allowlist message", msg)
	}

	// The same row is fine once the model is allowed.
	c.Access["campus-smart"] = struct{}{}
	if msg := validateModelQuotaAllocation(c); msg != "" {
		t.Fatalf("an allowed model must pass, got %q", msg)
	}
}

// No allowlist anywhere means every model is callable, so the allowlist rule
// must not fire on an unrestricted tenant.
func TestValidateModelQuotaSkipsTheAllowlistWhenUnrestricted(t *testing.T) {
	c := modelQuotaBase()
	c.AccessRestricted = false
	c.Access = nil
	if msg := validateModelQuotaAllocation(c); msg != "" {
		t.Fatalf("an unrestricted tenant must pass, got %q", msg)
	}
}

// The system pool sits above every tenant, so the ceiling and allowlist rules --
// both of which are about a parent -- must not apply to it. Applying them would
// make the root of the chain impossible to configure.
func TestValidateModelQuotaExemptsTheSystemPool(t *testing.T) {
	c := modelQuotaCheck{
		Allocation: model.ModelQuotaAllocation{
			TenantID:          nil, // the shared pool
			ModelAlias:        "campus-smart",
			DailyTokenLimit:   ptr(9_000_000),
			MonthlyTokenLimit: ptr(9_000_000),
		},
		// Deliberately hostile: a tight ceiling and an allowlist that excludes
		// the model. Neither may be consulted for the pool.
		CeilingDaily:     1,
		CeilingMonthly:   1,
		AccessRestricted: true,
		Access:           map[string]struct{}{"qwen": {}},
		ChainDepth:       4,
	}
	if msg := validateModelQuotaAllocation(c); msg != "" {
		t.Fatalf("the system pool must not be constrained by a parent, got %q", msg)
	}
}

// The key budget is what keeps one request from touching an unbounded number of
// Redis buckets. The tenant axis costs two keys per level and the model axis two
// more per level plus two for the shared pool, so the bound has to be checked
// against the depth rather than assumed.
func TestValidateModelQuotaEnforcesTheKeyBudget(t *testing.T) {
	c := modelQuotaBase()

	// The 4-level design this gateway is built around peaks at 18 keys.
	c.ChainDepth = 4
	if keys := modelQuotaChainKeys(c.ChainDepth); keys != 18 {
		t.Fatalf("4-level key count = %d, want 18", keys)
	}
	if msg := validateModelQuotaAllocation(c); msg != "" {
		t.Fatalf("a 4-level chain must fit the budget, got %q", msg)
	}

	// 5 levels is 22, still inside 24.
	c.ChainDepth = 5
	if msg := validateModelQuotaAllocation(c); msg != "" {
		t.Fatalf("a 5-level chain must fit the budget, got %q", msg)
	}

	// 6 levels is 26, over the budget.
	c.ChainDepth = 6
	if msg := validateModelQuotaAllocation(c); !strings.Contains(msg, "key budget") {
		t.Fatalf("a 6-level chain must be refused, got %q", msg)
	}
}

// A blank tenant id from a JSON body means "the shared pool", not "the tenant
// whose id is empty". Getting this backwards would silently turn one tenant's
// quota into a campus-wide cap, so the contract is pinned here as well as in
// internal/model.
func TestModelQuotaTenantIDBlankMeansSharedPool(t *testing.T) {
	if got := model.NormalizeOptionalID(nil); got != nil {
		t.Fatalf("nil -> %v, want nil (shared pool)", got)
	}
	blank := "   "
	if got := model.NormalizeOptionalID(&blank); got != nil {
		t.Fatalf("blank -> %v, want nil (shared pool)", got)
	}
	id := "  11111111-1111-1111-1111-111111111111  "
	got := model.NormalizeOptionalID(&id)
	if got == nil || *got != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("got %v, want the trimmed id", got)
	}
}

// The audit resource has to tell the two kinds of row apart, otherwise a
// campus-wide change and one tenant's change look identical in the log.
func TestModelQuotaResourceDistinguishesPoolFromTenant(t *testing.T) {
	tenantID := testTenantID
	pool := modelQuotaResource(model.ModelQuota{TenantID: nil, ModelAlias: "m"})
	tenant := modelQuotaResource(model.ModelQuota{TenantID: &tenantID, ModelAlias: "m"})
	if pool == tenant {
		t.Fatalf("pool and tenant resources must differ, both were %q", pool)
	}
	if !strings.Contains(pool, "model_pool") {
		t.Fatalf("pool resource = %q, want it marked as a pool", pool)
	}
	if !strings.Contains(tenant, tenantID) {
		t.Fatalf("tenant resource = %q, want the tenant id", tenant)
	}
}
