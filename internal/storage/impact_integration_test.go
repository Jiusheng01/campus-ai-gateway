package storage

import (
	"context"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

func TestImpactPreviewsCountCascadeAndKeyReferences(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)
	for _, stmt := range []string{`DELETE FROM gateway_api_key`, `DELETE FROM gateway_tenant`} {
		if _, err := pg.Pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	p1 := mustProvider(t, pg, "impact-p1")
	p2 := mustProvider(t, pg, "impact-p2")
	orphan, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "impact-orphan", Status: 1, ProviderID: p1.ID, UpstreamModel: "up-1",
	})
	if err != nil {
		t.Fatalf("CreateModel orphan: %v", err)
	}
	shared, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "impact-shared", Status: 1, ProviderID: p1.ID, UpstreamModel: "up-2",
	})
	if err != nil {
		t.Fatalf("CreateModel shared: %v", err)
	}
	if _, err := pg.CreateModelRoute(ctx, shared.ID, model.ModelRoute{
		ProviderID: p2.ID, UpstreamModel: "up-3", Priority: 2, Weight: 100, TimeoutMS: 60000, Status: 1,
	}); err != nil {
		t.Fatalf("CreateModelRoute: %v", err)
	}

	impact, err := pg.ProviderImpact(ctx, p1.ID)
	if err != nil {
		t.Fatalf("ProviderImpact: %v", err)
	}
	if impact.Routes != 2 || impact.OrphanedModels != 1 || len(impact.AffectedModels) != 2 {
		t.Fatalf("impact = %+v", impact)
	}
	for _, m := range impact.AffectedModels {
		switch m.ModelID {
		case orphan.ID:
			if !m.Orphaned || m.RemainingRoutes != 0 {
				t.Fatalf("orphan entry = %+v", m)
			}
		case shared.ID:
			if m.Orphaned || m.RemainingRoutes != 1 {
				t.Fatalf("shared entry = %+v", m)
			}
		}
	}

	tenant, err := pg.CreateTenant(ctx, model.Tenant{Code: "impact-tenant", Name: "Impact", Status: 1})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := pg.CreateAPIKey(ctx, model.APIKeyAdmin{
		TenantID: tenant.ID, Name: "impact-explicit", Status: 1, AllowedModels: []string{"impact-orphan"},
	}, "impact-digest-1"); err != nil {
		t.Fatalf("CreateAPIKey explicit: %v", err)
	}
	if _, err := pg.CreateAPIKey(ctx, model.APIKeyAdmin{
		TenantID: tenant.ID, Name: "impact-wildcard", Status: 1, AllowedModels: []string{model.AllModelsWildcard},
	}, "impact-digest-2"); err != nil {
		t.Fatalf("CreateAPIKey wildcard: %v", err)
	}
	modelImpact, err := pg.ModelImpact(ctx, orphan.ID)
	if err != nil {
		t.Fatalf("ModelImpact: %v", err)
	}
	if modelImpact.ExplicitKeys != 1 || modelImpact.WildcardKeys != 1 || modelImpact.EnabledRoutes != 1 {
		t.Fatalf("model impact = %+v", modelImpact)
	}
}
