package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
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
		// httptest upstreams listen on 127.0.0.1 and speak plaintext HTTP, so
		// the test server carries the same explicit exception an operator makes
		// with PROVIDER_HTTP_ALLOWLIST. Production validation is not loosened.
		cfg:        Config{DataPlane: DataPlaneDirect, ProviderHTTPAllowlist: []string{"127.0.0.1"}},
		routeCache: table,
		routeAt:    time.Now(),
		transport:  &transportPool{},
		selector:   newRouteSelector(),
		breaker:    newCircuitBreaker(),
		// The permissive address policy is the test-only injection; NewServer
		// still constructs the strict one.
		egress: permissiveEgressPolicy(),
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
		{ID: "m-ok", Alias: "demo-model", UpstreamModel: "qwen-plus", Status: 1},
		{ID: "m-off", Alias: "campus-off", UpstreamModel: "qwen-plus", Status: 2},
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
	routes := table.models["demo-model"]
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
	models := []model.ModelConfig{{ID: "m1", Alias: "demo-model", Status: 1}}
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

	routes := assembleRouteTable(providers, models, routesByModel).models["demo-model"]
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

// A Route with no upstream model cannot serve a request, and the retired model
// column is not a second authority to fill it from. The line is skipped instead
// of being silently pointed at whatever the model used to say.
func TestAssembleRouteTableSkipsARouteWithNoUpstreamModel(t *testing.T) {
	providers := []model.Provider{{ID: "p1", Name: "qwen", BaseURL: "https://a.test/v1", CredentialEnv: "K1", Status: 1}}
	// UpstreamModel on the model is the retired field; it must have no effect.
	models := []model.ModelConfig{{ID: "m1", Alias: "demo-model", UpstreamModel: "qwen-plus", Status: 1}}
	routesByModel := map[string][]model.ModelRoute{
		"m1": {{ModelID: "m1", ProviderID: "p1", UpstreamModel: "", Priority: 1, Status: 1}},
	}

	table := assembleRouteTable(providers, models, routesByModel)
	if routes, ok := table.models["demo-model"]; ok {
		t.Fatalf("a Route without an upstream model must not be routable, got %+v", routes)
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
		// A base whose version segment is not /v1 still carries the version: the
		// inbound /v1 is the gateway's own route prefix, not part of the
		// vendor path. Ark serves /api/v3/chat/completions, not /api/v3/v1/...
		{name: "ark api v3", base: "https://ark.cn-beijing.volces.com/api/v3", path: "/v1/chat/completions", want: "https://ark.cn-beijing.volces.com/api/v3/chat/completions"},
		{name: "zhipu paas v4", base: "https://open.bigmodel.cn/api/paas/v4", path: "/v1/chat/completions", want: "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{name: "dashscope compatible-mode v1", base: "https://dashscope.aliyuncs.com/compatible-mode/v1", path: "/v1/chat/completions", want: "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"},
		// A non-version last segment is a proxy prefix, not a version: the
		// inbound /v1 is how the upstream addresses its API and is kept.
		{name: "non-version prefix keeps the path", base: "https://host.test/gateway", path: "/v1/chat/completions", want: "https://host.test/gateway/v1/chat/completions"},
		{name: "trailing slash on a versioned base", base: "https://ark.cn-beijing.volces.com/api/v3/", path: "/v1/chat/completions", want: "https://ark.cn-beijing.volces.com/api/v3/chat/completions"},
		// The version rule is deliberately narrow. A segment that merely starts
		// with "v" and digits is not a version, and treating it as one would drop
		// the inbound /v1 that the upstream expects.
		{name: "application path v1chat is not a version", base: "https://api.test/v1chat", path: "/v1/chat/completions", want: "https://api.test/v1chat/v1/chat/completions"},
		{name: "proxy path v2ray is not a version", base: "https://api.test/v2ray", path: "/v1/chat/completions", want: "https://api.test/v2ray/v1/chat/completions"},
		{name: "uppercase version segment", base: "https://api.test/V1", path: "/v1/chat/completions", want: "https://api.test/V1/chat/completions"},
		{name: "v1beta1", base: "https://api.test/v1beta1", path: "/v1/chat/completions", want: "https://api.test/v1beta1/chat/completions"},
		// A query or fragment on the base is not part of the path. Concatenating
		// the raw string would put the inbound path inside the base's query.
		{name: "base query is dropped", base: "https://api.test/v1?x=1", path: "/v1/models", want: "https://api.test/v1/models"},
		{name: "base fragment is dropped", base: "https://api.test/v1#frag", path: "/v1/models", want: "https://api.test/v1/models"},
		{name: "base query is dropped and inbound query is kept", base: "https://api.test/v1?x=1", path: "/v1/models", query: "limit=5", want: "https://api.test/v1/models?limit=5"},
		{name: "base query is dropped on a non-version prefix", base: "https://host.test/gateway?x=1", path: "/v1/chat/completions", want: "https://host.test/gateway/v1/chat/completions"},
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
	out, ok := rewriteJSONModel([]byte(`{"model":"demo-model","messages":[{"role":"user","content":"hi"}],"temperature":0.2}`), "qwen-plus")
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
		"demo-model": {
			{Provider: "primary", BaseURL: first.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_PRIMARY", Priority: 1},
			{Provider: "backup", BaseURL: second.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_BACKUP", Priority: 2},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"demo-model","messages":[{"role":"user","content":"hi"}]}`))
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
	if strings.Contains(secondBody, "demo-model") {
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
		"demo-model": {
			{Provider: "primary", BaseURL: first.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_A", Priority: 1},
			{Provider: "backup", BaseURL: backup.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_B", Priority: 2},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
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
		"demo-model": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_UNSET", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
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
		"demo-model": {{Provider: "qwen", BaseURL: "https://a.test/v1", CredentialEnv: "K", Priority: 1}},
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
		"demo-vision": {{Provider: "qwen", BaseURL: "https://a.test/v1", CredentialEnv: "K", Priority: 1}},
		"demo-model":  {{Provider: "qwen", BaseURL: "https://a.test/v1", CredentialEnv: "K", Priority: 1}},
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
	if len(out.Data) != 2 || out.Data[0].ID != "demo-model" || out.Data[1].ID != "demo-vision" {
		t.Fatalf("model list = %+v, want demo-model then demo-vision", out.Data)
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
		"demo-model": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_TEST", Priority: 1},
		},
	}})

	payload := []byte(`{"model":"demo-model","messages":[{"role":"user","content":"ping"}],"stream":false}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/model-test", strings.NewReader(string(payload)))
	s.serveModelTestDirect(rec, req, modelTestRequest{Model: "demo-model", Prompt: "ping"}, payload, "req_test")

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
	// The operator supplied the upstream name, so the probe can report which
	// model it actually sent without exposing anything secret.
	if out.UpstreamModel != "qwen-plus" {
		t.Fatalf("upstream model = %q, want qwen-plus", out.UpstreamModel)
	}
}

// A transport failure must report the resolved category rather than flattening
// DNS, timeout and refusal into one word. The upstream model travels with the
// failure line so a wrong model identifier is not mistaken for a path bug.
func TestServeModelTestDirectNamesTheFailureCategory(t *testing.T) {
	t.Setenv("TEST_MODEL_TEST_REFUSED", "sk-test")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {{
			Provider:      "qwen",
			BaseURL:       "http://127.0.0.1:1/v1",
			UpstreamModel: "qwen-plus",
			CredentialEnv: "TEST_MODEL_TEST_REFUSED",
			Priority:      1,
			TimeoutMS:     1000,
		}},
	}})

	payload := []byte(`{"model":"demo-model","messages":[{"role":"user","content":"ping"}],"stream":false}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/model-test", strings.NewReader(string(payload)))
	s.serveModelTestDirect(rec, req, modelTestRequest{Model: "demo-model", Prompt: "ping"}, payload, "req_category")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "unreachable") {
		t.Fatalf("the failure must name the resolved category, not the old literal: %s", body)
	}
	if !strings.Contains(body, "could not be reached") {
		t.Fatalf("a refused connection should say it could not be reached: %s", body)
	}
	if !strings.Contains(body, "qwen-plus") {
		t.Fatalf("the failure should report the upstream model it sent: %s", body)
	}
}

// A channel that accepts the connection but never answers is a timeout, which
// the operator must be able to tell apart from a refusal.
func TestServeModelTestDirectNamesATimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{"model":"qwen-plus"}`))
	}))
	defer slow.Close()

	t.Setenv("TEST_MODEL_TEST_TIMEOUT", "sk-test")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {{
			Provider:      "qwen",
			BaseURL:       slow.URL + "/v1",
			UpstreamModel: "qwen-plus",
			CredentialEnv: "TEST_MODEL_TEST_TIMEOUT",
			Priority:      1,
			TimeoutMS:     50,
		}},
	}})

	payload := []byte(`{"model":"demo-model","messages":[{"role":"user","content":"ping"}],"stream":false}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/model-test", strings.NewReader(string(payload)))
	s.serveModelTestDirect(rec, req, modelTestRequest{Model: "demo-model", Prompt: "ping"}, payload, "req_timeout")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "did not answer in time") {
		t.Fatalf("a timeout should say the channel did not answer in time: %s", body)
	}
}

// The failed attempt is recorded in the log with the target it dialled, so a
// duplicated path segment is visible at a glance. Userinfo and the query are
// not part of the target: a base_url with embedded credentials must not reach a
// retained log (ADR-0009).
func TestServeModelTestDirectLogsTheDialledTargetWithoutCredentials(t *testing.T) {
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })

	t.Setenv("TEST_MODEL_TEST_LOG", "sk-test")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {{
			Provider:      "qwen",
			BaseURL:       "http://user:super-secret@127.0.0.1:1/v1",
			UpstreamModel: "qwen-plus",
			CredentialEnv: "TEST_MODEL_TEST_LOG",
			Priority:      1,
			TimeoutMS:     1000,
		}},
	}})

	payload := []byte(`{"model":"demo-model","messages":[{"role":"user","content":"ping"}],"stream":false}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/model-test", strings.NewReader(string(payload)))
	s.serveModelTestDirect(rec, req, modelTestRequest{Model: "demo-model", Prompt: "ping"}, payload, "req_target")

	out := logged.String()
	if !strings.Contains(out, "http://127.0.0.1:1/v1/chat/completions") {
		t.Fatalf("the log should name the dialled scheme, host and path: %s", out)
	}
	if strings.Contains(out, "super-secret") {
		t.Fatalf("the log must not carry the base URL's userinfo: %s", out)
	}
}

func TestLoggableTargetDropsTheQueryString(t *testing.T) {
	got := loggableTarget("https://user:secret@api.test:8443/v1/chat/completions?api_key=leak")
	want := "https://api.test:8443/v1/chat/completions"
	if got != want {
		t.Fatalf("loggable target = %q, want %q", got, want)
	}
}

// An egress denial is a policy answer about the target the operator configured,
// not a failure to build a request. The target is the thing to fix, so the log
// must name it exactly like any other failed attempt. Only a missing credential
// fails before the target exists.
func TestServeModelTestDirectLogsTheTargetOnAnEgressDenial(t *testing.T) {
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })

	t.Setenv("TEST_MODEL_TEST_DENIED", "sk-test")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {{
			Provider:      "qwen",
			BaseURL:       "http://127.0.0.1:1/v1",
			UpstreamModel: "qwen-plus",
			CredentialEnv: "TEST_MODEL_TEST_DENIED",
			Priority:      1,
			TimeoutMS:     1000,
		}},
	}})
	// Deny the address in the dial hook, after the target has been built.
	s.egress = strictEgressPolicy()

	payload := []byte(`{"model":"demo-model","messages":[{"role":"user","content":"ping"}],"stream":false}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/model-test", strings.NewReader(string(payload)))
	s.serveModelTestDirect(rec, req, modelTestRequest{Model: "demo-model", Prompt: "ping"}, payload, "req_denied")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "denied by policy") {
		t.Fatalf("the response should still carry the address-free denial: %s", rec.Body.String())
	}
	if ipLiteral.MatchString(rec.Body.String()) {
		t.Fatalf("the response must not expose the resolved address: %s", rec.Body.String())
	}

	out := logged.String()
	if !strings.Contains(out, "http://127.0.0.1:1/v1/chat/completions") {
		t.Fatalf("a denial must log the target the operator configured: %s", out)
	}
	if !strings.Contains(out, "denied by policy") {
		t.Fatalf("the log line should state the policy reason: %s", out)
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
		"demo-model": {
			{Provider: "a", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_FAIL_A", Priority: 1},
			{Provider: "b", BaseURL: upstream.URL + "/v1", UpstreamModel: "x", CredentialEnv: "TEST_DIRECT_KEY_FAIL_B", Priority: 2},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
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
		"demo-model": {{Provider: "qwen", BaseURL: "https://a.test/v1", CredentialEnv: "K", Priority: 1}},
	}})
	// s.pg is nil: any attempt to rebuild would panic rather than pass.
	table, err := s.routeTableCached(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(table.models["demo-model"]) != 1 {
		t.Fatalf("the seeded table was not returned: %+v", table.models)
	}
}
