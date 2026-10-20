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

func publishTestCandidate(t *testing.T, s *Server, providerID, upstream string) string {
	t.Helper()
	ctx := context.Background()
	if err := s.pg.SeedProviderModels(ctx, providerID, []string{upstream}, model.ProviderModelSourceManual); err != nil {
		t.Fatalf("SeedProviderModels: %v", err)
	}
	list, err := s.pg.ListProviderModels(ctx, providerID)
	if err != nil {
		t.Fatalf("ListProviderModels: %v", err)
	}
	for _, m := range list {
		if m.UpstreamModel == upstream {
			return m.ID
		}
	}
	t.Fatalf("candidate %q missing", upstream)
	return ""
}

func publishRequest(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/models/publish", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handlePublishModel(rec, req)
	return rec
}

func TestPublishEndpointCreatesThenRequiresExplicitAppend(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	ctx := context.Background()
	primary, err := pg.CreateProvider(ctx, model.Provider{
		Name: "http-primary", BaseURL: "https://primary.example.test/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider primary: %v", err)
	}
	backup, err := pg.CreateProvider(ctx, model.Provider{
		Name: "http-backup", BaseURL: "https://backup.example.test/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider backup: %v", err)
	}
	s := providerModelTestServer(t, pg)
	first := publishTestCandidate(t, s, primary.ID, "up-primary")
	second := publishTestCandidate(t, s, backup.ID, "up-backup")

	rec := publishRequest(t, s, `{"provider_model_id":"`+first+`","alias":"http-alias","display_name":"HTTP Alias"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("new publish = %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Created      bool  `json:"created"`
		RouteCreated bool  `json:"route_created"`
		Version      int64 `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("publish response: %v", err)
	}
	if !created.Created || !created.RouteCreated || created.Version != 1 {
		t.Fatalf("new publish = %+v", created)
	}

	rec = publishRequest(t, s, `{"provider_model_id":"`+second+`","alias":"http-alias"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "alias_exists") {
		t.Fatalf("implicit append = %d %s", rec.Code, rec.Body.String())
	}

	rec = publishRequest(t, s, `{"provider_model_id":"`+second+`","alias":"http-alias","append_to_existing":true}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"route_created":true`) {
		t.Fatalf("explicit append = %d %s", rec.Code, rec.Body.String())
	}
}

func TestLegacyCreateSharesPublishAndIsIdempotent(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	ctx := context.Background()
	provider, err := pg.CreateProvider(ctx, model.Provider{
		Name: "legacy-provider", BaseURL: "https://legacy.example.test/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	s := providerModelTestServer(t, pg)
	body := `{"alias":"legacy-alias-http","provider_id":"` + provider.ID + `","upstream_model":"legacy-up","status":1}`
	post := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleModels(rec, httptest.NewRequest(http.MethodPost, "/admin/models", strings.NewReader(body)))
		return rec
	}
	if rec := post(); rec.Code != http.StatusCreated {
		t.Fatalf("first create = %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(); rec.Code != http.StatusOK {
		t.Fatalf("repeat create = %d %s", rec.Code, rec.Body.String())
	}
	models, err := pg.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	found := 0
	for _, m := range models {
		if m.Alias == "legacy-alias-http" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("legacy create produced %d models, want 1", found)
	}
	candidates, err := pg.ListProviderModels(ctx, provider.ID)
	if err != nil {
		t.Fatalf("ListProviderModels: %v", err)
	}
	candidateFound := false
	for _, c := range candidates {
		if c.UpstreamModel == "legacy-up" {
			candidateFound = true
		}
	}
	if !candidateFound {
		t.Fatal("legacy create did not record the candidate")
	}
}

func TestModelUpdateRejectsStaleExpectedVersion(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	ctx := context.Background()
	provider, err := pg.CreateProvider(ctx, model.Provider{
		Name: "version-provider", BaseURL: "https://version.example.test/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	created, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "version-alias", Status: 1, ProviderID: provider.ID, UpstreamModel: "version-up",
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	s := providerModelTestServer(t, pg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/admin/models/"+created.ID,
		strings.NewReader(`{"alias":"version-alias","display_name":"stale","status":1,"expected_version":999}`))
	s.handleModelByID(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "config_version_conflict") {
		t.Fatalf("stale update = %d %s", rec.Code, rec.Body.String())
	}
	stored, err := pg.ModelByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("ModelByID: %v", err)
	}
	if stored.DisplayName == "stale" || stored.ConfigVersion != created.ConfigVersion {
		t.Fatalf("stale update changed the model: %+v", stored)
	}
}

func TestModelAliasIsImmutableOnUpdate(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	ctx := context.Background()
	provider, err := pg.CreateProvider(ctx, model.Provider{
		Name: "alias-provider", BaseURL: "https://alias.example.test/v1", Protocol: "openai-chat",
		CredentialEnv: "MS1_TEST_PROVIDER_KEY", Status: 1,
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	created, err := pg.CreateModel(ctx, model.ModelConfig{
		Alias: "fixed-alias", Status: 1, ProviderID: provider.ID, UpstreamModel: "up-fixed",
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	s := providerModelTestServer(t, pg)

	req := httptest.NewRequest(http.MethodPut, "/admin/models/"+created.ID,
		strings.NewReader(`{"alias":"renamed-alias","status":1}`))
	rec := httptest.NewRecorder()
	s.handleModelByID(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "alias_immutable") {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/admin/models/"+created.ID,
		strings.NewReader(`{"alias":"fixed-alias","display_name":"Fixed","status":1}`))
	rec = httptest.NewRecorder()
	s.handleModelByID(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-alias update = %d %s", rec.Code, rec.Body.String())
	}
	stored, err := pg.ModelByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("ModelByID: %v", err)
	}
	if stored.Alias != "fixed-alias" || stored.DisplayName != "Fixed" || stored.ConfigVersion != 2 {
		t.Fatalf("stored = %+v", stored)
	}
}
