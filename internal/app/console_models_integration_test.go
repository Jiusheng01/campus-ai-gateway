package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

// The catalog's contract is the JSON that leaves GET /api/console/models, so
// these tests run the real middleware, account lookup, tenant read and routing
// table against a migrated database. They skip when no test PostgreSQL answers,
// like the other app integration tests.
//
// The cases are the four shapes the page must tell apart: a wildcard Tenant, an
// explicit Tenant, an empty allowlist, and no Tenant at all, plus the fifth
// state an empty list can have — a Tenant that allows models none of which is
// currently routable.

func consoleModelsServer(t *testing.T, pg *storage.Postgres) *Server {
	t.Helper()
	cfg := testConfig()
	s := &Server{cfg: cfg, pg: pg, mux: http.NewServeMux()}
	s.adminAuth = newAdminAuthService(pg, newFakeLimiter(8), []byte("console-models-test-secret"), cfg)
	s.routes()
	return s
}

func consoleModelsClear(t *testing.T, pg *storage.Postgres) {
	t.Helper()
	tenantAssignmentClear(t, pg)
	clearProviderFixtures(t, pg)
}

// consoleModelsSeed creates one model through the management layer, which also
// writes its first Route. The status parameters let a case make exactly one part
// of the chain unusable, so "statically routable" is proven rather than assumed.
func consoleModelsSeed(t *testing.T, pg *storage.Postgres, alias, display, description string, modelStatus, providerStatus, routeStatus int) {
	t.Helper()
	ctx := context.Background()
	provider, err := pg.CreateProvider(ctx, model.Provider{
		Name:          "provider-" + alias,
		BaseURL:       "https://" + alias + ".example.test/v1",
		Protocol:      "openai-chat",
		CredentialEnv: "CONSOLE_MODELS_TEST_KEY",
		Status:        providerStatus,
	})
	if err != nil {
		t.Fatalf("CreateProvider %s: %v", alias, err)
	}
	created, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias:         alias,
		DisplayName:   display,
		Description:   description,
		Status:        modelStatus,
		ProviderID:    provider.ID,
		UpstreamModel: "upstream-" + alias,
	})
	if err != nil {
		t.Fatalf("CreateModel %s: %v", alias, err)
	}
	if routeStatus != 1 {
		if _, err := pg.Pool.Exec(ctx,
			`UPDATE gateway_model_route SET status=$1 WHERE model_id=$2::uuid`,
			routeStatus, created.ID); err != nil {
			t.Fatalf("set route status for %s: %v", alias, err)
		}
	}
}

func consoleModelsTenant(t *testing.T, pg *storage.Postgres, code string, allowed []string) model.Tenant {
	t.Helper()
	tenant, err := pg.CreateTenant(context.Background(), model.Tenant{
		Code:          code,
		Name:          code,
		Status:        1,
		AllowedModels: allowed,
	})
	if err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return tenant
}

// consoleModelsMember creates an account, optionally assigns it to a Tenant,
// and signs it in. The token is the access token the console sends.
func consoleModelsMember(t *testing.T, s *Server, pg *storage.Postgres, tenantID string) string {
	t.Helper()
	username := uniqueAccountName("models-member")
	account := tenantAssignmentAccount(t, pg, username, []string{RoleMember})
	if tenantID != "" {
		if _, err := pg.AssignAdminUserTenant(context.Background(), account.ID, tenantID); err != nil {
			t.Fatalf("assign tenant: %v", err)
		}
	}
	return tenantAssignmentToken(t, s, username)
}

func consoleModelsCall(t *testing.T, s *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/api/console/models", nil)
	req.Header.Set("X-Request-Id", "console-models-test")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

type consoleModelsBody struct {
	Models []struct {
		Alias       string `json:"alias"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Available   bool   `json:"available"`
	} `json:"models"`
	EmptyReason string `json:"empty_reason"`
}

func decodeConsoleModels(t *testing.T, rec *httptest.ResponseRecorder) consoleModelsBody {
	t.Helper()
	var out consoleModelsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode /api/console/models: %v (%s)", err, rec.Body.String())
	}
	return out
}

func consoleModelAliases(body consoleModelsBody) []string {
	out := make([]string, 0, len(body.Models))
	for _, m := range body.Models {
		out = append(out, m.Alias)
	}
	return out
}

// A wildcard Tenant sees every eligible model and nothing else: a disabled
// model, a disabled Provider and a disabled Route each remove their alias from
// the catalog. The response carries only the fields the page renders.
func TestConsoleModelsWildcardTenantSeesOnlyStaticallyRoutableModels(t *testing.T) {
	pg := usagePostgres(t)
	consoleModelsClear(t, pg)
	s := consoleModelsServer(t, pg)

	consoleModelsSeed(t, pg, "alpha-ready", "Alpha Ready", "可用的模型", 1, 1, 1)
	consoleModelsSeed(t, pg, "beta-disabled-model", "Beta", "模型已停用", 2, 1, 1)
	consoleModelsSeed(t, pg, "gamma-disabled-provider", "Gamma", "渠道已停用", 1, 2, 1)
	consoleModelsSeed(t, pg, "delta-disabled-route", "Delta", "线路已停用", 1, 1, 2)

	tenant := consoleModelsTenant(t, pg, "models-wildcard", []string{model.AllModelsWildcard})
	token := consoleModelsMember(t, s, pg, tenant.ID)

	rec := consoleModelsCall(t, s, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/models = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeConsoleModels(t, rec)
	got := consoleModelAliases(body)
	if len(got) != 1 || got[0] != "alpha-ready" {
		t.Fatalf("models = %v, want [alpha-ready]", got)
	}
	if body.EmptyReason != "" {
		t.Fatalf("empty_reason = %q for a non-empty catalog", body.EmptyReason)
	}
	if body.Models[0].DisplayName != "Alpha Ready" || body.Models[0].Description != "可用的模型" {
		t.Fatalf("display fields = %+v", body.Models[0])
	}
	if !body.Models[0].Available {
		t.Fatal("a model in the catalog must report itself available")
	}

	// The member projection must not grow Provider, credential, Route or
	// upstream detail. Checking the decoded object's keys is the structural
	// half; the prose check catches a value smuggled into a display string.
	var envelope struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode model objects: %v", err)
	}
	for _, m := range envelope.Models {
		if len(m) != 4 {
			t.Fatalf("model object keys = %v, want alias, display_name, description, available", m)
		}
		for _, key := range []string{"alias", "display_name", "description", "available"} {
			if _, ok := m[key]; !ok {
				t.Fatalf("model object is missing %q: %v", key, m)
			}
		}
	}
	raw := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{
		"provider-alpha-ready", "alpha-ready.example.test",
		"upstream-alpha-ready", "credential", "route", "base_url",
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("the catalog leaked %q: %s", forbidden, rec.Body.String())
		}
	}
}

// An explicit Tenant sees exactly the eligible aliases it names. An alias the
// Tenant allows but that has no usable Route is not advertised, and a routable
// model it does not name stays hidden.
func TestConsoleModelsExplicitTenantSeesExactlyItsEligibleMembers(t *testing.T) {
	pg := usagePostgres(t)
	consoleModelsClear(t, pg)
	s := consoleModelsServer(t, pg)

	consoleModelsSeed(t, pg, "explicit-alpha", "Alpha", "", 1, 1, 1)
	consoleModelsSeed(t, pg, "explicit-beta", "Beta", "", 1, 1, 1)
	consoleModelsSeed(t, pg, "explicit-gamma", "Gamma", "", 1, 1, 1)
	consoleModelsSeed(t, pg, "explicit-disabled", "Disabled", "", 2, 1, 1)

	tenant := consoleModelsTenant(t, pg, "models-explicit",
		[]string{"explicit-beta", "explicit-alpha", "explicit-disabled", "not-a-model"})
	token := consoleModelsMember(t, s, pg, tenant.ID)

	rec := consoleModelsCall(t, s, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/models = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeConsoleModels(t, rec)
	got := consoleModelAliases(body)
	if len(got) != 2 || got[0] != "explicit-alpha" || got[1] != "explicit-beta" {
		t.Fatalf("models = %v, want [explicit-alpha explicit-beta]", got)
	}
	if body.EmptyReason != "" {
		t.Fatalf("empty_reason = %q for a non-empty catalog", body.EmptyReason)
	}
}

// `[]` is a decision to allow nothing. The page must be able to say that,
// rather than showing the same empty list as an unassigned account.
func TestConsoleModelsEmptyAllowlistReportsTenantAllowsNone(t *testing.T) {
	pg := usagePostgres(t)
	consoleModelsClear(t, pg)
	s := consoleModelsServer(t, pg)

	consoleModelsSeed(t, pg, "none-ready", "Ready", "", 1, 1, 1)
	tenant := consoleModelsTenant(t, pg, "models-none", []string{})
	token := consoleModelsMember(t, s, pg, tenant.ID)

	rec := consoleModelsCall(t, s, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/models = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeConsoleModels(t, rec)
	if len(body.Models) != 0 {
		t.Fatalf("models = %v, want none", consoleModelAliases(body))
	}
	if body.EmptyReason != consoleModelsTenantAllowsNone {
		t.Fatalf("empty_reason = %q, want %q", body.EmptyReason, consoleModelsTenantAllowsNone)
	}
}

// An Account with no Tenant Membership has no workspace to call from. The
// endpoint answers the state rather than an error, so the page can explain it.
func TestConsoleModelsWithoutTenantReportsNoTenant(t *testing.T) {
	pg := usagePostgres(t)
	consoleModelsClear(t, pg)
	s := consoleModelsServer(t, pg)

	consoleModelsSeed(t, pg, "orphan-ready", "Ready", "", 1, 1, 1)
	token := consoleModelsMember(t, s, pg, "")

	rec := consoleModelsCall(t, s, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/models = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeConsoleModels(t, rec)
	if len(body.Models) != 0 {
		t.Fatalf("models = %v, want none", consoleModelAliases(body))
	}
	if body.EmptyReason != consoleModelsNoTenant {
		t.Fatalf("empty_reason = %q, want %q", body.EmptyReason, consoleModelsNoTenant)
	}
}

// A wildcard Tenant with nothing routable is a third empty state: the Tenant
// allows everything, but the gateway currently has nothing it could serve.
func TestConsoleModelsReportsWhenNoEligibleModelExists(t *testing.T) {
	pg := usagePostgres(t)
	consoleModelsClear(t, pg)
	s := consoleModelsServer(t, pg)

	tenant := consoleModelsTenant(t, pg, "models-nothing-routable", []string{model.AllModelsWildcard})
	token := consoleModelsMember(t, s, pg, tenant.ID)

	rec := consoleModelsCall(t, s, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/models = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeConsoleModels(t, rec)
	if len(body.Models) != 0 {
		t.Fatalf("models = %v, want none", consoleModelAliases(body))
	}
	if body.EmptyReason != consoleModelsNoneEligible {
		t.Fatalf("empty_reason = %q, want %q", body.EmptyReason, consoleModelsNoneEligible)
	}
}
