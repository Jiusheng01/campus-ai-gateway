package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

func publishCandidate(t *testing.T, pg *Postgres, providerID, upstream string) string {
	t.Helper()
	if err := pg.SeedProviderModels(context.Background(), providerID, []string{upstream}, model.ProviderModelSourceManual); err != nil {
		t.Fatalf("SeedProviderModels: %v", err)
	}
	list, err := pg.ListProviderModels(context.Background(), providerID)
	if err != nil {
		t.Fatalf("ListProviderModels: %v", err)
	}
	c, ok := providerModelByName(list, upstream)
	if !ok {
		t.Fatalf("candidate %q missing", upstream)
	}
	return c.ID
}

func TestPublishModelIsAtomicIdempotentAndExplicitOnAppend(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)
	primary := mustProvider(t, pg, "publish-primary")
	backup := mustProvider(t, pg, "publish-backup")
	primaryModel := publishCandidate(t, pg, primary.ID, "up-primary")
	backupModel := publishCandidate(t, pg, backup.ID, "up-backup")

	created, err := pg.PublishProviderModel(ctx, primaryModel, "stable-alias", "Stable Alias", "desc", false)
	if err != nil {
		t.Fatalf("publish new: %v", err)
	}
	if !created.Created || !created.RouteCreated || created.Version != 1 {
		t.Fatalf("new publish = %+v", created)
	}

	repeat, err := pg.PublishProviderModel(ctx, primaryModel, "stable-alias", "", "", false)
	if err != nil {
		t.Fatalf("publish repeat: %v", err)
	}
	if repeat.Created || repeat.RouteCreated || repeat.Route.ID != created.Route.ID {
		t.Fatalf("repeat = %+v", repeat)
	}

	if _, err := pg.PublishProviderModel(ctx, backupModel, "stable-alias", "", "", false); !errors.Is(err, ErrAliasExists) {
		t.Fatalf("different binding without append = %v, want ErrAliasExists", err)
	}
	routes, err := pg.ListModelRoutes(ctx, created.Model.ID)
	if err != nil {
		t.Fatalf("ListModelRoutes: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("a refused append wrote a route: %+v", routes)
	}

	appended, err := pg.PublishProviderModel(ctx, backupModel, "stable-alias", "", "", true)
	if err != nil {
		t.Fatalf("publish append: %v", err)
	}
	if appended.Created || !appended.RouteCreated || appended.Route.Priority != 2 || appended.Version != 2 {
		t.Fatalf("append = %+v", appended)
	}
}

func TestPublishAndProviderDeleteDoNotDeadlockOrLeaveDanglingRoutes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)
	provider := mustProvider(t, pg, "publish-race")
	candidate := publishCandidate(t, pg, provider.ID, "race-up")

	var wg sync.WaitGroup
	var publishErr, deleteErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, publishErr = pg.PublishProviderModel(ctx, candidate, "race-alias", "", "", false)
	}()
	go func() {
		defer wg.Done()
		_, deleteErr = pg.DeleteProvider(ctx, provider.ID)
	}()
	wg.Wait()
	if ctx.Err() != nil {
		t.Fatal("publish and provider delete did not finish within 10s")
	}
	if deleteErr != nil {
		t.Fatalf("provider delete: %v", deleteErr)
	}
	if publishErr != nil && !errors.Is(publishErr, pgx.ErrNoRows) {
		t.Fatalf("publish: %v", publishErr)
	}
	var dangling, models int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM gateway_model_route WHERE provider_id=$1::uuid`, provider.ID).Scan(&dangling); err != nil {
		t.Fatalf("count dangling routes: %v", err)
	}
	if err := pg.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM gateway_model WHERE alias='race-alias'`).Scan(&models); err != nil {
		t.Fatalf("count model: %v", err)
	}
	if dangling != 0 || models != 0 {
		t.Fatalf("dangling routes=%d model=%d", dangling, models)
	}
}

func TestPublishModelRollsBackWhenTheRouteWriteFails(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)
	primary := mustProvider(t, pg, "publish-rollback")
	candidate := publishCandidate(t, pg, primary.ID, "rollback-up")

	if _, err := pg.Pool.Exec(ctx, `
CREATE OR REPLACE FUNCTION ms1_fail_route() RETURNS trigger AS $$
BEGIN
  IF NEW.upstream_model = 'rollback-up' THEN
    RAISE EXCEPTION 'injected route failure';
  END IF;
  RETURN NEW;
END; $$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS ms1_fail_route_trigger ON gateway_model_route`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx, `
CREATE TRIGGER ms1_fail_route_trigger BEFORE INSERT ON gateway_model_route
FOR EACH ROW EXECUTE FUNCTION ms1_fail_route()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pg.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS ms1_fail_route_trigger ON gateway_model_route`)
		_, _ = pg.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS ms1_fail_route()`)
	})

	if _, err := pg.PublishProviderModel(ctx, candidate, "atomic-alias", "", "", false); err == nil {
		t.Fatal("publish succeeded despite the injected route failure")
	}
	var models int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_model WHERE alias='atomic-alias'`).Scan(&models); err != nil {
		t.Fatalf("count models: %v", err)
	}
	if models != 0 {
		t.Fatalf("a failed publish left %d model row(s)", models)
	}
}
