package app

import (
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The rate limits are the tenant's, and since 0014 they are three-state:
//
//	nil -> this level adds no cap (ancestors' caps still apply)
//	  0 -> explicitly unlimited at this level
//	 >0 -> this tenant's cap
//
// The bug this pins is the old behaviour on the key: validation silently
// rewrote a zero into 60 / 100000 / 10, so an operator who typed 0 to mean "no
// limit" got a hard cap instead. The tenant validator must not repeat that --
// and it must leave nil alone too. Rewriting nil to 0 would turn "inherit" into
// "unlimited", which is the one direction a child must never be able to move.
func TestValidateTenantLeavesTheThreeStatesAlone(t *testing.T) {
	inherit := model.Tenant{Code: "dept", Name: "Dept"}
	if reason := validateTenant(&inherit); reason != "" {
		t.Fatalf("a tenant with all limits unset must validate, got %q", reason)
	}
	if inherit.RPMLimit != nil || inherit.TPMLimit != nil || inherit.MaxConcurrency != nil {
		t.Fatalf("validation rewrote an unset limit: rpm=%v tpm=%v concurrency=%v",
			inherit.RPMLimit, inherit.TPMLimit, inherit.MaxConcurrency)
	}

	zero := int64(0)
	unlimited := model.Tenant{Code: "dept", Name: "Dept", RPMLimit: &zero, TPMLimit: &zero, MaxConcurrency: &zero}
	if reason := validateTenant(&unlimited); reason != "" {
		t.Fatalf("a tenant with explicit zeros must validate, got %q", reason)
	}
	if unlimited.RPMLimit == nil || *unlimited.RPMLimit != 0 {
		t.Fatalf("validation rewrote an explicit zero: rpm=%v", unlimited.RPMLimit)
	}

	rpm, tpm, conc := int64(60), int64(100000), int64(10)
	stated := model.Tenant{Code: "dept", Name: "Dept", RPMLimit: &rpm, TPMLimit: &tpm, MaxConcurrency: &conc}
	if reason := validateTenant(&stated); reason != "" {
		t.Fatalf("a tenant with explicit limits must validate, got %q", reason)
	}
	if stated.RPMLimit == nil || *stated.RPMLimit != 60 {
		t.Fatalf("validation altered an explicit limit: rpm=%v", stated.RPMLimit)
	}
	if stated.TPMLimit == nil || *stated.TPMLimit != 100000 {
		t.Fatalf("validation altered an explicit limit: tpm=%v", stated.TPMLimit)
	}
	if stated.MaxConcurrency == nil || *stated.MaxConcurrency != 10 {
		t.Fatalf("validation altered an explicit limit: concurrency=%v", stated.MaxConcurrency)
	}
}

func TestValidateTenantRejectsNegativeRateLimits(t *testing.T) {
	negative := int64(-1)
	cases := map[string]model.Tenant{
		"rpm":         {Code: "d", Name: "D", RPMLimit: &negative},
		"tpm":         {Code: "d", Name: "D", TPMLimit: &negative},
		"concurrency": {Code: "d", Name: "D", MaxConcurrency: &negative},
		"daily":       {Code: "d", Name: "D", DailyTokenLimit: &negative},
		"monthly":     {Code: "d", Name: "D", MonthlyTokenLimit: &negative},
		"requests":    {Code: "d", Name: "D", MonthlyRequestLimit: &negative},
	}
	for name, tenant := range cases {
		v := tenant
		if reason := validateTenant(&v); reason == "" {
			t.Fatalf("a negative %s limit must be rejected", name)
		}
	}
}

// A key no longer carries rate limits (ADR-0015), so key validation must accept
// a credential that names none — and must not demand them.
func TestValidateAPIKeyAcceptsACredentialWithNoLimits(t *testing.T) {
	v := model.APIKeyAdmin{Name: "app", TenantID: "11111111-1111-1111-1111-111111111111"}
	if reason := validateAPIKey(&v); reason != "" {
		t.Fatalf("a key with no limit fields must validate, got %q", reason)
	}
}
