package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

func i64ptr(v int64) *int64 { return &v }

// quotaTenant builds a tenant node with explicit limits. A nil argument means
// "inherit from the nearest ancestor that sets one".
func quotaTenant(id string, daily, monthly, requests, budget *int64) model.Tenant {
	return model.Tenant{
		ID:                  id,
		DailyTokenLimit:     daily,
		MonthlyTokenLimit:   monthly,
		MonthlyRequestLimit: requests,
		MonthlyBudgetMicros: budget,
	}
}

func accessTenant(id string, models []string) model.Tenant {
	return model.Tenant{ID: id, AllowedModels: models}
}

func TestValidateAllocationAgainstParent(t *testing.T) {
	school := quotaTenant("school", i64ptr(1000), i64ptr(10000), i64ptr(500), i64ptr(900))
	deptInherits := quotaTenant("dept", nil, nil, nil, nil)

	cases := []struct {
		name      string
		chain     []model.Tenant
		alloc     model.TenantAllocation
		wantError string // substring that must appear, "" means allowed
	}{
		{
			name:  "a root has no ceiling to exceed",
			chain: []model.Tenant{school},
			alloc: model.TenantAllocation{DailyTokenLimit: i64ptr(999999999)},
		},
		{
			name:  "allocating below the parent ceiling is allowed",
			chain: []model.Tenant{school, deptInherits},
			alloc: model.TenantAllocation{DailyTokenLimit: i64ptr(500)},
		},
		{
			name:  "allocating exactly the parent ceiling is allowed",
			chain: []model.Tenant{school, deptInherits},
			alloc: model.TenantAllocation{DailyTokenLimit: i64ptr(1000)},
		},
		{
			name:      "allocating above the parent ceiling is refused",
			chain:     []model.Tenant{school, deptInherits},
			alloc:     model.TenantAllocation{DailyTokenLimit: i64ptr(1001)},
			wantError: "exceeds what the parent level holds",
		},
		{
			name:      "the monthly request ceiling is checked too",
			chain:     []model.Tenant{school, deptInherits},
			alloc:     model.TenantAllocation{MonthlyRequestLimit: i64ptr(501)},
			wantError: "monthly_request_limit",
		},
		{
			name:      "the budget ceiling is checked too",
			chain:     []model.Tenant{school, deptInherits},
			alloc:     model.TenantAllocation{MonthlyBudgetMicros: i64ptr(901)},
			wantError: "monthly_budget_micros",
		},
		{
			name:  "inheriting can never exceed anything",
			chain: []model.Tenant{school, deptInherits},
			alloc: model.TenantAllocation{},
		},
		{
			// The ceiling is the parent's *effective* limit, not its own
			// explicit value. Here the parent inherits 1000 from the school, so
			// a request of 2000 must still be refused.
			name:      "the ceiling is inherited through the parent",
			chain:     []model.Tenant{school, deptInherits, quotaTenant("student", nil, nil, nil, nil)},
			alloc:     model.TenantAllocation{DailyTokenLimit: i64ptr(2000)},
			wantError: "exceeds what the parent level holds (1000)",
		},
		{
			// 0 means unlimited, so an uncapped parent imposes no ceiling.
			name:  "an uncapped parent imposes no ceiling",
			chain: []model.Tenant{quotaTenant("school", i64ptr(0), i64ptr(0), i64ptr(0), i64ptr(0)), deptInherits},
			alloc: model.TenantAllocation{DailyTokenLimit: i64ptr(1000000)},
		},
		{
			name:      "a negative value is refused",
			chain:     []model.Tenant{school, deptInherits},
			alloc:     model.TenantAllocation{DailyTokenLimit: i64ptr(-1)},
			wantError: "cannot be negative",
		},
		{
			name:      "a negative soft limit is refused",
			chain:     []model.Tenant{school, deptInherits},
			alloc:     model.TenantAllocation{SoftTokenLimit: i64ptr(-5)},
			wantError: "soft_token_limit",
		},
		{
			name:  "an unrestricted parent allows any model",
			chain: []model.Tenant{accessTenant("school", nil), accessTenant("dept", nil)},
			alloc: model.TenantAllocation{AllowedModels: []string{"gpt-4o"}},
		},
		{
			name:  "a child may narrow its parent's allowlist",
			chain: []model.Tenant{accessTenant("school", []string{"a", "b"}), accessTenant("dept", nil)},
			alloc: model.TenantAllocation{AllowedModels: []string{"b"}},
		},
		{
			name:      "a child may not widen its parent's allowlist",
			chain:     []model.Tenant{accessTenant("school", []string{"a", "b"}), accessTenant("dept", nil)},
			alloc:     model.TenantAllocation{AllowedModels: []string{"c"}},
			wantError: `model "c" is not available at the parent level`,
		},
		{
			// Access is intersected, so the ceiling is the parent's *effective*
			// allowlist -- the intersection of everything above it.
			name: "the access ceiling is inherited through the parent",
			chain: []model.Tenant{
				accessTenant("school", []string{"a", "b", "c"}),
				accessTenant("dept", []string{"b", "c"}),
				accessTenant("teacher", nil),
			},
			alloc:     model.TenantAllocation{AllowedModels: []string{"a"}},
			wantError: `model "a" is not available at the parent level`,
		},
		{
			name:  "denying every model is always a valid narrowing",
			chain: []model.Tenant{accessTenant("school", []string{"a"}), accessTenant("dept", nil)},
			alloc: model.TenantAllocation{AllowedModels: []string{}},
		},
		{
			name:  "inheriting access is always allowed",
			chain: []model.Tenant{accessTenant("school", []string{"a"}), accessTenant("dept", nil)},
			alloc: model.TenantAllocation{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateAllocationAgainstParent(tc.chain, tc.alloc)
			if tc.wantError == "" {
				if got != "" {
					t.Fatalf("expected the allocation to be allowed, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantError) {
				t.Fatalf("error = %q, want it to contain %q", got, tc.wantError)
			}
		})
	}
}

func TestNormalizeAllocationDeduplicatesAndTrimsModels(t *testing.T) {
	a := model.TenantAllocation{AllowedModels: []string{" gpt-4o ", "gpt-4o", "", "  ", "claude"}}
	normalizeAllocation(&a)

	want := []string{"gpt-4o", "claude"}
	if len(a.AllowedModels) != len(want) {
		t.Fatalf("models = %v, want %v", a.AllowedModels, want)
	}
	for i := range want {
		if a.AllowedModels[i] != want[i] {
			t.Fatalf("models = %v, want %v", a.AllowedModels, want)
		}
	}
}

// nil and [] mean different things and normalisation must not blur them: nil
// inherits, [] denies every model.
func TestNormalizeAllocationPreservesNilVersusEmpty(t *testing.T) {
	inherit := model.TenantAllocation{}
	normalizeAllocation(&inherit)
	if inherit.AllowedModels != nil {
		t.Fatal("nil must stay nil (inherit)")
	}

	denyAll := model.TenantAllocation{AllowedModels: []string{}}
	normalizeAllocation(&denyAll)
	if denyAll.AllowedModels == nil {
		t.Fatal("an empty list must stay non-nil (deny all)")
	}
	if len(denyAll.AllowedModels) != 0 {
		t.Fatalf("expected an empty list, got %v", denyAll.AllowedModels)
	}

	// A list of nothing but blanks collapses to deny-all rather than to
	// inherit: the caller asked for a list, it just contained no usable names.
	blanks := model.TenantAllocation{AllowedModels: []string{" ", ""}}
	normalizeAllocation(&blanks)
	if blanks.AllowedModels == nil {
		t.Fatal("a list of blanks must not become nil (inherit)")
	}
	if len(blanks.AllowedModels) != 0 {
		t.Fatalf("expected an empty list, got %v", blanks.AllowedModels)
	}
}

func TestNormalizeTenantParentTreatsBlankAsRoot(t *testing.T) {
	if got := normalizeTenantParent(nil); got != nil {
		t.Fatalf("nil = %v, want nil", got)
	}
	blank := "  "
	if got := normalizeTenantParent(&blank); got != nil {
		t.Fatalf("blank = %v, want nil", got)
	}
	id := "  abc  "
	got := normalizeTenantParent(&id)
	if got == nil || *got != "abc" {
		t.Fatalf("got %v, want \"abc\"", got)
	}
}

func TestNormalizeScopeTenantIDTreatsBlankAsNoScope(t *testing.T) {
	if got := normalizeScopeTenantID(nil); got != nil {
		t.Fatalf("nil = %v, want nil", got)
	}
	blank := ""
	if got := normalizeScopeTenantID(&blank); got != nil {
		t.Fatalf("blank = %v, want nil", got)
	}
	id := " abc "
	got := normalizeScopeTenantID(&id)
	if got == nil || *got != "abc" {
		t.Fatalf("got %v, want \"abc\"", got)
	}
}

// A scoped administrator must not be able to hand out a scope wider than their
// own, nor to create an account with no scope at all -- that would be exactly
// the global reach they do not have.
//
// Only the branches that decide without touching the database are exercised
// here; the in-subtree lookup is a thin wrapper over TenantIsDescendant, whose
// SQL is covered by the storage package.
func TestAuthorizeScopeGrantDecidesGlobalReachWithoutTheDatabase(t *testing.T) {
	s := &Server{} // a nil pool is safe: these branches return before any query
	ctx := context.Background()

	scope := "dept-id"
	scoped := adminPrincipal{
		Username:      "dept-admin",
		Roles:         map[string]bool{"tenant_admin": true},
		ScopeTenantID: &scope,
	}
	unscoped := adminPrincipal{
		Username: "root",
		Roles:    map[string]bool{"super_admin": true},
	}

	t.Run("a scoped actor cannot grant an account without a scope", func(t *testing.T) {
		msg, err := s.authorizeScopeGrant(ctx, scoped, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if msg == "" {
			t.Fatal("expected the grant to be refused")
		}
	})

	t.Run("a scoped actor cannot grant an empty scope either", func(t *testing.T) {
		blank := ""
		msg, err := s.authorizeScopeGrant(ctx, scoped, &blank)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if msg == "" {
			t.Fatal("a blank scope must be treated the same as no scope")
		}
	})

	t.Run("an unscoped actor may grant no scope", func(t *testing.T) {
		msg, err := s.authorizeScopeGrant(ctx, unscoped, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if msg != "" {
			t.Fatalf("expected the grant to be allowed, got %q", msg)
		}
	})

	t.Run("an unscoped actor may grant any scope", func(t *testing.T) {
		target := "some-tenant"
		msg, err := s.authorizeScopeGrant(ctx, unscoped, &target)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if msg != "" {
			t.Fatalf("expected the grant to be allowed, got %q", msg)
		}
	})
}
