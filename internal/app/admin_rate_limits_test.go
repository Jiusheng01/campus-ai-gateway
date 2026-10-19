package app

import (
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The rate limits are the tenant's, and zero means unlimited (ADR-0015). The
// bug this pins is the old behaviour on the key: validation silently rewrote a
// zero into 60 / 100000 / 10, so an operator who typed 0 to mean "no limit" got
// a hard cap instead. The tenant validator must not repeat that — a zero has to
// survive validation unchanged.
func TestValidateTenantTreatsZeroAsUnlimitedAndDoesNotRewriteIt(t *testing.T) {
	unlimited := model.Tenant{Code: "dept", Name: "Dept"}
	if reason := validateTenant(&unlimited); reason != "" {
		t.Fatalf("a tenant with all limits unset must validate, got %q", reason)
	}
	if unlimited.RPMLimit != 0 || unlimited.TPMLimit != 0 || unlimited.MaxConcurrency != 0 {
		t.Fatalf("validation rewrote a zero rate limit: rpm=%d tpm=%d concurrency=%d",
			unlimited.RPMLimit, unlimited.TPMLimit, unlimited.MaxConcurrency)
	}

	stated := model.Tenant{Code: "dept", Name: "Dept", RPMLimit: 60, TPMLimit: 100000, MaxConcurrency: 10}
	if reason := validateTenant(&stated); reason != "" {
		t.Fatalf("a tenant with explicit limits must validate, got %q", reason)
	}
	if stated.RPMLimit != 60 || stated.TPMLimit != 100000 || stated.MaxConcurrency != 10 {
		t.Fatalf("validation altered explicit limits: rpm=%d tpm=%d concurrency=%d",
			stated.RPMLimit, stated.TPMLimit, stated.MaxConcurrency)
	}
}

func TestValidateTenantRejectsNegativeRateLimits(t *testing.T) {
	cases := map[string]model.Tenant{
		"rpm":         {Code: "d", Name: "D", RPMLimit: -1},
		"tpm":         {Code: "d", Name: "D", TPMLimit: -1},
		"concurrency": {Code: "d", Name: "D", MaxConcurrency: -1},
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
