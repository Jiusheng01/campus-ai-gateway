package storage

import (
	"context"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// Rate limits live on the Tenant and are three-state since 0019. This pins the
// storage contract: an inherited limit stays nil, an explicit zero stays 0, and
// a positive cap stays itself through create, allocation and list.
func TestTenantRateLimitsRoundTrip(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearTenantsAndKeys(t, pg)

	rpm, tpm, conc := int64(120), int64(200000), int64(25)
	created, err := pg.CreateTenant(ctx, model.Tenant{
		Code: "rate-limits", Name: "Rate Limits", Status: 1,
		RPMLimit: &rpm, TPMLimit: &tpm, MaxConcurrency: &conc,
	})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if created.RPMLimit == nil || *created.RPMLimit != 120 ||
		created.TPMLimit == nil || *created.TPMLimit != 200000 ||
		created.MaxConcurrency == nil || *created.MaxConcurrency != 25 {
		t.Fatalf("create returned limits that do not match: %+v", created)
	}

	inherited, err := pg.CreateTenant(ctx, model.Tenant{Code: "rate-zero", Name: "Zero", Status: 1})
	if err != nil {
		t.Fatalf("CreateTenant inherited: %v", err)
	}
	if inherited.RPMLimit != nil || inherited.TPMLimit != nil || inherited.MaxConcurrency != nil {
		t.Fatalf("an unset limit was not stored as inherit: %+v", inherited)
	}

	zero := int64(0)
	rpm, conc = 60, 5
	updated, err := pg.AllocateTenantQuota(ctx, created.ID, model.TenantAllocation{
		RPMLimit: &rpm, TPMLimit: &zero, MaxConcurrency: &conc,
	})
	if err != nil {
		t.Fatalf("AllocateTenantQuota: %v", err)
	}
	if updated.RPMLimit == nil || *updated.RPMLimit != 60 ||
		updated.TPMLimit == nil || *updated.TPMLimit != 0 ||
		updated.MaxConcurrency == nil || *updated.MaxConcurrency != 5 {
		t.Fatalf("allocation returned limits that do not match: %+v", updated)
	}

	listed, err := pg.ListTenants(ctx)
	if err != nil {
		t.Fatalf("ListTenants: %v", err)
	}
	var found bool
	for _, v := range listed {
		if v.ID != created.ID {
			continue
		}
		found = true
		if v.RPMLimit == nil || *v.RPMLimit != 60 ||
			v.TPMLimit == nil || *v.TPMLimit != 0 ||
			v.MaxConcurrency == nil || *v.MaxConcurrency != 5 {
			t.Fatalf("list returned limits that do not match: %+v", v)
		}
	}
	if !found {
		t.Fatal("the created tenant is missing from ListTenants")
	}

	// A rename must not disturb the allocation. This is the property that
	// motivated moving quota writes out of UpdateTenant.
	renamed, err := pg.UpdateTenant(ctx, created.ID, TenantUpdate{Tenant: model.Tenant{Code: "rate-limits", Name: "Renamed", Status: 1}})
	if err != nil {
		t.Fatalf("UpdateTenant: %v", err)
	}
	if renamed.Name != "Renamed" {
		t.Fatalf("rename did not take effect: %q", renamed.Name)
	}
	if renamed.RPMLimit == nil || *renamed.RPMLimit != 60 ||
		renamed.TPMLimit == nil || *renamed.TPMLimit != 0 ||
		renamed.MaxConcurrency == nil || *renamed.MaxConcurrency != 5 {
		t.Fatalf("a rename rewrote the allocation: %+v", renamed)
	}
}
