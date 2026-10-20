package app

import (
	"context"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// A write must be visible to the next request in the saving instance, while the
// table is still built from the database snapshot rather than from the write
// handler's own view.
func TestRouteCacheSeesACommittedWriteAfterInvalidation(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	ctx := context.Background()
	provider, err := pg.CreateProvider(ctx, model.Provider{
		Name: "cache-provider", BaseURL: "https://cache.example.test/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	if _, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "cache-first", Status: 1, ProviderID: provider.ID, UpstreamModel: "up-1",
	}); err != nil {
		t.Fatalf("CreateModel first: %v", err)
	}
	s := providerModelTestServer(t, pg)
	first, err := s.routeTableCached(ctx)
	if err != nil {
		t.Fatalf("first routeTableCached: %v", err)
	}
	if _, ok := first.models["cache-first"]; !ok {
		t.Fatalf("first table = %+v", first.models)
	}
	if _, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "cache-second", Status: 1, ProviderID: provider.ID, UpstreamModel: "up-2",
	}); err != nil {
		t.Fatalf("CreateModel second: %v", err)
	}
	s.invalidateRouteCache()
	second, err := s.routeTableCached(ctx)
	if err != nil {
		t.Fatalf("second routeTableCached: %v", err)
	}
	if _, ok := second.models["cache-second"]; !ok {
		t.Fatalf("the committed write was not visible after invalidation: %+v", second.models)
	}
}
