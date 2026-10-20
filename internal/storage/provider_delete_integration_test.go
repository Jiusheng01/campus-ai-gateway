package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// Deleting a Provider is a cascade, not a row delete. These tests pin the two
// facts an operator would otherwise discover in production: a Virtual Model
// that only this Provider served disappears with it, while a model that still
// has another Route survives; and the Credential goes only when no other
// Provider still needs it.

func TestDeleteProviderCascadesToRoutesOrphanModelsAndProviderModels(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)

	doomed, err := pg.CreateProviderWithPresetModels(ctx, model.Provider{
		Name:          "doomed-channel",
		BaseURL:       "https://doomed.example.test",
		Protocol:      "openai-chat",
		CredentialEnv: "TEST_DOOMED_KEY",
		Status:        1,
	}, []string{"up-shared", "up-only-doomed"})
	if err != nil {
		t.Fatalf("CreateProviderWithPresetModels: %v", err)
	}
	keeper := mustProvider(t, pg, "keeper-channel")

	// Served by both channels: deleting doomed must leave this model and its
	// keeper Route alone.
	shared, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "shared-model", ProviderID: doomed.ID, UpstreamModel: "up-shared", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateModel shared: %v", err)
	}
	if _, err := pg.CreateModelRoute(ctx, shared.ID, model.ModelRoute{
		ProviderID: keeper.ID, UpstreamModel: "up-shared-keeper", Priority: 2, Weight: 100, TimeoutMS: 60000, Status: 1,
	}); err != nil {
		t.Fatalf("CreateModelRoute keeper: %v", err)
	}

	// Served only by doomed: it has no Route left and must disappear rather than
	// linger as a model that can never answer.
	orphan, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "orphan-model", ProviderID: doomed.ID, UpstreamModel: "up-only-doomed", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateModel orphan: %v", err)
	}

	got, err := pg.DeleteProvider(ctx, doomed.ID)
	if err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if got.DeletedRoutes != 2 || got.DeletedVirtualModels != 1 || got.DeletedProviderModels != 2 {
		t.Fatalf("delete counts = %+v, want 2 routes / 1 model / 2 provider models", got)
	}

	var providers int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_provider WHERE id=$1::uuid`, doomed.ID).Scan(&providers); err != nil {
		t.Fatal(err)
	}
	if providers != 0 {
		t.Fatal("the provider row survived")
	}

	var providerModels int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_provider_model WHERE provider_id=$1::uuid`, doomed.ID).Scan(&providerModels); err != nil {
		t.Fatal(err)
	}
	if providerModels != 0 {
		t.Fatalf("%d provider model rows survived", providerModels)
	}

	var credentials int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_credential WHERE id=$1::uuid`, doomed.CredentialID).Scan(&credentials); err != nil {
		t.Fatal(err)
	}
	if credentials != 0 {
		t.Fatal("an unshared credential survived the provider delete")
	}

	models, err := pg.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	byID := map[string]model.ModelConfig{}
	for _, m := range models {
		byID[m.ID] = m
	}
	if _, ok := byID[orphan.ID]; ok {
		t.Fatal("the model left with zero routes survived")
	}
	if _, ok := byID[shared.ID]; !ok {
		t.Fatal("a model that still has another route was deleted")
	}

	routes, err := pg.ListModelRoutes(ctx, shared.ID)
	if err != nil {
		t.Fatalf("ListModelRoutes: %v", err)
	}
	if len(routes) != 1 || routes[0].ProviderID != keeper.ID {
		t.Fatalf("shared model routes = %+v, want only the keeper route", routes)
	}
}

func TestDeleteProviderKeepsACredentialAnotherProviderStillUses(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)

	owner := mustProvider(t, pg, "credential-owner")
	user := mustProvider(t, pg, "credential-user")
	if _, err := pg.Pool.Exec(ctx, `
UPDATE gateway_provider SET credential_id=$1::uuid WHERE id=$2::uuid
`, owner.CredentialID, user.ID); err != nil {
		t.Fatalf("share the credential: %v", err)
	}

	if _, err := pg.DeleteProvider(ctx, owner.ID); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}

	var credentials int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_credential WHERE id=$1::uuid`, owner.CredentialID).Scan(&credentials); err != nil {
		t.Fatal(err)
	}
	if credentials != 1 {
		t.Fatal("a credential another provider still references was deleted")
	}

	providers, err := pg.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(providers) != 1 || providers[0].ID != user.ID {
		t.Fatalf("providers after delete = %+v, want only %s", providers, user.ID)
	}
}

// The models to remove must come from the Routes the DELETE actually removed,
// not from the list read before it. The test stages exactly the gap between
// those two points: another transaction holds a Route onto the doomed Provider
// uncommitted, so the initial read cannot see it, and DeleteProvider waits on
// the Provider row until that transaction commits. With a precomputed list the
// Route is deleted but the model it leaves with zero Routes survives; with
// RETURNING the model is removed.
func TestDeleteProviderDerivesAffectedModelsFromDeletedRoutes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)

	doomed := mustProvider(t, pg, "race-doomed")
	keeper := mustProvider(t, pg, "race-keeper")
	modelConfig, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "race-model", ProviderID: keeper.ID, UpstreamModel: "up-race-keeper", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	// Hold an uncommitted move of the model's only Route onto the doomed
	// Provider. The FK check takes a KEY SHARE on that Provider, which is what
	// DeleteProvider's FOR UPDATE will wait for.
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM gateway_model_route WHERE model_id=$1::uuid`, modelConfig.ID); err != nil {
		t.Fatalf("remove keeper route: %v", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO gateway_model_route(model_id, provider_id, upstream_model, priority, weight, timeout_ms, status)
VALUES ($1::uuid, $2::uuid, 'up-race-doomed', 1, 100, 60000, 1)`, modelConfig.ID, doomed.ID); err != nil {
		t.Fatalf("insert doomed route: %v", err)
	}

	type outcome struct {
		result model.ProviderDeleteResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := pg.DeleteProvider(context.Background(), doomed.ID)
		done <- outcome{result: result, err: err}
	}()

	// Wait until DeleteProvider is blocked on the Provider row, which is after
	// its initial Route read and before its Route DELETE. Then let it continue.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pg.Pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM pg_stat_activity
WHERE datname = current_database()
  AND wait_event_type='Lock'
  AND query LIKE '%SELECT credential_id::text%FROM gateway_provider%FOR UPDATE%'
  AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatalf("inspect waits: %v", err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("DeleteProvider never blocked on the provider row")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit route move: %v", err)
	}

	got := <-done
	if got.err != nil {
		t.Fatalf("DeleteProvider: %v", got.err)
	}
	if got.result.DeletedRoutes != 1 || got.result.DeletedVirtualModels != 1 {
		t.Fatalf("delete counts = %+v, want 1 route / 1 model", got.result)
	}

	var models int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_model WHERE id=$1::uuid`, modelConfig.ID).Scan(&models); err != nil {
		t.Fatal(err)
	}
	if models != 0 {
		t.Fatal("the model the committed Route left with zero Routes survived the delete")
	}
}

func TestDeleteProviderReturnsNoRowsWhenMissing(t *testing.T) {
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)

	_, err := pg.DeleteProvider(context.Background(), "11111111-1111-1111-1111-111111111111")
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("DeleteProvider of a missing provider = %v, want pgx.ErrNoRows", err)
	}
}
