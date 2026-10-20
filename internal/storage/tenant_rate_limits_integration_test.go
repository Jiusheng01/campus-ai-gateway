package storage

import (
	"context"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// Rate limits live on the Tenant (ADR-0015) and are three-state since 0014.
// This pins the storage contract: nil (inherit), 0 (unlimited) and a positive
// cap all round-trip through create, update and list without collapsing into
// each other.
//
// The nil case is the one that matters most. A column that defaulted to 0 would
// make every newly created tenant unlimited, and a read path that flattened NULL
// to 0 would do the same on the way back -- so both halves of the round trip are
// asserted separately rather than trusting that "it is nullable" is enough.
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
	if created.RPMLimit == nil || *created.RPMLimit != 120 {
		t.Fatalf("create returned rpm=%v, want 120", created.RPMLimit)
	}
	if created.TPMLimit == nil || *created.TPMLimit != 200000 {
		t.Fatalf("create returned tpm=%v, want 200000", created.TPMLimit)
	}
	if created.MaxConcurrency == nil || *created.MaxConcurrency != 25 {
		t.Fatalf("create returned concurrency=%v, want 25", created.MaxConcurrency)
	}

	// No limits named at all must stay NULL: that is "inherit from my parent".
	inherit, err := pg.CreateTenant(ctx, model.Tenant{Code: "rate-inherit", Name: "Inherit", Status: 1})
	if err != nil {
		t.Fatalf("CreateTenant inherit: %v", err)
	}
	if inherit.RPMLimit != nil || inherit.TPMLimit != nil || inherit.MaxConcurrency != nil {
		t.Fatalf("unset limits came back non-nil: rpm=%v tpm=%v concurrency=%v",
			inherit.RPMLimit, inherit.TPMLimit, inherit.MaxConcurrency)
	}

	// An explicit zero is a declaration ("unlimited"), not an absence, and has
	// to survive as a non-nil zero.
	zero := int64(0)
	unlimited, err := pg.CreateTenant(ctx, model.Tenant{
		Code: "rate-zero", Name: "Zero", Status: 1,
		RPMLimit: &zero, TPMLimit: &zero, MaxConcurrency: &zero,
	})
	if err != nil {
		t.Fatalf("CreateTenant zero: %v", err)
	}
	if unlimited.RPMLimit == nil || *unlimited.RPMLimit != 0 {
		t.Fatalf("an explicit zero came back as %v, want a non-nil 0", unlimited.RPMLimit)
	}

	rpm2, conc2 := int64(60), int64(5)
	created.RPMLimit = &rpm2
	created.TPMLimit = &zero
	created.MaxConcurrency = &conc2
	updated, err := pg.UpdateTenant(ctx, created.ID, created)
	if err != nil {
		t.Fatalf("UpdateTenant: %v", err)
	}
	if updated.RPMLimit == nil || *updated.RPMLimit != 60 {
		t.Fatalf("update returned rpm=%v, want 60", updated.RPMLimit)
	}
	if updated.TPMLimit == nil || *updated.TPMLimit != 0 {
		t.Fatalf("update returned tpm=%v, want a non-nil 0", updated.TPMLimit)
	}
	if updated.MaxConcurrency == nil || *updated.MaxConcurrency != 5 {
		t.Fatalf("update returned concurrency=%v, want 5", updated.MaxConcurrency)
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
		if v.RPMLimit == nil || *v.RPMLimit != 60 {
			t.Fatalf("list returned rpm=%v, want 60", v.RPMLimit)
		}
		if v.TPMLimit == nil || *v.TPMLimit != 0 {
			t.Fatalf("list returned tpm=%v, want a non-nil 0", v.TPMLimit)
		}
		if v.MaxConcurrency == nil || *v.MaxConcurrency != 5 {
			t.Fatalf("list returned concurrency=%v, want 5", v.MaxConcurrency)
		}
	}
	if !found {
		t.Fatal("the created tenant is missing from ListTenants")
	}
}
