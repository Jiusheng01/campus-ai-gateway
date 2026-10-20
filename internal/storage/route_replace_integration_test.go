package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

func routeSetFixture(t *testing.T) (*Postgres, model.ModelConfig, []model.Provider, []model.ModelRoute) {
	t.Helper()
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)
	p1 := mustProvider(t, pg, "replace-p1")
	p2 := mustProvider(t, pg, "replace-p2")
	created, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "replace-model", Status: 1, ProviderID: p1.ID, UpstreamModel: "up-1",
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if _, err := pg.CreateModelRoute(ctx, created.ID, model.ModelRoute{
		ProviderID: p2.ID, UpstreamModel: "up-2", Priority: 2, Weight: 100, TimeoutMS: 60000, Status: 1,
	}); err != nil {
		t.Fatalf("CreateModelRoute: %v", err)
	}
	routes, err := pg.ListModelRoutes(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListModelRoutes: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("fixture routes = %+v", routes)
	}
	return pg, created, []model.Provider{p1, p2}, routes
}

func TestReplaceModelRoutesIsAtomicAndPreservesIds(t *testing.T) {
	ctx := context.Background()
	pg, created, providers, routes := routeSetFixture(t)
	before, err := pg.ModelByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("ModelByID: %v", err)
	}

	out := []model.ModelRoute{
		{ID: routes[1].ID, ProviderID: providers[1].ID, UpstreamModel: "up-2", Priority: 1, Weight: 100, TimeoutMS: 30000, Status: 1},
		{ID: routes[0].ID, ProviderID: providers[0].ID, UpstreamModel: "up-1", Priority: 2, Weight: 100, TimeoutMS: 60000, Status: 1},
		{ProviderID: providers[0].ID, UpstreamModel: "up-3", Priority: 3, Weight: 50, TimeoutMS: 60000, Status: 1},
	}
	updated, saved, err := pg.ReplaceModelRoutes(ctx, created.ID, before.ConfigVersion, out)
	if err != nil {
		t.Fatalf("ReplaceModelRoutes: %v", err)
	}
	if updated.ConfigVersion != before.ConfigVersion+1 {
		t.Fatalf("version = %d, want %d", updated.ConfigVersion, before.ConfigVersion+1)
	}
	if len(saved) != 3 {
		t.Fatalf("saved routes = %+v", saved)
	}
	ids := map[string]bool{}
	for _, r := range saved {
		ids[r.ID] = true
	}
	if !ids[routes[1].ID] || !ids[routes[0].ID] {
		t.Fatalf("unchanged route ids were not preserved: %+v", saved)
	}

	if _, _, err := pg.ReplaceModelRoutes(ctx, created.ID, before.ConfigVersion, out); !errors.Is(err, ErrConfigVersionConflict) {
		t.Fatalf("stale replace = %v, want conflict", err)
	}
	after, _ := pg.ModelByID(ctx, created.ID)
	if after.ConfigVersion != updated.ConfigVersion {
		t.Fatalf("a conflict advanced the version: %d -> %d", updated.ConfigVersion, after.ConfigVersion)
	}
}

func TestLegacyRouteWriteUsesTheSameVersionAndStalesAGroupSave(t *testing.T) {
	ctx := context.Background()
	pg, created, providers, _ := routeSetFixture(t)
	before, _ := pg.ModelByID(ctx, created.ID)

	if _, err := pg.CreateModelRoute(ctx, created.ID, model.ModelRoute{
		ProviderID: providers[0].ID, UpstreamModel: "up-3", Priority: 3, Weight: 100, TimeoutMS: 60000, Status: 1,
	}); err != nil {
		t.Fatalf("legacy CreateModelRoute: %v", err)
	}
	afterLegacy, _ := pg.ModelByID(ctx, created.ID)
	if afterLegacy.ConfigVersion != before.ConfigVersion+1 {
		t.Fatalf("legacy write version = %d, want %d", afterLegacy.ConfigVersion, before.ConfigVersion+1)
	}
	if _, _, err := pg.ReplaceModelRoutes(ctx, created.ID, before.ConfigVersion, []model.ModelRoute{
		{ID: "", ProviderID: providers[0].ID, UpstreamModel: "up-1", Priority: 1, Weight: 100, TimeoutMS: 60000, Status: 1},
	}); !errors.Is(err, ErrConfigVersionConflict) {
		t.Fatalf("group save after legacy write = %v, want conflict", err)
	}
}

func TestProviderDeleteBumpsSurvivingModelVersion(t *testing.T) {
	ctx := context.Background()
	pg, created, providers, _ := routeSetFixture(t)
	before, _ := pg.ModelByID(ctx, created.ID)

	result, err := pg.DeleteProvider(ctx, providers[0].ID)
	if err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if result.DeletedRoutes != 1 {
		t.Fatalf("deleted routes = %d, want 1", result.DeletedRoutes)
	}
	after, err := pg.ModelByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("ModelByID after delete: %v", err)
	}
	if after.ConfigVersion != before.ConfigVersion+1 {
		t.Fatalf("surviving model version = %d, want %d", after.ConfigVersion, before.ConfigVersion+1)
	}
	routes, err := pg.ListModelRoutes(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListModelRoutes: %v", err)
	}
	if len(routes) != 1 || routes[0].ProviderID != providers[1].ID {
		t.Fatalf("remaining routes = %+v", routes)
	}
}

func TestProviderDeleteAndGroupSaveDoNotDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pg, created, providers, routes := routeSetFixture(t)
	before, _ := pg.ModelByID(ctx, created.ID)
	out := []model.ModelRoute{
		{ID: routes[0].ID, ProviderID: providers[0].ID, UpstreamModel: "up-1", Priority: 1, Weight: 100, TimeoutMS: 60000, Status: 1},
		{ID: routes[1].ID, ProviderID: providers[1].ID, UpstreamModel: "up-2", Priority: 2, Weight: 100, TimeoutMS: 60000, Status: 1},
	}

	var wg sync.WaitGroup
	var deleteErr, groupErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, deleteErr = pg.DeleteProvider(ctx, providers[0].ID)
	}()
	go func() {
		defer wg.Done()
		_, _, groupErr = pg.ReplaceModelRoutes(ctx, created.ID, before.ConfigVersion, out)
	}()
	wg.Wait()
	if ctx.Err() != nil {
		t.Fatal("provider delete and group save did not finish within 10s")
	}
	if deleteErr != nil {
		t.Fatalf("provider delete: %v", deleteErr)
	}
	if groupErr != nil && !errors.Is(groupErr, ErrConfigVersionConflict) {
		t.Fatalf("group save: %v", groupErr)
	}
	// Whatever the ordering, no route may reference the deleted provider.
	var dangling int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM gateway_model_route WHERE provider_id=$1::uuid`, providers[0].ID).Scan(&dangling); err != nil {
		t.Fatalf("count dangling routes: %v", err)
	}
	if dangling != 0 {
		t.Fatalf("%d route(s) still reference the deleted provider", dangling)
	}
}

func TestLegacyAndGroupWritesSerializeWithoutHalfState(t *testing.T) {
	ctx := context.Background()
	pg, created, providers, routes := routeSetFixture(t)
	before, _ := pg.ModelByID(ctx, created.ID)
	out := []model.ModelRoute{
		{ID: routes[0].ID, ProviderID: providers[0].ID, UpstreamModel: "up-1", Priority: 1, Weight: 100, TimeoutMS: 60000, Status: 1},
		{ID: routes[1].ID, ProviderID: providers[1].ID, UpstreamModel: "up-2", Priority: 2, Weight: 100, TimeoutMS: 60000, Status: 1},
	}

	var wg sync.WaitGroup
	var groupErr, legacyErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, groupErr = pg.ReplaceModelRoutes(ctx, created.ID, before.ConfigVersion, out)
	}()
	go func() {
		defer wg.Done()
		_, legacyErr = pg.CreateModelRoute(ctx, created.ID, model.ModelRoute{
			ProviderID: providers[0].ID, UpstreamModel: "up-concurrent", Priority: 3, Weight: 100, TimeoutMS: 60000, Status: 1,
		})
	}()
	wg.Wait()
	if legacyErr != nil {
		t.Fatalf("legacy write failed: %v", legacyErr)
	}
	if groupErr != nil && !errors.Is(groupErr, ErrConfigVersionConflict) {
		t.Fatalf("group save failed with %v", groupErr)
	}
	after, _ := pg.ModelByID(ctx, created.ID)
	wantVersion := before.ConfigVersion + 1
	if groupErr == nil {
		wantVersion++
	}
	if after.ConfigVersion != wantVersion {
		t.Fatalf("final version = %d, want %d (group err %v)", after.ConfigVersion, wantVersion, groupErr)
	}
	saved, _ := pg.ListModelRoutes(ctx, created.ID)
	// The fixture starts with two routes, and the legacy write always adds one.
	// Whether the group save won or conflicted, the final set is those three
	// routes; the version assertion above distinguishes the two orderings.
	if len(saved) != 3 {
		t.Fatalf("final routes = %d, want 3: %+v", len(saved), saved)
	}
}
