package storage

import (
	"context"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

func authorizationFixture(t *testing.T) (*Postgres, string, []model.APIKeyAdmin) {
	t.Helper()
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)
	if _, err := pg.Pool.Exec(ctx, `DELETE FROM gateway_api_key`); err != nil {
		t.Fatalf("clear keys: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx, `DELETE FROM gateway_tenant`); err != nil {
		t.Fatalf("clear tenants: %v", err)
	}
	tenant, err := pg.CreateTenant(ctx, model.Tenant{Code: "auth-tenant", Name: "Auth Tenant", Status: 1})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	provider := mustProvider(t, pg, "auth-provider")
	created, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "auth-alias", Status: 1, ProviderID: provider.ID, UpstreamModel: "auth-up",
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	var keys []model.APIKeyAdmin
	for i, allowed := range [][]string{{}, {model.AllModelsWildcard}, {"other"}} {
		k, err := pg.CreateAPIKey(ctx, model.APIKeyAdmin{
			TenantID: tenant.ID, Name: "key-" + string(rune('a'+i)), Status: 1, AllowedModels: allowed,
		}, "digest-"+string(rune('a'+i)))
		if err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
		keys = append(keys, k)
	}
	return pg, created.ID, keys
}

func TestModelAuthorizationMergesAtomicallyAndProtectsWildcards(t *testing.T) {
	ctx := context.Background()
	pg, modelID, keys := authorizationFixture(t)

	alias, results, err := pg.SetModelAuthorization(ctx, modelID, []string{keys[0].ID, keys[1].ID, keys[2].ID}, true)
	if err != nil {
		t.Fatalf("allow: %v", err)
	}
	if alias != "auth-alias" || len(results) != 3 {
		t.Fatalf("allow results = %s %+v", alias, results)
	}
	outcome := map[string]string{}
	for _, r := range results {
		outcome[r.APIKeyID] = r.Outcome
	}
	if outcome[keys[0].ID] != "added" || outcome[keys[1].ID] != "already_allowed" || outcome[keys[2].ID] != "added" {
		t.Fatalf("allow outcomes = %+v", outcome)
	}

	_, results, err = pg.SetModelAuthorization(ctx, modelID, []string{keys[0].ID, keys[1].ID}, false)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	outcome = map[string]string{}
	for _, r := range results {
		outcome[r.APIKeyID] = r.Outcome
	}
	if outcome[keys[0].ID] != "removed" || outcome[keys[1].ID] != "wildcard_refused" {
		t.Fatalf("remove outcomes = %+v", outcome)
	}

	_, listed, err := pg.ListModelAuthorizations(ctx, modelID)
	if err != nil {
		t.Fatalf("ListModelAuthorizations: %v", err)
	}
	for _, k := range listed {
		if k.APIKeyID == keys[1].ID && (!k.Wildcard || !k.Allowed) {
			t.Fatalf("wildcard key = %+v", k)
		}
		if k.APIKeyID == keys[0].ID && k.Allowed {
			t.Fatalf("removed key still allowed: %+v", k)
		}
	}
}
