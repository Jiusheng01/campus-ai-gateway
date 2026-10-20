package app

import (
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// A budget replacement carries seven numbers whose meaning depends on whether
// they are absent, zero or positive. The validator's job is narrow: refuse the
// one value that has no meaning (a negative cap) and leave the other three
// states exactly as they arrived. Anything cleverer would quietly turn
// "inherit" into "unlimited".

func TestValidateTenantAllocationKeepsTheThreeStates(t *testing.T) {
	inherit := model.TenantAllocation{}
	if msg := validateTenantAllocation(&inherit); msg != "" {
		t.Fatalf("all fields absent = %q, want accepted: nil is the inherit state", msg)
	}
	if inherit.DailyTokenLimit != nil || inherit.RPMLimit != nil || inherit.MaxConcurrency != nil {
		t.Fatal("validation must not materialise a value where the caller sent none")
	}

	zero := int64(0)
	explicit := model.TenantAllocation{DailyTokenLimit: &zero, RPMLimit: &zero, MaxConcurrency: &zero}
	if msg := validateTenantAllocation(&explicit); msg != "" {
		t.Fatalf("explicit zero = %q, want accepted: zero means unlimited", msg)
	}
	if explicit.DailyTokenLimit == nil || *explicit.DailyTokenLimit != 0 {
		t.Fatal("explicit zero must survive as zero, not collapse into inherit")
	}

	cap := int64(5_000_000)
	positive := model.TenantAllocation{DailyTokenLimit: &cap, MonthlyTokenLimit: &cap}
	if msg := validateTenantAllocation(&positive); msg != "" {
		t.Fatalf("positive cap = %q, want accepted", msg)
	}
	if *positive.DailyTokenLimit != cap {
		t.Fatal("a positive cap must be preserved")
	}
}

func TestValidateTenantAllocationNamesTheOffendingField(t *testing.T) {
	neg := int64(-1)
	cases := []struct {
		name  string
		in    model.TenantAllocation
		field string
	}{
		{"daily", model.TenantAllocation{DailyTokenLimit: &neg}, "daily_token_limit"},
		{"monthly tokens", model.TenantAllocation{MonthlyTokenLimit: &neg}, "monthly_token_limit"},
		{"monthly requests", model.TenantAllocation{MonthlyRequestLimit: &neg}, "monthly_request_limit"},
		{"budget", model.TenantAllocation{MonthlyBudgetMicros: &neg}, "monthly_budget_micros"},
		{"rpm", model.TenantAllocation{RPMLimit: &neg}, "rpm_limit"},
		{"tpm", model.TenantAllocation{TPMLimit: &neg}, "tpm_limit"},
		{"concurrency", model.TenantAllocation{MaxConcurrency: &neg}, "max_concurrency"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := validateTenantAllocation(&tc.in)
			if msg == "" {
				t.Fatalf("%s: negative value accepted", tc.field)
			}
			if !strings.Contains(msg, tc.field) {
				t.Fatalf("message %q does not name %q", msg, tc.field)
			}
		})
	}
}

// The allowlist has its own three states — nil inherits, empty denies
// everything — and trimming must not collapse them.
func TestValidateTenantAllocationNormalizesTheAllowlist(t *testing.T) {
	inherit := model.TenantAllocation{AllowedModels: nil}
	if msg := validateTenantAllocation(&inherit); msg != "" {
		t.Fatalf("nil allowlist = %q, want accepted", msg)
	}
	if inherit.AllowedModels != nil {
		t.Fatal("a nil allowlist means inherit and must stay nil")
	}

	deny := model.TenantAllocation{AllowedModels: []string{}}
	if msg := validateTenantAllocation(&deny); msg != "" {
		t.Fatalf("empty allowlist = %q, want accepted", msg)
	}
	if deny.AllowedModels == nil || len(deny.AllowedModels) != 0 {
		t.Fatal("an empty allowlist means deny every model and must stay empty")
	}

	list := model.TenantAllocation{AllowedModels: []string{" campus-smart ", "campus-smart", "qwen-plus"}}
	if msg := validateTenantAllocation(&list); msg != "" {
		t.Fatalf("allowlist = %q, want accepted", msg)
	}
	if len(list.AllowedModels) != 2 {
		t.Fatalf("allowlist = %v, want duplicates and blanks removed", list.AllowedModels)
	}
	if list.AllowedModels[0] != "campus-smart" {
		t.Fatalf("allowlist = %v, want trimmed aliases", list.AllowedModels)
	}
}

func TestValidateModelQuotaAllocation(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	cap := int64(1000)
	cases := []struct {
		name  string
		in    model.ModelQuotaAllocation
		ok    bool
		field string
	}{
		{"system pool", model.ModelQuotaAllocation{ModelAlias: "campus-smart", DailyTokenLimit: &cap}, true, ""},
		{"tenant row", model.ModelQuotaAllocation{ModelAlias: "campus-smart", TenantID: &tenant, DailyTokenLimit: &cap}, true, ""},
		{"alias trimmed", model.ModelQuotaAllocation{ModelAlias: "  campus-smart  ", MonthlyTokenLimit: &cap}, true, ""},
		{"missing alias", model.ModelQuotaAllocation{DailyTokenLimit: &cap}, false, "model_alias"},
		{"blank alias", model.ModelQuotaAllocation{ModelAlias: "   ", DailyTokenLimit: &cap}, false, "model_alias"},
		{"malformed tenant", model.ModelQuotaAllocation{ModelAlias: "m", TenantID: strptr("not-a-uuid"), DailyTokenLimit: &cap}, false, "tenant_id"},
		// Both limits left to inherit caps nothing; the row would only look
		// configured. Deleting the row is how a subject inherits again.
		{"no limit at all", model.ModelQuotaAllocation{ModelAlias: "campus-smart"}, false, "at least one limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := validateModelQuotaAllocation(&tc.in)
			if tc.ok {
				if msg != "" {
					t.Fatalf("rejected: %q", msg)
				}
				return
			}
			if msg == "" {
				t.Fatal("accepted an invalid allocation")
			}
			if !strings.Contains(msg, tc.field) {
				t.Fatalf("message %q does not name %q", msg, tc.field)
			}
		})
	}
}

// An empty tenant_id is how a client spells "the campus pool". It has to become
// nil explicitly: the storage layer distinguishes NULL from a real id, and a
// stray "" would fail the uuid cast.
func TestValidateModelQuotaTreatsBlankTenantAsTheSystemPool(t *testing.T) {
	cap := int64(1000)
	in := model.ModelQuotaAllocation{ModelAlias: "campus-smart", TenantID: strptr("  "), DailyTokenLimit: &cap}
	if msg := validateModelQuotaAllocation(&in); msg != "" {
		t.Fatalf("blank tenant id = %q, want accepted", msg)
	}
	if in.TenantID != nil {
		t.Fatalf("tenant_id = %q, want nil (the campus pool)", *in.TenantID)
	}
}

func TestValidateModelQuotaRejectsNegativeCaps(t *testing.T) {
	neg := int64(-5)
	for _, in := range []model.ModelQuotaAllocation{
		{ModelAlias: "campus-smart", DailyTokenLimit: &neg},
		{ModelAlias: "campus-smart", MonthlyTokenLimit: &neg},
	} {
		if msg := validateModelQuotaAllocation(&in); msg == "" {
			t.Fatalf("negative cap accepted for %+v", in)
		}
	}
	zero := int64(0)
	ok := model.ModelQuotaAllocation{ModelAlias: "campus-smart", DailyTokenLimit: &zero}
	if msg := validateModelQuotaAllocation(&ok); msg != "" {
		t.Fatalf("explicit zero = %q, want accepted: zero means unlimited", msg)
	}
}

// A scoped administrator may write only strict descendants. The campus pool is
// global, and the scope tenant's own row is the ceiling their parent set;
// letting them replace or delete it would raise their own limit back to the
// inherited value.
func TestModelQuotaSubjectReachableExcludesThePoolAndTheScopeTenant(t *testing.T) {
	scope := "11111111-1111-1111-1111-111111111111"
	other := "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name   string
		scope  string
		tenant *string
		want   bool
	}{
		{"global reaches the pool", "", nil, true},
		{"global reaches a tenant", "", &other, true},
		{"scoped cannot reach the pool", scope, nil, false},
		{"scoped cannot reach its own tenant", scope, &scope, false},
		{"scoped can reach a descendant candidate", scope, &other, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelQuotaSubjectReachable(tc.scope, tc.tenant); got != tc.want {
				t.Fatalf("reachable = %v, want %v", got, tc.want)
			}
		})
	}
}

func strptr(v string) *string { return &v }
