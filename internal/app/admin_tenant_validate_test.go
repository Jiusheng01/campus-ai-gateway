package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// validTenant is a tenant that passes validation, so each case only has to
// state the single field it is exercising.
func validTenant() model.Tenant {
	return model.Tenant{
		Code:   "dept-cs",
		Name:   "计算机学院",
		Status: 1,
		Kind:   model.TenantKindDept,
	}
}

// TestValidateTenantNamesTheMissingField pins the fix for a form that answered
// every omission with one generic message. An operator who had filled in the
// name but missed the code was told "code and name are required" and had no way
// to tell which field was actually at fault.
func TestValidateTenantNamesTheMissingField(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*model.Tenant)
		want   string
	}{
		{
			name:   "missing code names the code",
			mutate: func(v *model.Tenant) { v.Code = "" },
			want:   "tenant code is required",
		},
		{
			name:   "missing name names the name",
			mutate: func(v *model.Tenant) { v.Name = "" },
			want:   "tenant name is required",
		},
		{
			name:   "missing both names both",
			mutate: func(v *model.Tenant) { v.Code = ""; v.Name = "" },
			want:   "tenant code and name are required",
		},
		{
			// A field of spaces is empty as far as the operator is concerned;
			// validating before the trim would let it through.
			name:   "whitespace-only code counts as missing",
			mutate: func(v *model.Tenant) { v.Code = "   " },
			want:   "tenant code is required",
		},
		{
			name:   "whitespace-only name counts as missing",
			mutate: func(v *model.Tenant) { v.Name = "\t\n " },
			want:   "tenant name is required",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := validTenant()
			c.mutate(&v)
			if got := validateTenant(&v); got != c.want {
				t.Fatalf("validateTenant() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestValidateTenantAcceptsAWellFormedTenant(t *testing.T) {
	v := validTenant()
	if got := validateTenant(&v); got != "" {
		t.Fatalf("validateTenant() rejected a valid tenant: %q", got)
	}
}

func TestValidateTenantNormalisesInput(t *testing.T) {
	v := model.Tenant{Code: "  dept-cs  ", Name: "  计算机学院  "}
	if got := validateTenant(&v); got != "" {
		t.Fatalf("validateTenant() = %q, want no error", got)
	}
	if v.Code != "dept-cs" || v.Name != "计算机学院" {
		t.Fatalf("code/name were not trimmed: %q / %q", v.Code, v.Name)
	}
	// Status 0 is the zero value from a form that never set it, so it means
	// "enabled" rather than "invalid".
	if v.Status != 1 {
		t.Fatalf("status = %d, want 1 (enabled)", v.Status)
	}
	// An omitted kind falls back to the generic level.
	if v.Kind != model.TenantKindTenant {
		t.Fatalf("kind = %q, want %q", v.Kind, model.TenantKindTenant)
	}
}

func TestValidateTenantRejectsUnknownKind(t *testing.T) {
	v := validTenant()
	v.Kind = "faculty"
	if got := validateTenant(&v); got == "" {
		t.Fatal("validateTenant() accepted an unknown kind")
	}
}

func TestValidateTenantRejectsInvalidStatus(t *testing.T) {
	v := validTenant()
	v.Status = 7
	if got := validateTenant(&v); got == "" {
		t.Fatal("validateTenant() accepted status 7")
	}
}

// TestValidateTenantNamesTheNegativeQuota keeps the error actionable: the
// operator needs to know which of the five quota dimensions was wrong.
func TestValidateTenantNamesTheNegativeQuota(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*model.Tenant)
		field  string
	}{
		{"daily", func(v *model.Tenant) { v.DailyTokenLimit = i64ptr(-1) }, "daily_token_limit"},
		{"monthly", func(v *model.Tenant) { v.MonthlyTokenLimit = i64ptr(-1) }, "monthly_token_limit"},
		{"requests", func(v *model.Tenant) { v.MonthlyRequestLimit = i64ptr(-1) }, "monthly_request_limit"},
		{"budget", func(v *model.Tenant) { v.MonthlyBudgetMicros = i64ptr(-1) }, "monthly_budget_micros"},
		{"soft", func(v *model.Tenant) { v.SoftTokenLimit = i64ptr(-1) }, "soft_token_limit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := validTenant()
			c.mutate(&v)
			got := validateTenant(&v)
			if got == "" {
				t.Fatalf("validateTenant() accepted a negative %s", c.field)
			}
			if !strings.Contains(got, c.field) {
				t.Fatalf("error %q does not name the offending field %q", got, c.field)
			}
		})
	}
}

// TestValidateTenantAllowsZeroQuota guards the tri-state contract: 0 means
// "unlimited" and must not be confused with a missing value.
func TestValidateTenantAllowsZeroQuota(t *testing.T) {
	v := validTenant()
	v.DailyTokenLimit = i64ptr(0)
	v.MonthlyTokenLimit = i64ptr(0)
	if got := validateTenant(&v); got != "" {
		t.Fatalf("validateTenant() rejected 0 (=unlimited): %q", got)
	}
}

func TestValidateTenantAllowsNilQuota(t *testing.T) {
	v := validTenant()
	// nil means "inherit from the nearest ancestor that sets one".
	if got := validateTenant(&v); got != "" {
		t.Fatalf("validateTenant() rejected nil (=inherit): %q", got)
	}
}

func TestValidateTenantRejectsNegativeRateLimits(t *testing.T) {
	v := validTenant()
	v.RPMLimit = -1
	if got := validateTenant(&v); got == "" {
		t.Fatal("validateTenant() accepted a negative rpm_limit")
	}
	v = validTenant()
	v.TPMLimit = -1
	if got := validateTenant(&v); got == "" {
		t.Fatal("validateTenant() accepted a negative tpm_limit")
	}
}

func TestValidateTenantNormalisesAllowedModels(t *testing.T) {
	v := validTenant()
	v.AllowedModels = []string{"qwen-max", " qwen-max ", "", "  ", "gpt-4o"}
	if got := validateTenant(&v); got != "" {
		t.Fatalf("validateTenant() = %q, want no error", got)
	}
	want := []string{"qwen-max", "gpt-4o"}
	if !reflect.DeepEqual(v.AllowedModels, want) {
		t.Fatalf("allowed_models = %#v, want %#v", v.AllowedModels, want)
	}
}

// TestValidateTenantPreservesNilVersusEmptyModels pins the distinction the
// quota chain depends on: nil inherits the parent allowlist, while an empty
// (non-nil) list denies every model. Collapsing one into the other would either
// silently widen or silently block access.
func TestValidateTenantPreservesNilVersusEmptyModels(t *testing.T) {
	inherit := validTenant()
	inherit.AllowedModels = nil
	if got := validateTenant(&inherit); got != "" {
		t.Fatalf("validateTenant() = %q, want no error", got)
	}
	if inherit.AllowedModels != nil {
		t.Fatalf("nil allowed_models became %#v, want nil (=inherit)", inherit.AllowedModels)
	}

	denyAll := validTenant()
	denyAll.AllowedModels = []string{}
	if got := validateTenant(&denyAll); got != "" {
		t.Fatalf("validateTenant() = %q, want no error", got)
	}
	if denyAll.AllowedModels == nil {
		t.Fatal("empty allowed_models became nil (=inherit); it must stay [] (=deny all)")
	}
	if len(denyAll.AllowedModels) != 0 {
		t.Fatalf("allowed_models = %#v, want empty", denyAll.AllowedModels)
	}
}
