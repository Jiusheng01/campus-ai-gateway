package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

// DELETE /admin/providers/{id} is the destructive management operation, so the
// tests are about what an operator observes: the status, the counts that say
// what went away, and the state left in the database. The storage tests pin the
// cascade itself; these pin the HTTP contract around it and the RBAC/audit
// behaviour the prefix already provides.

func clearProviderDeleteFixtures(t *testing.T, pg *storage.Postgres) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM gateway_model_route`,
		`DELETE FROM gateway_model`,
		`DELETE FROM gateway_provider_model`,
		`DELETE FROM gateway_provider`,
	} {
		if _, err := pg.Pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func seedProviderDeleteFixture(t *testing.T, pg *storage.Postgres) (doomed, keeper model.Provider, shared, orphan model.ModelConfig) {
	t.Helper()
	ctx := context.Background()

	doomedProvider, err := pg.CreateProviderWithPresetModels(ctx, model.Provider{
		Name:          "delete-doomed",
		BaseURL:       "https://doomed.example.test",
		Protocol:      "openai-chat",
		CredentialEnv: "TEST_DELETE_DOOMED_KEY",
		Status:        1,
	}, []string{"up-shared"})
	if err != nil {
		t.Fatalf("create doomed provider: %v", err)
	}
	keeperProvider, err := pg.CreateProvider(ctx, model.Provider{
		Name:          "delete-keeper",
		BaseURL:       "https://keeper.example.test",
		Protocol:      "openai-chat",
		CredentialEnv: "TEST_DELETE_KEEPER_KEY",
		Status:        1,
	})
	if err != nil {
		t.Fatalf("create keeper provider: %v", err)
	}

	shared, err = pg.CreateModel(ctx, model.ModelConfig{
		Alias: "delete-shared-model", ProviderID: doomedProvider.ID, UpstreamModel: "up-shared", Status: 1,
	})
	if err != nil {
		t.Fatalf("create shared model: %v", err)
	}
	if _, err := pg.CreateModelRoute(ctx, shared.ID, model.ModelRoute{
		ProviderID: keeperProvider.ID, UpstreamModel: "up-shared-keeper", Priority: 2, Weight: 100, TimeoutMS: 60000, Status: 1,
	}); err != nil {
		t.Fatalf("create keeper route: %v", err)
	}
	orphan, err = pg.CreateModel(ctx, model.ModelConfig{
		Alias: "delete-orphan-model", ProviderID: doomedProvider.ID, UpstreamModel: "up-only-doomed", Status: 1,
	})
	if err != nil {
		t.Fatalf("create orphan model: %v", err)
	}

	return doomedProvider, keeperProvider, shared, orphan
}

func TestDeleteProviderEndpointCascadesAndReportsCounts(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderDeleteFixtures(t, pg)
	doomed, _, shared, orphan := seedProviderDeleteFixture(t, pg)

	s := &Server{pg: pg}
	rec := httptest.NewRecorder()
	s.handleProviderByID(rec, httptest.NewRequest(http.MethodDelete, "/admin/providers/"+doomed.ID, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body %s", rec.Code, rec.Body.String())
	}
	var got model.ProviderDeleteResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode delete result: %v", err)
	}
	if got.DeletedRoutes != 2 || got.DeletedModels != 1 || got.DeletedProviderModels != 1 {
		t.Fatalf("counts = %+v, want 2 routes / 1 model / 1 provider model", got)
	}

	ctx := context.Background()
	var providers int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_provider WHERE id=$1::uuid`, doomed.ID).Scan(&providers); err != nil {
		t.Fatal(err)
	}
	if providers != 0 {
		t.Fatal("the deleted provider still exists")
	}

	var orphanModels, sharedRoutes int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_model WHERE id=$1::uuid`, orphan.ID).Scan(&orphanModels); err != nil {
		t.Fatal(err)
	}
	if orphanModels != 0 {
		t.Fatal("a model left with zero routes survived")
	}
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_model_route WHERE model_id=$1::uuid`, shared.ID).Scan(&sharedRoutes); err != nil {
		t.Fatal(err)
	}
	if sharedRoutes != 1 {
		t.Fatalf("the model with another route has %d routes, want 1", sharedRoutes)
	}
}

func TestDeleteProviderEndpointReturns404WhenMissing(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderDeleteFixtures(t, pg)
	s := &Server{pg: pg}

	for _, id := range []string{
		"11111111-1111-1111-1111-111111111111",
		"not-a-uuid",
	} {
		rec := httptest.NewRecorder()
		s.handleProviderByID(rec, httptest.NewRequest(http.MethodDelete, "/admin/providers/"+id, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("DELETE %s status = %d, want 404; body %s", id, rec.Code, rec.Body.String())
		}
	}
}

func TestDeleteProviderEndpointRejectsOtherMethods(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleProviderByID(rec, httptest.NewRequest(http.MethodPost, "/admin/providers/11111111-1111-1111-1111-111111111111", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "PUT, DELETE" {
		t.Fatalf("Allow = %q, want \"PUT, DELETE\"", allow)
	}
}

// The role boundary is the one the rest of /admin/providers already has: the
// channel administrators and super administrators may delete, operators and
// auditors may not. Nothing in the handler adds a second rule.
func TestDeleteProviderRBAC(t *testing.T) {
	s, _ := newTestServer(t, trustedHeaderConfig())
	const path = "/admin/providers/11111111-1111-1111-1111-111111111111"

	for _, tc := range []struct {
		roles string
		want  int
	}{
		{RoleGatewayAdmin, http.StatusOK},
		{RoleSuperAdmin, http.StatusOK},
		{RoleOperator, http.StatusForbidden},
		{RoleAuditor, http.StatusForbidden},
		{RoleMember, http.StatusForbidden},
	} {
		if rec := trustedHeaderCall(t, s, tc.roles, http.MethodDelete, path); rec.Code != tc.want {
			t.Fatalf("DELETE as %q = %d, want %d", tc.roles, rec.Code, tc.want)
		}
	}
}

// A destructive request is audited by the middleware, without the handler doing
// anything for it: the row names the operator, the method and the path.
func TestDeleteProviderIsAudited(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderDeleteFixtures(t, pg)
	doomed, _, _, _ := seedProviderDeleteFixture(t, pg)

	s := &Server{pg: pg, cfg: Config{AdminAuthMode: "token", AdminToken: "test-admin-token"}}
	req := httptest.NewRequest(http.MethodDelete, "/admin/providers/"+doomed.ID, nil)
	req.Header.Set("X-Request-Id", "delete-provider-audit")
	req.Header.Set("Authorization", "Bearer test-admin-token")
	rec := httptest.NewRecorder()
	s.adminMiddleware(http.HandlerFunc(s.handleProviderByID)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body %s", rec.Code, rec.Body.String())
	}

	rows, err := pg.ListAdminAudit(context.Background(), 20)
	if err != nil {
		t.Fatalf("ListAdminAudit: %v", err)
	}
	for _, row := range rows {
		if row.RequestID != "delete-provider-audit" {
			continue
		}
		if row.Action != http.MethodDelete || row.Resource != "/admin/providers/"+doomed.ID || row.StatusCode != http.StatusOK {
			t.Fatalf("audit row = %+v, want DELETE %s -> 200", row, req.URL.Path)
		}
		if row.Actor != "admin_token" {
			t.Fatalf("audit actor = %q, want admin_token", row.Actor)
		}
		return
	}
	t.Fatal("the DELETE was not audited")
}
