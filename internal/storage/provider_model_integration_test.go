package storage

import (
	"context"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The Provider Model list is data attached to a channel. These tests exercise
// the two properties the console depends on: a list round-trips with its
// source, and merging upstream names can add rows but can never take a manual
// row away or rewrite where an existing name came from.
func TestProviderModelListRoundTripsWithItsSource(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)

	created, err := pg.CreateProviderWithPresetModels(ctx, model.Provider{
		Name:          "preset-channel",
		BaseURL:       "https://preset.example.test",
		Protocol:      "openai-chat",
		CredentialEnv: "TEST_PROVIDER_MODEL_KEY",
		Status:        1,
	}, []string{"qwen-max", "qwen-plus"})
	if err != nil {
		t.Fatalf("CreateProviderWithPresetModels: %v", err)
	}

	listed, err := pg.ListProviderModels(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListProviderModels: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("preset list has %d rows, want 2: %+v", len(listed), listed)
	}
	for _, m := range listed {
		if m.ProviderID != created.ID || m.Source != model.ProviderModelSourcePreset || m.Status != 1 {
			t.Fatalf("preset row = %+v, want provider %s source preset status 1", m, created.ID)
		}
	}
	if !hasProviderModel(listed, "qwen-max") || !hasProviderModel(listed, "qwen-plus") {
		t.Fatalf("preset names did not round-trip: %+v", listed)
	}
}

func TestProviderModelMergesFetchedNamesWithoutTouchingManualRows(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)

	created := mustProvider(t, pg, "model-merge")

	if err := pg.SeedProviderModels(ctx, created.ID, []string{"qwen-max"}, model.ProviderModelSourcePreset); err != nil {
		t.Fatalf("SeedProviderModels(preset): %v", err)
	}
	if _, _, err := pg.AddProviderModel(ctx, created.ID, "internal-model"); err != nil {
		t.Fatalf("AddProviderModel: %v", err)
	}

	added, err := pg.MergeFetchedProviderModels(ctx, created.ID, []string{"qwen-max", "qwen-plus", "internal-model", "qwen-plus"})
	if err != nil {
		t.Fatalf("MergeFetchedProviderModels: %v", err)
	}
	if added != 1 {
		t.Fatalf("merge added %d rows, want 1 (qwen-plus)", added)
	}

	listed, err := pg.ListProviderModels(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListProviderModels: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("list has %d rows, want 3: %+v", len(listed), listed)
	}
	for _, want := range []struct{ name, source string }{
		{"qwen-max", model.ProviderModelSourcePreset},
		{"qwen-plus", model.ProviderModelSourceFetched},
		{"internal-model", model.ProviderModelSourceManual},
	} {
		got, ok := providerModelByName(listed, want.name)
		if !ok {
			t.Fatalf("%s is missing from the list: %+v", want.name, listed)
		}
		if got.Source != want.source {
			t.Fatalf("%s source = %q, want %q", want.name, got.Source, want.source)
		}
	}
}

// Removing a Provider removes its list. An orphaned row would keep saying a
// channel that no longer exists offers a model.
func TestDeletingAProviderRemovesItsModelList(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)

	created := mustProvider(t, pg, "model-cascade")
	if err := pg.SeedProviderModels(ctx, created.ID, []string{"qwen-max"}, model.ProviderModelSourcePreset); err != nil {
		t.Fatalf("SeedProviderModels: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx, `DELETE FROM gateway_provider WHERE id=$1::uuid`, created.ID); err != nil {
		t.Fatalf("delete provider: %v", err)
	}
	var count int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_provider_model WHERE provider_id=$1::uuid`, created.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%d orphaned provider model rows survived the provider delete", count)
	}
}

func hasProviderModel(list []model.ProviderModel, name string) bool {
	_, ok := providerModelByName(list, name)
	return ok
}

func providerModelByName(list []model.ProviderModel, name string) (model.ProviderModel, bool) {
	for _, m := range list {
		if m.UpstreamModel == name {
			return m, true
		}
	}
	return model.ProviderModel{}, false
}
