package storage

import (
	"context"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// ADR-0005: Route is the sole model routing authority, so a model carries only
// its client-facing identity and its routing columns are gone. These tests pin
// the two halves of that: creating a model writes its first Route, and editing
// it rewrites that same Route rather than whichever Route is currently best.

// clearModelsAndProviders empties the model routing tables between cases. Routes
// and models go first: both reference the provider.
func clearModelsAndProviders(t *testing.T, pg *Postgres) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM gateway_model_route`,
		`DELETE FROM gateway_model`,
		`DELETE FROM gateway_provider`,
	} {
		if _, err := pg.Pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func mustProvider(t *testing.T, pg *Postgres, name string) model.Provider {
	t.Helper()
	p, err := pg.CreateProvider(context.Background(), model.Provider{
		Name:          name,
		BaseURL:       "https://" + name + ".example.test",
		Protocol:      "openai-chat",
		CredentialEnv: "TEST_API_KEY",
		Status:        1,
	})
	if err != nil {
		t.Fatalf("CreateProvider %s: %v", name, err)
	}
	return p
}

func routeByUpstream(routes []model.ModelRoute, upstream string) *model.ModelRoute {
	for i := range routes {
		if routes[i].UpstreamModel == upstream {
			return &routes[i]
		}
	}
	return nil
}

// Creating a model writes the model and its first Route, and the model itself
// round-trips with identity only — there is no routing state on it to drift.
func TestCreateModelWritesFirstRoute(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)

	ch := mustProvider(t, pg, "chan-create")

	created, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "campus-smart", ProviderID: ch.ID, UpstreamModel: "qwen-max", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if created.ID == "" || created.Alias != "campus-smart" || created.Status != 1 {
		t.Fatalf("create returned %+v", created)
	}
	if created.ProviderID != "" || created.ProviderName != "" || created.UpstreamModel != "" {
		t.Fatalf("the model acknowledgement must not carry routing fields, got %q/%q/%q",
			created.ProviderID, created.ProviderName, created.UpstreamModel)
	}

	routes, err := pg.ListModelRoutes(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListModelRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("create must write exactly one route, got %d", len(routes))
	}
	if r := routes[0]; r.ProviderID != ch.ID || r.UpstreamModel != "qwen-max" || r.Priority != 1 || r.Status != 1 {
		t.Fatalf("first route = %+v", r)
	}

	listed, err := pg.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	var found *model.ModelConfig
	for i := range listed {
		if listed[i].ID == created.ID {
			found = &listed[i]
		}
	}
	if found == nil {
		t.Fatal("the created model is missing from ListModels")
	}
	if found.ProviderID != "" || found.ProviderName != "" || found.UpstreamModel != "" {
		t.Fatalf("the model read must not carry routing fields, got %q/%q/%q",
			found.ProviderID, found.ProviderName, found.UpstreamModel)
	}
}

// Editing a model changes its alias and status only. It never rewrites a Route,
// not the first one and not the best enabled one.
//
// The legacy path picked the highest-priority enabled Route and overwrote it, so
// disabling the primary and then editing the model silently re-pointed the
// backup line. Routing is now edited through the Route API one Route at a time;
// a model edit must leave every Route byte-for-byte alone even when the request
// carries a binding.
func TestUpdateModelChangesIdentityOnlyAndLeavesRoutesAlone(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)

	primary := mustProvider(t, pg, "chan-primary")
	backup := mustProvider(t, pg, "chan-backup")

	created, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "failover", ProviderID: primary.ID, UpstreamModel: "up-primary", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if _, err := pg.CreateModelRoute(ctx, created.ID, model.ModelRoute{
		ProviderID: backup.ID, UpstreamModel: "up-backup", Priority: 2, Weight: 100, TimeoutMS: 60000, Status: 1,
	}); err != nil {
		t.Fatalf("CreateModelRoute: %v", err)
	}

	routes, err := pg.ListModelRoutes(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListModelRoutes: %v", err)
	}
	first := routeByUpstream(routes, "up-primary")
	second := routeByUpstream(routes, "up-backup")
	if first == nil || second == nil {
		t.Fatalf("expected both routes, got %+v", routes)
	}

	// Disable the first route, leaving only the backup enabled. Under the old
	// ordering this is what pushed an edit onto the backup.
	if _, err := pg.UpdateModelRoute(ctx, first.ID, model.ModelRoute{
		ProviderID: primary.ID, UpstreamModel: "up-primary", Priority: 1, Weight: 100, TimeoutMS: 60000, Status: 2,
	}); err != nil {
		t.Fatalf("disable first route: %v", err)
	}

	// Edit the model's identity and status. The request deliberately carries a
	// binding too: it must be ignored, because routing is not model state.
	updated, err := pg.UpdateModel(ctx, created.ID, model.ModelConfig{
		Alias: "failover-renamed", ProviderID: primary.ID, UpstreamModel: "up-primary-v2", Status: 2,
	})
	if err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}
	if updated.Alias != "failover-renamed" || updated.Status != 2 {
		t.Fatalf("the edit must change alias and status, got %+v", updated)
	}
	if updated.ProviderID != "" || updated.ProviderName != "" || updated.UpstreamModel != "" {
		t.Fatalf("the model acknowledgement must not carry routing fields, got %q/%q/%q",
			updated.ProviderID, updated.ProviderName, updated.UpstreamModel)
	}

	routes, err = pg.ListModelRoutes(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListModelRoutes after edit: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("a model edit must not add or remove routes, got %+v", routes)
	}
	editedFirst := routeByUpstream(routes, "up-primary")
	editedBackup := routeByUpstream(routes, "up-backup")
	if editedFirst == nil || editedFirst.ID != first.ID || editedFirst.Status != 2 || editedFirst.ProviderID != primary.ID {
		t.Fatalf("the first route must be untouched by a model edit, got %+v", routes)
	}
	if editedBackup == nil || editedBackup.ID != second.ID || editedBackup.ProviderID != backup.ID {
		t.Fatalf("the backup route must be untouched by a model edit, got %+v", routes)
	}
	if routeByUpstream(routes, "up-primary-v2") != nil {
		t.Fatalf("the update's binding must not be written to any route, got %+v", routes)
	}
}
