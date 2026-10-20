package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// providerDiagnosticRequest drives the real handler with the principal the
// admin middleware would have attached. RBAC itself is covered separately.
func providerDiagnosticRequest(t *testing.T, s *Server, role, providerID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/providers/"+providerID+"/test", strings.NewReader(body))
	req = req.WithContext(withAdminPrincipal(req.Context(), adminPrincipal{
		User: "diagnostic-operator", Roles: map[string]bool{role: true},
	}))
	rec := httptest.NewRecorder()
	s.handleProviderTest(rec, req)
	return rec
}

func TestProviderDiagnosticDefaultOmitsContentAndPersistsMetadata(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	ctx := context.Background()

	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"up-x","choices":[{"message":{"content":"SHOULD_NOT_RETURN"}}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`))
	}))
	defer upstream.Close()
	t.Setenv("MS1_TEST_PROVIDER_KEY", "sk-test-value")

	provider, err := pg.CreateProvider(ctx, model.Provider{
		Name: "diag-default", BaseURL: upstream.URL + "/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	var before int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_request_log`).Scan(&before); err != nil {
		t.Fatalf("count request logs: %v", err)
	}

	s := providerModelTestServer(t, pg)
	s.providerTests = newProviderTestLimiter()
	rec := providerDiagnosticRequest(t, s, RoleGatewayAdmin, provider.ID,
		`{"upstream_model":"up-x","prompt":"reply with MODEL_OK"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "SHOULD_NOT_RETURN") || strings.Contains(rec.Body.String(), `"content"`) {
		t.Fatalf("default diagnostic must not return content: %s", rec.Body.String())
	}
	var out providerTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if out.TotalTokens != 7 || out.StatusCode != http.StatusOK {
		t.Fatalf("response = %+v", out)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls)
	}

	stored, err := pg.ProviderByID(ctx, provider.ID)
	if err != nil {
		t.Fatalf("ProviderByID: %v", err)
	}
	if stored.LastTest == nil || !stored.LastTest.OK || stored.LastTest.UpstreamModel != "up-x" || stored.LastTest.StatusCode != 200 {
		t.Fatalf("last test = %+v", stored.LastTest)
	}
	var after int
	if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_request_log`).Scan(&after); err != nil {
		t.Fatalf("count request logs: %v", err)
	}
	if after != before {
		t.Fatalf("diagnostic wrote %d request log row(s), want none", after-before)
	}
}

func TestProviderDiagnosticContentPreviewRequiresSuperAdmin(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	ctx := context.Background()

	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"up-x","choices":[{"message":{"content":"PREVIEW_ONLY"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer upstream.Close()
	t.Setenv("MS1_TEST_PROVIDER_KEY", "sk-test-value")

	provider, err := pg.CreateProvider(ctx, model.Provider{
		Name: "diag-content", BaseURL: upstream.URL + "/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	s := providerModelTestServer(t, pg)
	s.providerTests = newProviderTestLimiter()

	rec := providerDiagnosticRequest(t, s, RoleGatewayAdmin, provider.ID,
		`{"upstream_model":"up-x","prompt":"p","include_content":true}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "content_preview_forbidden") {
		t.Fatalf("gateway_admin content preview = %d %s", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("a refused content preview must not call the upstream")
	}

	rec = providerDiagnosticRequest(t, s, RoleSuperAdmin, provider.ID,
		`{"upstream_model":"up-x","prompt":"p","include_content":true}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "PREVIEW_ONLY") {
		t.Fatalf("super_admin content preview = %d %s", rec.Code, rec.Body.String())
	}
}

func TestProviderDiagnosticRejectsArbitraryTargetFields(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	ctx := context.Background()
	provider, err := pg.CreateProvider(ctx, model.Provider{
		Name: "diag-fields", BaseURL: "https://upstream.example.test/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	s := providerModelTestServer(t, pg)
	rec := providerDiagnosticRequest(t, s, RoleGatewayAdmin, provider.ID,
		`{"upstream_model":"up-x","prompt":"p","base_url":"http://127.0.0.1:1/v1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestProviderStatusPatchIsAudited(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	ctx := context.Background()
	provider, err := pg.CreateProvider(ctx, model.Provider{
		Name: "audit-patch", BaseURL: "https://audit.example.test/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	cfg := trustedHeaderConfig()
	s := &Server{cfg: cfg, pg: pg}
	path := "/admin/providers/" + provider.ID
	req := httptest.NewRequest(http.MethodPatch, "http://gateway.local"+path, strings.NewReader(`{"status":2}`))
	req.Header.Set("X-Request-Id", "audit-patch-req")
	req.Header.Set(cfg.AdminUserHeader, "zhang.san")
	req.Header.Set(cfg.AdminRolesHeader, RoleGatewayAdmin)
	req.Header.Set("X-Campus-Auth-Secret", cfg.AdminProxySecret)
	rec := httptest.NewRecorder()
	s.adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.updateProviderStatus(w, r, provider.ID, "audit-patch-req")
	})).ServeHTTP(rec, req)

	var action, resource string
	var status int
	if err := pg.Pool.QueryRow(ctx, `
SELECT action, resource, status_code FROM gateway_admin_audit_log
WHERE request_id=$1 ORDER BY id DESC LIMIT 1`, "audit-patch-req").Scan(&action, &resource, &status); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if action != http.MethodPatch || resource != path || status != http.StatusOK {
		t.Fatalf("audit = %s %s %d", action, resource, status)
	}
}

func TestProviderListFiltersAndStatusPatch(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	ctx := context.Background()
	alpha, err := pg.CreateProvider(ctx, model.Provider{
		Name: "filter-alpha", BaseURL: "https://alpha.example.test/v1", Protocol: "openai-chat",
		ProviderType: "alpha", CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider alpha: %v", err)
	}
	if _, err := pg.CreateProvider(ctx, model.Provider{
		Name: "filter-beta", BaseURL: "https://beta.example.test/v1", Protocol: "openai-chat",
		ProviderType: "beta", CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	}); err != nil {
		t.Fatalf("CreateProvider beta: %v", err)
	}
	s := providerModelTestServer(t, pg)

	req := httptest.NewRequest(http.MethodGet, "/admin/providers?provider_type=alpha&status=1", nil)
	rec := httptest.NewRecorder()
	s.handleProviders(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Data  []model.Provider `json:"data"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("list is not JSON: %v", err)
	}
	if list.Total != 1 || len(list.Data) != 1 || list.Data[0].ID != alpha.ID {
		t.Fatalf("filtered list = %+v", list)
	}

	req = httptest.NewRequest(http.MethodPatch, "/admin/providers/"+alpha.ID, strings.NewReader(`{"status":2}`))
	rec = httptest.NewRecorder()
	s.updateProviderStatus(rec, req, alpha.ID, "test-req")
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", rec.Code, rec.Body.String())
	}
	stored, err := pg.ProviderByID(ctx, alpha.ID)
	if err != nil {
		t.Fatalf("ProviderByID: %v", err)
	}
	if stored.Status != 2 {
		t.Fatalf("status = %d, want 2", stored.Status)
	}
}
