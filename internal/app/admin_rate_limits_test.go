package app

import (
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// Limits are three-state since 0019: nil means inherit, 0 means explicitly
// unlimited, and a positive value is a cap. Validation must preserve all three
// distinctions rather than rewriting one into another. The bug this pins on the
// key was validation silently turning a zero into a hard default; the tenant
// validator must never do that, and must never turn a nil into a number either.
func TestValidateTenantPreservesUnsetAndZeroLimits(t *testing.T) {
	inherit := model.Tenant{Code: "dept", Name: "Dept"}
	if reason := validateTenant(&inherit); reason != "" {
		t.Fatalf("a tenant with all limits unset must validate, got %q", reason)
	}
	if inherit.RPMLimit != nil || inherit.TPMLimit != nil || inherit.MaxConcurrency != nil ||
		inherit.DailyTokenLimit != nil || inherit.MonthlyTokenLimit != nil || inherit.MonthlyRequestLimit != nil {
		t.Fatal("validation turned an inherited limit into a value")
	}

	zero := int64(0)
	unlimited := model.Tenant{
		Code: "dept", Name: "Dept",
		RPMLimit: &zero, TPMLimit: &zero, MaxConcurrency: &zero,
		MonthlyRequestLimit: &zero, DailyTokenLimit: &zero, MonthlyTokenLimit: &zero,
	}
	if reason := validateTenant(&unlimited); reason != "" {
		t.Fatalf("a tenant that is explicitly unlimited must validate, got %q", reason)
	}
	if unlimited.RPMLimit == nil || *unlimited.RPMLimit != 0 ||
		unlimited.TPMLimit == nil || *unlimited.TPMLimit != 0 ||
		unlimited.MaxConcurrency == nil || *unlimited.MaxConcurrency != 0 {
		t.Fatal("validation rewrote an explicit zero rate limit")
	}

	rpm, tpm, conc := int64(60), int64(100000), int64(10)
	stated := model.Tenant{Code: "dept", Name: "Dept", RPMLimit: &rpm, TPMLimit: &tpm, MaxConcurrency: &conc}
	if reason := validateTenant(&stated); reason != "" {
		t.Fatalf("a tenant with explicit limits must validate, got %q", reason)
	}
	if *stated.RPMLimit != 60 || *stated.TPMLimit != 100000 || *stated.MaxConcurrency != 10 {
		t.Fatalf("validation altered explicit limits: rpm=%d tpm=%d concurrency=%d",
			*stated.RPMLimit, *stated.TPMLimit, *stated.MaxConcurrency)
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
