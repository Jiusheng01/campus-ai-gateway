package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// newDirectTestServer builds a Server whose routing table is already warm.
//
// routeTableCached returns the seeded table without touching s.pg while the
// cache is fresh, which is what lets the whole data plane be exercised with no
// database: the only thing the tests have to supply is the table itself.
func newDirectTestServer(t *testing.T, table routeTable) *Server {
	t.Helper()
	if table.models == nil {
		table.models = map[string][]directRoute{}
	}
	return &Server{
		cfg:        Config{DataPlane: DataPlaneDirect},
		routeCache: table,
		routeAt:    time.Now(),
		transport:  &transportPool{},
		selector:   newRouteSelector(),
		breaker:    newCircuitBreaker(),
	}
}

func TestAssembleRouteTableSkipsUnroutableEntries(t *testing.T) {
	providers := []model.Provider{
		{ID: "p-ok", Name: "qwen", BaseURL: "https://example.test/v1", CredentialEnv: "KEY_QWEN", Status: 1},
		// Disabled is 2, not 0. A zero never persists — the write paths
		// normalise it — so a check against zero would route to every channel
		// an operator had switched off.
		{ID: "p-off", Name: "disabled", BaseURL: "https://off.test/v1", CredentialEnv: "KEY_OFF", Status: 2},
		{ID: "p-zero", Name: "zero-status", BaseURL: "https://zero.test/v1", CredentialEnv: "KEY_ZERO", Status: 0},
		{ID: "p-nourl", Name: "nourl", BaseURL: "   ", CredentialEnv: "KEY_NOURL", Status: 1},
	}
	models := []model.ModelConfig{
		{ID: "m-ok", Alias: "campus-smart", Status: 1},
		{ID: "m-off", Alias: "campus-off", Status: 2},
	}
	routesByModel := map[string][]model.ModelRoute{
		"m-ok": {
			{ID: "r-ok", ModelID: "m-ok", ProviderID: "p-ok", UpstreamModel: "qwen-plus", Priority: 1, Status: 1},
			{ID: "r-off", ModelID: "m-ok", ProviderID: "p-ok", UpstreamModel: "qwen-plus", Priority: 2, Status: 2},
			{ID: "r-poff", ModelID: "m-ok", ProviderID: "p-off", UpstreamModel: "qwen-plus", Priority: 3, Status: 1},
			{ID: "r-pzero", ModelID: "m-ok", ProviderID: "p-zero", UpstreamModel: "qwen-plus", Priority: 4, Status: 1},
			{ID: "r-nourl", ModelID: "m-ok", ProviderID: "p-nourl", UpstreamModel: "qwen-plus", Priority: 5, Status: 1},
			{ID: "r-ghost", ModelID: "m-ok", ProviderID: "p-missing", UpstreamModel: "qwen-plus", Priority: 6, Status: 1},
		},
		"m-off": {
			{ID: "r-off-model", ModelID: "m-off", ProviderID: "p-ok", UpstreamModel: "qwen-plus", Priority: 1, Status: 1},
		},
	}

	table := assembleRouteTable(providers, models, routesByModel)

	if _, ok := table.models["campus-off"]; ok {
		t.Fatalf("a disabled model must not appear in the routing table")
	}
	routes := table.models["campus-smart"]
	if len(routes) != 1 {
		t.Fatalf("expected exactly one routable line, got %d: %+v", len(routes), routes)
	}
	if routes[0].Provider != "qwen" {
		t.Fatalf("wrong provider kept: %q", routes[0].Provider)
	}
	if routes[0].CredentialEnv != "KEY_QWEN" {
		t.Fatalf("credential env must travel with the line: %q", routes[0].CredentialEnv)
	}
}

func TestAssembleRouteTableOrdersByPriorityNotWeight(t *testing.T) {
	providers := []model.Provider{
		{ID: "p1", Name: "primary", BaseURL: "https://a.test/v1", CredentialEnv: "K1", Status: 1},
		{ID: "p2", Name: "backup-a", BaseURL: "https://b.test/v1", CredentialEnv: "K2", Status: 1},
		{ID: "p3", Name: "backup-b", BaseURL: "https://c.test/v1", CredentialEnv: "K3", Status: 1},
	}
	models := []model.ModelConfig{{ID: "m1", Alias: "campus-smart", Status: 1}}
	// Deliberately declared out of order, and with the lighter line first. The
	// weight must NOT reorder the band any more: it is the smooth weighted round
	// robin's input (orderRoutes), not a sort key. Only priority moves lines.
	routesByModel := map[string][]model.ModelRoute{
		"m1": {
			{ModelID: "m1", ProviderID: "p3", UpstreamModel: "x", Priority: 2, Weight: 50, Status: 1},
			{ModelID: "m1", ProviderID: "p1", UpstreamModel: "x", Priority: 1, Weight: 100, Status: 1},
			{ModelID: "m1", ProviderID: "p2", UpstreamModel: "x", Priority: 2, Weight: 100, Status: 1},
		},
	}

	routes := assembleRouteTable(providers, models, routesByModel).models["campus-smart"]
	got := make([]string, len(routes))
	for i, r := range routes {
		got[i] = r.Provider
	}
	// Priority 1 first; within priority 2 the declaration order is kept
	// (backup-b was declared before backup-a), because weight no longer sorts.
	want := []string{"primary", "backup-b", "backup-a"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("line order = %v, want %v", got, want)
	}
}

func TestAssembleRouteTableSkipsRoutesWithoutUpstream(t *testing.T) {
	providers := []model.Provider{{ID: "p1", Name: "qwen", BaseURL: "https://a.test/v1", CredentialEnv: "K1", Status: 1}}
	models := []model.ModelConfig{{ID: "m1", Alias: "campus-smart", Status: 1}}
	routesByModel := map[string][]model.ModelRoute{
		"m1": {{ModelID: "m1", ProviderID: "p1", UpstreamModel: "", Priority: 1, Status: 1}},
	}

	if _, ok := assembleRouteTable(providers, models, routesByModel).models["campus-smart"]; ok {
		t.Fatal("a route without an upstream model must not make the alias routable")
	}
}

func TestDirectTargetURL(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		path    string
		query   string
		want    string
		wantErr bool
	}{
		{name: "openai base repeats v1", base: "https://api.test/v1", path: "/v1/chat/completions", want: "https://api.test/v1/chat/completions"},
		{name: "vendor root keeps the path", base: "https://api.test", path: "/v1/chat/completions", want: "https://api.test/v1/chat/completions"},
		{name: "trailing slash on base", base: "https://api.test/v1/", path: "/v1/embeddings", want: "https://api.test/v1/embeddings"},
		{name: "query is preserved", base: "https://api.test/v1", path: "/v1/models", query: "limit=5", want: "https://api.test/v1/models?limit=5"},
		{name: "empty base is an error", base: "  ", path: "/v1/chat/completions", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &url.URL{Path: tc.path, RawQuery: tc.query}
			got, err := directTargetURL(tc.base, u)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("target = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRewriteJSONModel(t *testing.T) {
	out, ok := rewriteJSONModel([]byte(`{"model":"campus-smart","messages":[{"role":"user","content":"hi"}],"temperature":0.2}`), "qwen-plus")
	if !ok {
		t.Fatal("expected the body to be rewritten")
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("rewritten body is not JSON: %v", err)
	}
	if got["model"] != "qwen-plus" {
		t.Fatalf("model = %v, want qwen-plus", got["model"])
	}
	if got["temperature"] != 0.2 {
		t.Fatalf("unrelated fields must survive the rewrite: %v", got["temperature"])
	}
	if _, ok := got["messages"]; !ok {
		t.Fatal("messages must survive the rewrite")
	}

	if _, ok := rewriteJSONModel([]byte(`{"prompt":"no model here"}`), "x"); ok {
		t.Fatal("a body without a model field must not be reported as rewritten")
	}
	if _, ok := rewriteJSONModel([]byte("not json at all"), "x"); ok {
		t.Fatal("a non-JSON body must not be reported as rewritten")
	}
}

func TestShouldFailover(t *testing.T) {
	cases := map[int]bool{
		http.StatusOK:                  false,
		http.StatusBadRequest:          false, // the request is wrong everywhere
		http.StatusUnauthorized:        true,
		http.StatusForbidden:           true,
		http.StatusNotFound:            true,
		http.StatusRequestTimeout:      true,
		http.StatusTooManyRequests:     true,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:          true,
		http.StatusServiceUnavailable:  true,
	}
	for status, want := range cases {
		if got := shouldFailover(status); got != want {
			t.Errorf("shouldFailover(%d) = %v, want %v", status, got, want)
		}
	}
}

func TestServeDirectFailsOverToTheSecondChannel(t *testing.T) {
	var secondAuth string
	var secondBody string
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"capacity exhausted"}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		secondBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen-plus","usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`))
	}))
	defer second.Close()

	t.Setenv("TEST_DIRECT_KEY_PRIMARY", "sk-primary")
	t.Setenv("TEST_DIRECT_KEY_BACKUP", "sk-backup")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "primary", BaseURL: first.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_PRIMARY", Priority: 1},
			{Provider: "backup", BaseURL: second.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_BACKUP", Priority: 2},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"campus-smart","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(headerProvider); got != "backup" {
		t.Fatalf("provider attribution = %q, want backup", got)
	}
	if got := rec.Header().Get(headerAttempts); got != "2" {
		t.Fatalf("attempts = %q, want 2", got)
	}
	if got := rec.Header().Get(headerUpstreamModel); got != "qwen-plus" {
		t.Fatalf("upstream model = %q, want qwen-plus", got)
	}
	if secondAuth != "Bearer sk-backup" {
		t.Fatalf("the backup channel must receive its own credential, got %q", secondAuth)
	}
	// The client sent the campus alias; the channel must receive its own name.
	if !strings.Contains(secondBody, `"model":"qwen-plus"`) {
		t.Fatalf("the upstream model was not substituted: %s", secondBody)
	}
	if strings.Contains(secondBody, "campus-smart") {
		t.Fatalf("the campus alias leaked upstream: %s", secondBody)
	}
}

func TestServeDirectDoesNotFailOverOnABadRequest(t *testing.T) {
	var backupHits int64
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"messages is required"}`))
	}))
	defer first.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&backupHits, 1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer backup.Close()

	t.Setenv("TEST_DIRECT_KEY_A", "sk-a")
	t.Setenv("TEST_DIRECT_KEY_B", "sk-b")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "primary", BaseURL: first.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_A", Priority: 1},
			{Provider: "backup", BaseURL: backup.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_B", Priority: 2},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-smart"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want the upstream's 400", rec.Code)
	}
	if n := atomic.LoadInt64(&backupHits); n != 0 {
		t.Fatalf("a 400 must not be retried on another channel; backup was hit %d time(s)", n)
	}
}

func TestServeDirectReportsAMissingCredential(t *testing.T) {
	t.Setenv("TEST_DIRECT_KEY_UNSET", "")

	var reached int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reached, 1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_UNSET", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-smart"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "TEST_DIRECT_KEY_UNSET") {
		t.Fatalf("the error must name the missing variable so an operator can fix it: %s", rec.Body.String())
	}
	if n := atomic.LoadInt64(&reached); n != 0 {
		t.Fatalf("a channel without a credential must never be dialled; it was hit %d time(s)", n)
	}
}

func TestServeDirectRejectsAnUnknownModel(t *testing.T) {
	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {{Provider: "qwen", BaseURL: "https://a.test/v1", CredentialEnv: "K", Priority: 1}},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-unknown"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "model_not_configured") {
		t.Fatalf("expected model_not_configured, got %s", rec.Body.String())
	}
}

func TestServeDirectModelsListsWhatCanBeRouted(t *testing.T) {
	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-vision": {{Provider: "qwen", BaseURL: "https://a.test/v1", CredentialEnv: "K", Priority: 1}},
		"campus-smart":  {{Provider: "qwen", BaseURL: "https://a.test/v1", CredentialEnv: "K", Priority: 1}},
	}})

	rec := httptest.NewRecorder()
	s.serveDirectModels(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if out.Object != "list" {
		t.Fatalf("object = %q, want list", out.Object)
	}
	if len(out.Data) != 2 || out.Data[0].ID != "campus-smart" || out.Data[1].ID != "campus-vision" {
		t.Fatalf("model list = %+v, want campus-smart then campus-vision", out.Data)
	}
}

func TestServeModelTestDirectReportsTheServingChannel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen-plus","choices":[{"message":{"content":"pong"}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`))
	}))
	defer upstream.Close()

	t.Setenv("TEST_DIRECT_KEY_TEST", "sk-test")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_TEST", Priority: 1},
		},
	}})

	payload := []byte(`{"model":"campus-smart","messages":[{"role":"user","content":"ping"}],"stream":false}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/model-test", strings.NewReader(string(payload)))
	s.serveModelTestDirect(rec, req, modelTestRequest{Model: "campus-smart", Prompt: "ping"}, payload, "req_test")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var out modelTestResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if out.Channel != "qwen" {
		t.Fatalf("channel = %q, want qwen", out.Channel)
	}
	if out.Content != "pong" {
		t.Fatalf("content = %q, want pong", out.Content)
	}
	if out.TotalTokens != 7 {
		t.Fatalf("total tokens = %d, want 7 (derived from input+output)", out.TotalTokens)
	}
}

func TestServeDirectRecordsAttemptsWhenEveryLineFails(t *testing.T) {
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"模型暂时不可用"}`))
	}))
	defer upstream.Close()

	t.Setenv("TEST_DIRECT_KEY_FAIL_A", "sk-a")
	t.Setenv("TEST_DIRECT_KEY_FAIL_B", "sk-b")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "a", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_FAIL_A", Priority: 1},
			{Provider: "b", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_FAIL_B", Priority: 2},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"campus-smart"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want the upstream's 503", rec.Code)
	}
	if n := atomic.LoadInt64(&hits); n != 2 {
		t.Fatalf("both lines should have been tried, got %d", n)
	}
	// Without this header the request log would show a total failure with no
	// attempt count, which reads as "nothing was attempted".
	if got := rec.Header().Get(headerAttempts); got != "2" {
		t.Fatalf("attempts header = %q, want 2", got)
	}
	// The upstream body is not copied to the caller: it can carry internal
	// addresses and upstream credentials. The client gets the stable code and
	// the attempt count; the body is logged server-side.
	if strings.Contains(rec.Body.String(), "模型暂时不可用") {
		t.Fatalf("the upstream body must not be echoed to the caller: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("the stable error code must remain: %s", rec.Body.String())
	}
}

func TestRouteTableCacheDoesNotRebuildWhileFresh(t *testing.T) {
	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {{Provider: "qwen", BaseURL: "https://a.test/v1", CredentialEnv: "K", Priority: 1}},
	}})
	// s.pg is nil: any attempt to rebuild would panic rather than pass.
	table, err := s.routeTableCached(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(table.models["campus-smart"]) != 1 {
		t.Fatalf("the seeded table was not returned: %+v", table.models)
	}
}
