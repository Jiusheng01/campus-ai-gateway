package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

// These tests drive the Provider Model endpoints through handleAdmin, the same
// switch every /admin route uses, against a real migrated database. The
// contract is the JSON that leaves the endpoint: a channel created through the
// API already lists its preset models, and the list carries each name's source.
// They skip when no test PostgreSQL answers, like the other app integration
// tests.

func providerModelTestServer(t *testing.T, pg *storage.Postgres) *Server {
	t.Helper()
	return &Server{
		pg: pg,
		// httptest upstreams listen on 127.0.0.1, and plaintext HTTP is
		// refused unless the host is allowlisted. Configuring it here is the
		// same explicit exception an operator makes with
		// PROVIDER_HTTP_ALLOWLIST=127.0.0.1; production validation is not
		// loosened for the tests.
		cfg:                  Config{ProviderHTTPAllowlist: []string{"127.0.0.1"}},
		credentialSealingKey: bytes.Repeat([]byte{0x44}, security.CredentialKeySize),
		transport:            &transportPool{},
		// The same test-only injection as newDirectTestServer: these tests
		// dial httptest upstreams on 127.0.0.1, which the strict policy refuses.
		egress: permissiveEgressPolicy(),
	}
}

func clearProviderFixtures(t *testing.T, pg *storage.Postgres) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM gateway_model_route`,
		`DELETE FROM gateway_model`,
		`DELETE FROM gateway_provider`,
		`DELETE FROM gateway_credential`,
	} {
		if _, err := pg.Pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func createChannelThroughAPI(t *testing.T, s *Server, name, baseURL string) model.Provider {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name": name, "base_url": baseURL, "protocol": "openai-chat", "credential_secret": "sk-test-value",
	})
	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost, "/admin/providers", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created model.Provider
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created channel: %v", err)
	}
	return created
}

func listChannelModelsThroughAPI(t *testing.T, s *Server, providerID string) []model.ProviderModel {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodGet, "/admin/providers/"+providerID+"/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list models status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []model.ProviderModel `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode model list: %v", err)
	}
	return body.Data
}

// Creating a channel seeds the supplier's common models, and the list endpoint
// reports each one with source=preset. The seed is the server's, so a client
// that never loads the console templates still gets the list.
func TestCreatingAChannelSeedsAndListsItsPresetModels(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	created := createChannelThroughAPI(t, s, "deepseek", "https://deepseek.example.test/v1")
	models := listChannelModelsThroughAPI(t, s, created.ID)

	if len(models) == 0 {
		t.Fatal("a new deepseek channel must arrive with its preset models")
	}
	found := false
	for _, m := range models {
		if m.ProviderID != created.ID {
			t.Fatalf("model %q belongs to %s, want %s", m.UpstreamModel, m.ProviderID, created.ID)
		}
		if m.Source != model.ProviderModelSourcePreset {
			t.Fatalf("preset row %q has source %q", m.UpstreamModel, m.Source)
		}
		if m.UpstreamModel == "deepseek-chat" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the preset list is missing deepseek-chat: %+v", models)
	}
}

// The seed follows the endpoint, not the display name. An operator who picks a
// template and then renames the channel must still get that supplier's models;
// otherwise the first hop depends on leaving the template's name untouched.
func TestRenamedChannelIsSeededFromItsBaseURL(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	created := createChannelThroughAPI(t, s, "prod-east-channel", "https://api.deepseek.com/v1")
	models := listChannelModelsThroughAPI(t, s, created.ID)
	if len(models) == 0 {
		t.Fatal("a renamed channel on the DeepSeek base URL must still be seeded")
	}
	found := false
	for _, m := range models {
		if m.UpstreamModel == "deepseek-chat" && m.Source == model.ProviderModelSourcePreset {
			found = true
		}
	}
	if !found {
		t.Fatalf("the renamed channel is missing the DeepSeek preset: %+v", models)
	}
}

// A supplier the server has no preset for starts with an empty list — the
// endpoint still answers, and it does not borrow another supplier's models.
func TestUnknownSupplierStartsWithAnEmptyModelList(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	created := createChannelThroughAPI(t, s, "campus-internal", "https://internal.example.test/v1")
	if models := listChannelModelsThroughAPI(t, s, created.ID); len(models) != 0 {
		t.Fatalf("an unknown supplier must start empty, got %+v", models)
	}
}

func TestListingModelsForAMissingChannelIs404(t *testing.T) {
	pg := usagePostgres(t)
	s := providerModelTestServer(t, pg)

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodGet,
		"/admin/providers/6ba7b810-9dad-11d1-80b4-00c04fd430c8/models", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "provider_not_found") {
		t.Fatalf("the failure must name the missing channel: %s", rec.Body.String())
	}
}
