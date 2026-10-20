package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The model catalog is the operator's answer to "I configured a channel, what
// can it serve?" — so these tests drive the two catalog endpoints through
// handleAdmin against a real migrated database, like the Provider Model tests.
// They skip when no test PostgreSQL answers.

func catalogThroughAPI(t *testing.T, s *Server) []model.CatalogModel {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodGet, "/admin/model-catalog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []model.CatalogModel `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	return body.Data
}

func catalogRow(t *testing.T, items []model.CatalogModel, upstream string) model.CatalogModel {
	t.Helper()
	for _, item := range items {
		if item.UpstreamModel == upstream {
			return item
		}
	}
	t.Fatalf("catalog has no row for %q: %+v", upstream, items)
	return model.CatalogModel{}
}

func openThroughAPI(t *testing.T, s *Server, providerModelID string) model.CatalogOpenResult {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"provider_model_id": providerModelID})
	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/model-catalog/open", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("open status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out model.CatalogOpenResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode open result: %v", err)
	}
	return out
}

func findProviderModel(t *testing.T, s *Server, providerID, upstream string) model.ProviderModel {
	t.Helper()
	for _, m := range listChannelModelsThroughAPI(t, s, providerID) {
		if m.UpstreamModel == upstream {
			return m
		}
	}
	t.Fatalf("channel %s does not list %q", providerID, upstream)
	return model.ProviderModel{}
}

// The catalog is cross-channel: one row per upstream name, naming every channel
// that offers it and whether client traffic can already reach it. Opening one
// channel's model flips that provider's state without pretending the other
// channel is opened.
func TestModelCatalogIsCrossChannelAndTracksOpenState(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	deepseek := createChannelThroughAPI(t, s, "deepseek", "https://deepseek.example.test/v1")
	internal := createChannelThroughAPI(t, s, "campus-internal", "https://internal.example.test/v1")
	addManualModel(t, s, internal.ID, "deepseek-chat")

	row := catalogRow(t, catalogThroughAPI(t, s), "deepseek-chat")
	if len(row.Providers) != 2 {
		t.Fatalf("deepseek-chat has %d providing channels, want 2: %+v", len(row.Providers), row.Providers)
	}
	if row.Opened {
		t.Fatalf("deepseek-chat is not opened yet: %+v", row)
	}

	opened := openThroughAPI(t, s, findProviderModel(t, s, deepseek.ID, "deepseek-chat").ID)
	if !opened.Created || !opened.RouteCreated {
		t.Fatalf("first open must create the model and its first route: %+v", opened)
	}

	row = catalogRow(t, catalogThroughAPI(t, s), "deepseek-chat")
	if !row.Opened {
		t.Fatalf("deepseek-chat should be opened after the action: %+v", row)
	}
	for _, p := range row.Providers {
		got := p.ID == deepseek.ID
		if p.Opened != got {
			t.Fatalf("provider %s opened = %v, want %v (only the opened channel)", p.Name, p.Opened, got)
		}
	}
}

// Opening a Provider Model that no Virtual Model shares creates the Virtual
// Model under the upstream name and its first Route with the same defaults the
// console uses for a primary line.
func TestOpeningACatalogModelCreatesTheVirtualModelAndFirstRoute(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	created := createChannelThroughAPI(t, s, "campus-internal", "https://internal.example.test/v1")
	addManualModel(t, s, created.ID, "internal-chat-v1")
	pm := findProviderModel(t, s, created.ID, "internal-chat-v1")

	out := openThroughAPI(t, s, pm.ID)
	if !out.Created {
		t.Fatalf("a new alias must create the Virtual Model: %+v", out)
	}
	if out.Model.ID == "" || out.Model.Alias != "internal-chat-v1" {
		t.Fatalf("model = %+v, want alias internal-chat-v1", out.Model)
	}
	if !out.RouteCreated {
		t.Fatalf("the first Route was not reported as created: %+v", out)
	}
	if out.Route.ModelID != out.Model.ID ||
		out.Route.ProviderID != created.ID ||
		out.Route.UpstreamModel != "internal-chat-v1" {
		t.Fatalf("route = %+v, want the opened channel and model", out.Route)
	}
	if out.Route.Priority != 1 || out.Route.Weight != 100 || out.Route.TimeoutMS != 60000 || out.Route.Status != 1 {
		t.Fatalf("route defaults = %+v, want priority 1, weight 100, timeout 60000, enabled", out.Route)
	}

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodGet, "/admin/models/"+out.Model.ID+"/routes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("routes status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var routes struct {
		Data []model.ModelRoute `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &routes); err != nil {
		t.Fatalf("decode routes: %v", err)
	}
	if len(routes.Data) != 1 || routes.Data[0].ID != out.Route.ID {
		t.Fatalf("the model has %+v, want exactly the route just created (%s)", routes.Data, out.Route.ID)
	}
}

// The unique binding (model_id, provider_id, upstream_model) makes a repeated
// open a no-op: the same Virtual Model and the same Route, never a second row.
func TestOpeningTheSameCatalogModelTwiceIsIdempotent(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	created := createChannelThroughAPI(t, s, "campus-internal", "https://internal.example.test/v1")
	addManualModel(t, s, created.ID, "internal-chat-v1")
	pm := findProviderModel(t, s, created.ID, "internal-chat-v1")

	first := openThroughAPI(t, s, pm.ID)
	second := openThroughAPI(t, s, pm.ID)

	if second.Created || second.RouteCreated {
		t.Fatalf("a repeated open must not create anything: %+v", second)
	}
	if second.Model.ID != first.Model.ID || second.Route.ID != first.Route.ID {
		t.Fatalf("repeated open returned a different model/route: first %+v, second %+v", first, second)
	}

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodGet, "/admin/models/"+first.Model.ID+"/routes", nil))
	var routes struct {
		Data []model.ModelRoute `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &routes)
	if len(routes.Data) != 1 {
		t.Fatalf("the model has %d routes after two opens, want 1", len(routes.Data))
	}
}

// Opening the same upstream name from a second channel appends a Route to the
// existing Virtual Model — the cross-vendor failover the gateway exists for.
// The appended line sits behind the first, so it is a backup rather than a
// silent 50/50 split.
func TestOpeningTheSameNameFromAnotherChannelAppendsARoute(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	first := createChannelThroughAPI(t, s, "campus-internal", "https://internal.example.test/v1")
	second := createChannelThroughAPI(t, s, "campus-secondary", "https://secondary.example.test/v1")
	addManualModel(t, s, first.ID, "shared-model")
	addManualModel(t, s, second.ID, "shared-model")

	one := openThroughAPI(t, s, findProviderModel(t, s, first.ID, "shared-model").ID)
	two := openThroughAPI(t, s, findProviderModel(t, s, second.ID, "shared-model").ID)

	if two.Created {
		t.Fatalf("the second channel must reuse the existing Virtual Model: %+v", two)
	}
	if !two.RouteCreated {
		t.Fatalf("the second channel's line was not created: %+v", two)
	}
	if two.Model.ID != one.Model.ID {
		t.Fatalf("second open model = %s, want %s", two.Model.ID, one.Model.ID)
	}
	if two.Route.ProviderID != second.ID || two.Route.UpstreamModel != "shared-model" {
		t.Fatalf("appended route = %+v, want the second channel", two.Route)
	}
	if two.Route.Priority != 2 {
		t.Fatalf("appended route priority = %d, want 2 (failover behind the first line)", two.Route.Priority)
	}

	row := catalogRow(t, catalogThroughAPI(t, s), "shared-model")
	if !row.Opened || len(row.Providers) != 2 {
		t.Fatalf("catalog row = %+v, want both channels opened", row)
	}
	for _, p := range row.Providers {
		if !p.Opened {
			t.Fatalf("channel %s should be opened after its own action: %+v", p.Name, row.Providers)
		}
	}
}

func TestOpeningAMissingProviderModelIs404(t *testing.T) {
	pg := usagePostgres(t)
	s := providerModelTestServer(t, pg)

	body := []byte(`{"provider_model_id":"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}`)
	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/model-catalog/open", bytes.NewReader(body)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}
