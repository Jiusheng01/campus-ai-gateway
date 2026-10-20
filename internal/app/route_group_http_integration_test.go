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

func routeGroupFixture(t *testing.T) (*Server, model.ModelConfig, []model.Provider, []model.ModelRoute) {
	t.Helper()
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	ctx := context.Background()
	p1, err := pg.CreateProvider(ctx, model.Provider{
		Name: "group-p1", BaseURL: "https://p1.example.test/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider p1: %v", err)
	}
	p2, err := pg.CreateProvider(ctx, model.Provider{
		Name: "group-p2", BaseURL: "https://p2.example.test/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider p2: %v", err)
	}
	created, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "group-model", Status: 1, ProviderID: p1.ID, UpstreamModel: "up-1",
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if _, err := pg.CreateModelRoute(ctx, created.ID, model.ModelRoute{
		ProviderID: p2.ID, UpstreamModel: "up-2", Priority: 2, Weight: 100, TimeoutMS: 60000, Status: 1,
	}); err != nil {
		t.Fatalf("CreateModelRoute: %v", err)
	}
	routes, err := pg.ListModelRoutes(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListModelRoutes: %v", err)
	}
	return providerModelTestServer(t, pg), created, []model.Provider{p1, p2}, routes
}

func routeGroupRequest(t *testing.T, s *Server, modelID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/admin/models/"+modelID+"/routes", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleModelRoutes(rec, req)
	return rec
}

func TestGroupSavePreservesIdsAndRejectsStaleVersion(t *testing.T) {
	s, created, providers, routes := routeGroupFixture(t)
	d, _ := s.pg.ModelByID(context.Background(), created.ID)
	body := map[string]any{
		"expected_version": d.ConfigVersion,
		"routes": []map[string]any{
			{"id": routes[1].ID, "provider_id": providers[1].ID, "upstream_model": "up-2", "priority": 1, "weight": 100, "timeout_ms": 30000, "status": 1},
			{"id": routes[0].ID, "provider_id": providers[0].ID, "upstream_model": "up-1", "priority": 2, "weight": 100, "timeout_ms": 60000, "status": 1},
		},
	}
	payload, _ := json.Marshal(body)
	rec := routeGroupRequest(t, s, created.ID, string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("group save = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Version int64              `json:"version"`
		Routes  []model.ModelRoute `json:"routes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response: %v", err)
	}
	if out.Version != d.ConfigVersion+1 || len(out.Routes) != 2 {
		t.Fatalf("group save response = %+v", out)
	}
	ids := map[string]bool{}
	for _, r := range out.Routes {
		ids[r.ID] = true
	}
	if !ids[routes[0].ID] || !ids[routes[1].ID] {
		t.Fatalf("route ids not preserved: %+v", out.Routes)
	}

	rec = routeGroupRequest(t, s, created.ID, string(payload))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "config_version_conflict") {
		t.Fatalf("stale save = %d %s", rec.Code, rec.Body.String())
	}
}

func TestGroupSaveRejectsChangedEnabledZeroWeightAndUnknownFields(t *testing.T) {
	s, created, providers, routes := routeGroupFixture(t)
	d, _ := s.pg.ModelByID(context.Background(), created.ID)
	zero := map[string]any{
		"expected_version": d.ConfigVersion,
		"routes": []map[string]any{
			{"id": routes[0].ID, "provider_id": providers[0].ID, "upstream_model": "up-1", "priority": 1, "weight": 0, "timeout_ms": 60000, "status": 1},
		},
	}
	payload, _ := json.Marshal(zero)
	rec := routeGroupRequest(t, s, created.ID, string(payload))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("changed zero weight = %d %s", rec.Code, rec.Body.String())
	}

	rec = routeGroupRequest(t, s, created.ID, `{"expected_version":1,"routes":[],"unexpected":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d %s", rec.Code, rec.Body.String())
	}
}
