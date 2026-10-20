package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// "从上游刷新" reuses the admin-triggered, gateway-to-provider outbound path:
// the same credential resolution and transport as the connectivity test. The
// upstream's names are merged as source=fetched, an existing name keeps its
// original source, and a manual row is never removed.
func TestRefreshMergesUpstreamModelsAndKeepsManualRows(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)

	var gotMethod, gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"deepseek-chat"},
			{"id":"deepseek-v3"},
			{"id":"internal-model"},
			{"id":"deepseek-v3"},
			{"id":"   "}
		]}`))
	}))
	defer upstream.Close()

	s := providerModelTestServer(t, pg)
	created := createChannelThroughAPI(t, s, "deepseek", upstream.URL+"/v1")

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models",
		bytes.NewReader([]byte(`{"upstream_model":"internal-model"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("manual add status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models/refresh", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var refreshed struct {
		Data  []model.ProviderModel `json:"data"`
		Added int                   `json:"added"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refreshed); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	if refreshed.Added != 1 {
		t.Fatalf("refresh added %d, want 1 (deepseek-v3)", refreshed.Added)
	}

	if gotMethod != http.MethodGet || gotPath != "/v1/models" {
		t.Fatalf("upstream saw %s %s, want GET /v1/models", gotMethod, gotPath)
	}
	if gotAuth != "Bearer sk-test-value" {
		t.Fatalf("upstream Authorization = %q, want the channel's credential", gotAuth)
	}

	byName := make(map[string]model.ProviderModel, len(refreshed.Data))
	for _, m := range refreshed.Data {
		byName[m.UpstreamModel] = m
	}
	for _, want := range []struct{ name, source string }{
		{"deepseek-chat", model.ProviderModelSourcePreset},
		{"deepseek-reasoner", model.ProviderModelSourcePreset},
		{"internal-model", model.ProviderModelSourceManual},
		{"deepseek-v3", model.ProviderModelSourceFetched},
	} {
		got, ok := byName[want.name]
		if !ok {
			t.Fatalf("%s is missing after refresh: %+v", want.name, refreshed.Data)
		}
		if got.Source != want.source {
			t.Fatalf("%s source = %q, want %q", want.name, got.Source, want.source)
		}
	}
	if len(refreshed.Data) != 4 {
		t.Fatalf("the list has %d rows, want 4: %+v", len(refreshed.Data), refreshed.Data)
	}
}

// An upstream that answers with an error is a failed refresh: a clear error,
// and the list exactly as it was. The merge happens only after a complete,
// parsed upstream answer.
func TestRefreshFailsCleanlyWhenTheUpstreamErrors(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"upstream fell over"}`))
	}))
	defer upstream.Close()

	created := createChannelThroughAPI(t, s, "refresh-error", upstream.URL+"/v1")
	addManualModel(t, s, created.ID, "keep-me")

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models/refresh", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("refresh status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "500") {
		t.Fatalf("the failure should name the upstream status: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "upstream fell over") {
		t.Fatalf("the upstream body must not be echoed: %s", rec.Body.String())
	}
	assertOnlyManualKeepMe(t, listChannelModelsThroughAPI(t, s, created.ID))
}

// A 404 means the channel does not serve a model list, not that the gateway
// built a wrong URL. The message names the path the gateway actually asked for —
// the joined path, so an Ark channel reports /api/v3/models — and never the host
// it dialled (ADR-0009).
func TestRefreshExplainsThatA404MeansNoModelList(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	created := createChannelThroughAPI(t, s, "refresh-no-list", upstream.URL+"/api/v3")
	addManualModel(t, s, created.ID, "keep-me")

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models/refresh", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("refresh status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/api/v3/models" {
		t.Fatalf("upstream saw %q, want /api/v3/models", gotPath)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "GET /api/v3/models") {
		t.Fatalf("a 404 should name the joined path it asked for: %s", body)
	}
	if !strings.Contains(body, "does not expose a model list") {
		t.Fatalf("a 404 should explain that the channel has no model list: %s", body)
	}
	if strings.Contains(body, "127.0.0.1") || strings.Contains(body, upstream.URL) {
		t.Fatalf("the refresh message must not name the upstream host: %s", body)
	}
	assertOnlyManualKeepMe(t, listChannelModelsThroughAPI(t, s, created.ID))
}

func TestRefreshFailsCleanlyOnAMalformedUpstreamList(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`this is not a model list`))
	}))
	defer upstream.Close()

	created := createChannelThroughAPI(t, s, "refresh-malformed", upstream.URL+"/v1")
	addManualModel(t, s, created.ID, "keep-me")

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models/refresh", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("refresh status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	assertOnlyManualKeepMe(t, listChannelModelsThroughAPI(t, s, created.ID))
}

func TestRefreshFailsCleanlyWhenTheChannelIsUnreachable(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	created := createChannelThroughAPI(t, s, "refresh-unreachable", "http://127.0.0.1:1/v1")
	addManualModel(t, s, created.ID, "keep-me")

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models/refresh", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("refresh status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	assertOnlyManualKeepMe(t, listChannelModelsThroughAPI(t, s, created.ID))
}

// A channel whose credential is missing must fail before dialling: refresh is
// not allowed to turn "no key" into an empty Bearer request.
func TestRefreshFailsClosedWhenTheCredentialIsMissing(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)

	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = w.Write([]byte(`{"data":[{"id":"should-not-arrive"}]}`))
	}))
	defer upstream.Close()

	created, err := pg.CreateProvider(context.Background(), model.Provider{
		Name:          "refresh-missing-credential",
		BaseURL:       upstream.URL + "/v1",
		Protocol:      "openai-chat",
		CredentialEnv: "TEST_REFRESH_MISSING_KEY",
		Status:        1,
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	addManualModel(t, s, created.ID, "keep-me")

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models/refresh", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("refresh status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt64(&hits); n != 0 {
		t.Fatalf("a missing credential must never be dialled; upstream was hit %d time(s)", n)
	}
	if !strings.Contains(rec.Body.String(), "TEST_REFRESH_MISSING_KEY") {
		t.Fatalf("the failure should name the missing variable: %s", rec.Body.String())
	}
	assertOnlyManualKeepMe(t, listChannelModelsThroughAPI(t, s, created.ID))
}

// ADR-0012 disables automatic redirects. A channel whose /models answers with
// a redirect must not bounce the gateway to another host; that would let a
// channel's configuration choose where the gateway's credential is sent.
func TestRefreshDoesNotFollowUpstreamRedirects(t *testing.T) {
	var targetHits int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&targetHits, 1)
		_, _ = w.Write([]byte(`{"data":[{"id":"stolen"}]}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL+"/v1/models")
		w.WriteHeader(http.StatusFound)
	}))
	defer redirector.Close()

	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)
	created := createChannelThroughAPI(t, s, "redirecting", redirector.URL+"/v1")

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models/refresh", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("refresh status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt64(&targetHits); n != 0 {
		t.Fatalf("the refresh followed the redirect to %s %d time(s)", target.URL, n)
	}
	if !strings.Contains(rec.Body.String(), "302") {
		t.Fatalf("the failure should name the redirect status: %s", rec.Body.String())
	}
}

// Refresh reuses the data plane's dialer, so a target the egress policy denies
// is a clear, channel-named failure rather than a generic unreachable one.
func TestRefreshReportsAnEgressDenialWithTheChannelAndReason(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)
	s.egress = strictEgressPolicy()

	// The loopback host is allowlisted for http:// at write time, so the target
	// reaches the dialer; the strict address policy is what refuses it.
	created := createChannelThroughAPI(t, s, "egress-blocked", "http://127.0.0.1:1/v1")

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models/refresh", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("refresh status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "provider_egress_denied") {
		t.Fatalf("the failure must carry the egress denial code: %s", body)
	}
	if !strings.Contains(body, "egress-blocked") || !strings.Contains(body, "loopback") {
		t.Fatalf("the failure must name the channel and the reason: %s", body)
	}
	if ipLiteral.MatchString(body) {
		t.Fatalf("the admin response must not expose the resolved address either: %s", body)
	}
}

// A refresh denial is a security event, exactly like a data-plane denial: the
// admin button is another way to point a channel at the gateway's own network,
// and an auditor needs to see the attempt.
func TestRefreshEgressDenialRecordsASecurityEvent(t *testing.T) {
	pg := usagePostgres(t)
	clearProviderFixtures(t, pg)
	s := providerModelTestServer(t, pg)
	s.egress = strictEgressPolicy()

	created := createChannelThroughAPI(t, s, "egress-audited", "http://127.0.0.1:1/v1")

	const reqID = "req_refresh_egress_denied"
	req := httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+created.ID+"/models/refresh", nil)
	req.Header.Set("X-Request-Id", reqID)
	rec := httptest.NewRecorder()
	s.handleAdmin(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("refresh status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}

	events, err := pg.ListSecurityEvents(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListSecurityEvents: %v", err)
	}
	for _, event := range events {
		if event.RequestID != reqID {
			continue
		}
		if event.EventType != egressDenialEventType {
			t.Fatalf("event type = %q, want %q", event.EventType, egressDenialEventType)
		}
		if !strings.Contains(event.Detail, "egress-audited") || !strings.Contains(event.Detail, "loopback") {
			t.Fatalf("the event detail must name the channel and reason: %q", event.Detail)
		}
		return
	}
	t.Fatalf("no security event was recorded for the denied refresh (%d recent events)", len(events))
}

func addManualModel(t *testing.T, s *Server, providerID, name string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"upstream_model": name})
	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost,
		"/admin/providers/"+providerID+"/models", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("manual add %q status = %d, body = %s", name, rec.Code, rec.Body.String())
	}
}

func assertOnlyManualKeepMe(t *testing.T, models []model.ProviderModel) {
	t.Helper()
	if len(models) != 1 || models[0].UpstreamModel != "keep-me" || models[0].Source != model.ProviderModelSourceManual {
		t.Fatalf("a failed refresh must leave the list intact, got %+v", models)
	}
}
