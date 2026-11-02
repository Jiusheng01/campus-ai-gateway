package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/ratelimit"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/redis/go-redis/v9"
)

// This is the acceptance flow the P0 work is about, exercised end to end
// against the real PostgreSQL, Redis and data plane:
//
//	register -> automatic Tenant -> console identity -> model catalog ->
//	personal key -> /v1/models -> /v1/chat/completions -> usage and logs
//
// A second account is registered through the same path and shown that neither
// the first account's key nor its Request Records are reachable. The upstream
// is an isolated httptest server; no real provider credential is involved.

const registrationFlowHMACSecret = "registration-flow-test-hmac-secret"
const registrationFlowUpstreamKey = "sk-registration-flow-upstream"

func registrationFlowServer(t *testing.T, pg *storage.Postgres, client *redis.Client, tenantID, upstreamURL string) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.PublicRegisterTenantID = tenantID
	cfg.KeyHMACSecret = registrationFlowHMACSecret
	cfg.PersonalKeyLimit = 10
	cfg.ProviderHTTPAllowlist = []string{"127.0.0.1"}
	cfg.DataPlane = DataPlaneDirect

	routeBase := strings.TrimSuffix(upstreamURL, "/") + "/v1"
	s := &Server{
		cfg:   cfg,
		pg:    pg,
		limit: ratelimit.New(client),
		mux:   http.NewServeMux(),
		routeCache: routeTable{models: map[string][]directRoute{
			"campus-flow": {{
				Provider:      "registration-flow-upstream",
				BaseURL:       routeBase,
				UpstreamModel: "upstream-flow",
				CredentialEnv: "REGISTRATION_FLOW_UPSTREAM_KEY",
				Priority:      1,
				TimeoutMS:     2000,
			}},
		}},
		routeAt:   time.Now(),
		transport: &transportPool{},
		selector:  newRouteSelector(),
		breaker:   newCircuitBreaker(),
		egress:    permissiveEgressPolicy(),
	}
	s.adminAuth = newAdminAuthService(pg, newFakeLimiter(8), []byte("registration-flow-test-secret"), cfg).
		withRegistrationLimiter(newFakeRegistrationLimiter(0))
	s.routes()
	s.upstream = http.HandlerFunc(s.serveDirect)
	return s
}

func registrationFlowUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-registration-flow",
			"object":  "chat.completion",
			"model":   "upstream-flow",
			"choices": []map[string]any{{"index": 0, "message": map[string]string{"role": "assistant", "content": "你好"}}},
			"usage":   map[string]int{"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7},
		})
	}))
}

func registrationFlowCall(t *testing.T, s *Server, method, path, token, body, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://gateway.local"+path, strings.NewReader(body))
	req.Header.Set("X-Request-Id", requestID)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func registerThroughTheFlow(t *testing.T, s *Server, username string) (accountID, accessToken string) {
	t.Helper()
	rec := registrationFlowCall(t, s, http.MethodPost, "/auth/register", "", registrationBody(username), "reg-flow-"+username)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %s = %d: %s", username, rec.Code, rec.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode registration response: %v", err)
	}
	if body.AccessToken == "" || body.User.ID == "" {
		t.Fatalf("registration response lacks a session or account: %s", rec.Body.String())
	}
	return body.User.ID, body.AccessToken
}

func TestNewMemberCanRegisterCallAModelAndSeeOnlyTheirOwnRecords(t *testing.T) {
	pg := usagePostgres(t)
	client := usageRedis(t)
	t.Setenv("REGISTRATION_FLOW_UPSTREAM_KEY", registrationFlowUpstreamKey)

	consoleModelsClear(t, pg)
	usageClearTables(t, pg)
	consoleModelsSeed(t, pg, "campus-flow", "校园流程", "端到端验收模型", 1, 1, 1)
	tenant := usageSeedTenant(t, pg, "reg-flow-tenant", 0, 0, 0)
	registrationTenantSeedAdmin(t, pg)

	upstream := registrationFlowUpstream(t)
	defer upstream.Close()
	s := registrationFlowServer(t, pg, client, tenant.ID, upstream.URL)

	// 1. Register. The response is a session, and the account already has the
	// configured Tenant before that session can be used.
	accountID, accessToken := registerThroughTheFlow(t, s, uniqueAccountName("flow-alice"))
	if got := registrationsTenant(t, pg, accountID); got != tenant.ID {
		t.Fatalf("registered membership = %q, want %q", got, tenant.ID)
	}

	// 2. The personal workspace already sees the membership.
	rec := registrationFlowCall(t, s, http.MethodGet, "/api/console/me", accessToken, "", "reg-flow-me")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/me = %d: %s", rec.Code, rec.Body.String())
	}
	var me consoleMeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.TenantID != tenant.ID || me.TenantStatus != 1 || !me.WorkspaceUsable {
		t.Fatalf("console identity = %+v, want the assigned enabled Tenant", me)
	}

	// 3. The catalog is the Tenant's allowlist intersected with what the data
	// plane can actually route.
	rec = registrationFlowCall(t, s, http.MethodGet, "/api/console/models", accessToken, "", "reg-flow-models")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/models = %d: %s", rec.Code, rec.Body.String())
	}
	var catalog consoleModelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].Alias != "campus-flow" {
		t.Fatalf("catalog = %+v, want [campus-flow]", catalog.Models)
	}

	// 4. The account creates its own key. The full credential is returned once.
	rec = registrationFlowCall(t, s, http.MethodPost, "/api/console/keys", accessToken,
		`{"name":"flow-key","allowed_models":["campus-flow"]}`, "reg-flow-key-create")
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/console/keys = %d: %s", rec.Code, rec.Body.String())
	}
	var created model.APIKeyCreateResult
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode key: %v", err)
	}
	if created.APIKey == "" {
		t.Fatal("the create response must carry the full key once")
	}

	// 5. The key authenticates against the data plane and names the model.
	rec = registrationFlowCall(t, s, http.MethodGet, "/v1/models", created.APIKey, "", "reg-flow-v1-models")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/models = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "campus-flow") {
		t.Fatalf("GET /v1/models did not advertise the callable model: %s", rec.Body.String())
	}

	// 6. A real (isolated) upstream serves the completion.
	rec = registrationFlowCall(t, s, http.MethodPost, "/v1/chat/completions", created.APIKey,
		`{"model":"campus-flow","messages":[{"role":"user","content":"你好"}]}`, "reg-flow-chat")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/chat/completions = %d: %s", rec.Code, rec.Body.String())
	}

	// 7. The request produced a record the owner can see, and the usage it
	// feeds is the owner's, not the whole deployment's.
	// The window is explicit: the request row's created_at comes from the
	// database clock, and a default "now" from the application clock can trail
	// it by a few milliseconds on a container host.
	usageFrom := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	usageTo := time.Now().UTC().Add(time.Minute).Format(time.RFC3339)
	usagePath := "/api/console/usage?from=" + url.QueryEscape(usageFrom) + "&to=" + url.QueryEscape(usageTo)
	rec = registrationFlowCall(t, s, http.MethodGet, usagePath, accessToken, "", "reg-flow-usage")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/usage = %d: %s", rec.Code, rec.Body.String())
	}
	var usage model.ConsoleUsage
	if err := json.Unmarshal(rec.Body.Bytes(), &usage); err != nil {
		t.Fatalf("decode usage: %v", err)
	}
	// The model listing is a data-plane request too and is recorded, so the
	// count is at least the chat call; the tokens belong to the completion.
	if usage.Totals.Requests < 1 || usage.Totals.TotalTokens != 7 {
		t.Fatalf("usage totals = %+v, want at least the 7-token completion", usage.Totals)
	}

	rec = registrationFlowCall(t, s, http.MethodGet, "/api/console/request-logs", accessToken, "", "reg-flow-logs")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/request-logs = %d: %s", rec.Code, rec.Body.String())
	}
	var logs struct {
		Data []struct {
			RequestID string `json:"request_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &logs); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	foundChat := false
	for _, row := range logs.Data {
		if row.RequestID == "reg-flow-chat" {
			foundChat = true
		}
	}
	if !foundChat {
		t.Fatalf("personal logs = %+v, want the chat request this account made", logs.Data)
	}

	// 8. A second self-registered account in the same Tenant sees none of the
	// first account's key or history.
	bobID, bobToken := registerThroughTheFlow(t, s, uniqueAccountName("flow-bob"))
	if bobID == accountID {
		t.Fatal("the second registration reused the first account")
	}

	rec = registrationFlowCall(t, s, http.MethodGet, "/api/console/keys", bobToken, "", "reg-flow-bob-keys")
	if rec.Code != http.StatusOK {
		t.Fatalf("bob GET /api/console/keys = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), created.APIKey) || strings.Contains(rec.Body.String(), "flow-key") {
		t.Fatalf("another account saw the first account's key: %s", rec.Body.String())
	}

	rec = registrationFlowCall(t, s, http.MethodGet, "/api/console/keys/"+created.ID, bobToken, "", "reg-flow-bob-key-read")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bob read another account's key = %d, want 404: %s", rec.Code, rec.Body.String())
	}

	rec = registrationFlowCall(t, s, http.MethodGet, "/api/console/request-logs", bobToken, "", "reg-flow-bob-logs")
	if rec.Code != http.StatusOK {
		t.Fatalf("bob GET /api/console/request-logs = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "reg-flow-chat") {
		t.Fatalf("another account saw the first account's request record: %s", rec.Body.String())
	}
}

func registrationsTenant(t *testing.T, pg *storage.Postgres, accountID string) string {
	t.Helper()
	var tenant *string
	if err := pg.Pool.QueryRow(context.Background(),
		`SELECT tenant_id::text FROM gateway_admin_user WHERE id=$1::uuid`, accountID).Scan(&tenant); err != nil {
		t.Fatalf("read membership: %v", err)
	}
	if tenant == nil {
		return ""
	}
	return *tenant
}
