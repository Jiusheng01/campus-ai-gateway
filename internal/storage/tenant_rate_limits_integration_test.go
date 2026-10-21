package storage

import (
	"context"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// Rate limits live on the Tenant now (ADR-0015). This pins the storage contract:
// the three values round-trip through create, update and list, and a zero is
// stored and read back verbatim so "unlimited" stays unlimited.
func TestTenantRateLimitsRoundTrip(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearTenantsAndKeys(t, pg)

	created, err := pg.CreateTenant(ctx, model.Tenant{
		Code: "rate-limits", Name: "Rate Limits", Status: 1,
		RPMLimit: 120, TPMLimit: 200000, MaxConcurrency: 25,
	})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if created.RPMLimit != 120 || created.TPMLimit != 200000 || created.MaxConcurrency != 25 {
		t.Fatalf("create returned rpm=%d tpm=%d concurrency=%d, want 120/200000/25",
			created.RPMLimit, created.TPMLimit, created.MaxConcurrency)
	}

	zero, err := pg.CreateTenant(ctx, model.Tenant{Code: "rate-zero", Name: "Zero", Status: 1})
	if err != nil {
		t.Fatalf("CreateTenant zero: %v", err)
	}
	if zero.RPMLimit != 0 || zero.TPMLimit != 0 || zero.MaxConcurrency != 0 {
		t.Fatalf("zero limits were not preserved on create: rpm=%d tpm=%d concurrency=%d",
			zero.RPMLimit, zero.TPMLimit, zero.MaxConcurrency)
	}

	created.RPMLimit = 60
	created.TPMLimit = 0
	created.MaxConcurrency = 5
	updated, err := pg.UpdateTenant(ctx, created.ID, TenantUpdate{Tenant: created, AllowedModels: &created.AllowedModels})
	if err != nil {
		t.Fatalf("UpdateTenant: %v", err)
	}
	if updated.RPMLimit != 60 || updated.TPMLimit != 0 || updated.MaxConcurrency != 5 {
		t.Fatalf("update returned rpm=%d tpm=%d concurrency=%d, want 60/0/5",
			updated.RPMLimit, updated.TPMLimit, updated.MaxConcurrency)
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
		if v.RPMLimit != 60 || v.TPMLimit != 0 || v.MaxConcurrency != 5 {
			t.Fatalf("list returned rpm=%d tpm=%d concurrency=%d, want 60/0/5",
				v.RPMLimit, v.TPMLimit, v.MaxConcurrency)
		}
	}
	if !found {
		t.Fatal("the created tenant is missing from ListTenants")
	}
}
