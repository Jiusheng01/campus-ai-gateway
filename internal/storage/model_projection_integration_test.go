package storage

import (
	"context"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The management list reads a projection of the route set; it must not invent a
// second routing state. This pins the two fields the console uses to distinguish
// "configured" from "statically routable" and the route-count summary.
func TestListModelsReportsRouteProjection(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)

	primary := mustProvider(t, pg, "projection-primary")
	backup := mustProvider(t, pg, "projection-backup")

	created, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "projection-model", DisplayName: "Projection Model", Status: 1,
		ProviderID: primary.ID, UpstreamModel: "up-primary",
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if created.ConfigVersion != 1 || created.DisplayName != "Projection Model" {
		t.Fatalf("create returned %+v", created)
	}
	if _, err := pg.CreateModelRoute(ctx, created.ID, model.ModelRoute{
		ProviderID: backup.ID, UpstreamModel: "up-backup", Priority: 2, Weight: 100, TimeoutMS: 60000, Status: 1,
	}); err != nil {
		t.Fatalf("CreateModelRoute: %v", err)
	}

	listed, err := pg.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	var got *model.ModelConfig
	for i := range listed {
		if listed[i].ID == created.ID {
			got = &listed[i]
		}
	}
	if got == nil {
		t.Fatal("created model missing from ListModels")
	}
	if got.RouteCount != 2 || got.EnabledRouteCount != 2 {
		t.Fatalf("route counts = %d/%d, want 2/2", got.RouteCount, got.EnabledRouteCount)
	}
	if !got.StaticallyRoutable || !got.CredentialConfigured {
		t.Fatalf("projection = %+v, want statically routable and credential configured", got)
	}
	if got.RoutingModeSummary != "主备 2 层" {
		t.Fatalf("routing mode summary = %q", got.RoutingModeSummary)
	}

	detail, err := pg.ModelByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("ModelByID: %v", err)
	}
	if detail.ConfigVersion != got.ConfigVersion || detail.RouteCount != 2 {
		t.Fatalf("detail = %+v, list = %+v", detail, *got)
	}
}
