package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

func TestModelAuthorizationRBACIsSuperAdminOnly(t *testing.T) {
	path := "/admin/models/11111111-1111-1111-1111-111111111111/authorizations"
	for _, role := range []string{RoleGatewayAdmin, RoleAuditor, RoleOperator, RoleMember} {
		p := adminPrincipal{User: "u", Roles: map[string]bool{role: true}}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if p.can(req) {
			t.Fatalf("%s may reach model authorization", role)
		}
	}
	p := adminPrincipal{User: "u", Roles: map[string]bool{RoleSuperAdmin: true}}
	if !p.can(httptest.NewRequest(http.MethodGet, path, nil)) {
		t.Fatal("super_admin may not reach model authorization")
	}
}

func TestModelAuthorizationEndpointMergesAndRefusesWildcardRemoval(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	ctx := context.Background()
	tenant, err := pg.CreateTenant(ctx, model.Tenant{Code: "auth-http-tenant", Name: "Auth HTTP", Status: 1})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	explicit, err := pg.CreateAPIKey(ctx, model.APIKeyAdmin{
		TenantID: tenant.ID, Name: "explicit", Status: 1, AllowedModels: []string{},
	}, "auth-http-digest-1")
	if err != nil {
		t.Fatalf("CreateAPIKey explicit: %v", err)
	}
	wildcard, err := pg.CreateAPIKey(ctx, model.APIKeyAdmin{
		TenantID: tenant.ID, Name: "wildcard", Status: 1, AllowedModels: []string{model.AllModelsWildcard},
	}, "auth-http-digest-2")
	if err != nil {
		t.Fatalf("CreateAPIKey wildcard: %v", err)
	}
	provider, err := pg.CreateProvider(ctx, model.Provider{
		Name: "auth-http-provider", BaseURL: "https://auth.example.test/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	created, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "auth-http-alias", Status: 1, ProviderID: provider.ID, UpstreamModel: "auth-http-up",
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	s := providerModelTestServer(t, pg)

	do := func(role, method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/admin/models/"+created.ID+"/authorizations", strings.NewReader(body))
		req = req.WithContext(withAdminPrincipal(req.Context(), adminPrincipal{
			User: "auth-operator", Roles: map[string]bool{role: true},
		}))
		rec := httptest.NewRecorder()
		s.handleModelAuthorizations(rec, req)
		return rec
	}

	if rec := do(RoleGatewayAdmin, http.MethodGet, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("gateway_admin read = %d %s", rec.Code, rec.Body.String())
	}
	rec := do(RoleSuperAdmin, http.MethodPut, `{"api_key_ids":["`+explicit.ID+`","`+wildcard.ID+`"],"allow":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("allow = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Results []model.AuthorizationResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("allow response: %v", err)
	}
	outcomes := map[string]string{}
	for _, r := range out.Results {
		outcomes[r.APIKeyID] = r.Outcome
	}
	if outcomes[explicit.ID] != "added" || outcomes[wildcard.ID] != "already_allowed" {
		t.Fatalf("allow outcomes = %+v", outcomes)
	}

	rec = do(RoleSuperAdmin, http.MethodPut, `{"api_key_ids":["`+wildcard.ID+`"],"allow":false}`)
	var removed struct {
		Results []model.AuthorizationResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &removed); err != nil {
		t.Fatalf("remove response: %v", err)
	}
	if len(removed.Results) != 1 || removed.Results[0].Outcome != "wildcard_refused" {
		t.Fatalf("wildcard removal = %+v", removed.Results)
	}
}
