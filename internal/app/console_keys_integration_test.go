package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

// These tests exercise the personal key endpoints at the HTTP seam against a
// migrated Postgres. The rule under test in several cases — the key limit and
// the owner/tenant derivation — is a property of the create transaction, which
// an in-memory fake could only confirm about itself.

const consoleKeysHMACSecret = "console-keys-test-hmac-secret"

// consoleKeysServer wires the real console routes and the real storage layer
// behind the session auth service, so the middleware, the handler and the
// transaction all run as they do in production.
func consoleKeysServer(t *testing.T, pg *storage.Postgres, limit int64) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.KeyHMACSecret = consoleKeysHMACSecret
	cfg.PersonalKeyLimit = limit
	s := &Server{cfg: cfg, pg: pg, mux: http.NewServeMux()}
	s.adminAuth = newAdminAuthService(pg, newFakeLimiter(8), []byte("console-keys-test-secret"), cfg)
	s.routes()
	return s
}

func consoleKeysTenant(t *testing.T, pg *storage.Postgres, code string, allowed []string) model.Tenant {
	t.Helper()
	tenant, err := pg.CreateTenant(context.Background(), model.Tenant{
		Code: code, Name: code, Status: 1, AllowedModels: allowed,
	})
	if err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return tenant
}

func consoleKeysAssign(t *testing.T, pg *storage.Postgres, userID, tenantID string) {
	t.Helper()
	if _, err := pg.AssignAdminUserTenant(context.Background(), userID, tenantID); err != nil {
		t.Fatalf("assign tenant %s to %s: %v", tenantID, userID, err)
	}
}

func consoleKeysCall(t *testing.T, s *Server, token, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://gateway.local/api/console/keys", strings.NewReader(body))
	req.Header.Set("X-Request-Id", "console-keys-test")
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

func decodeConsoleKeyCreate(t *testing.T, rec *httptest.ResponseRecorder) model.APIKeyCreateResult {
	t.Helper()
	var out model.APIKeyCreateResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode create response %q: %v", rec.Body.String(), err)
	}
	return out
}

type consoleKeyListBody struct {
	Data []struct {
		ID            string   `json:"id"`
		TenantID      string   `json:"tenant_id"`
		Name          string   `json:"name"`
		KeyPrefix     string   `json:"key_prefix"`
		Status        int      `json:"status"`
		AllowedModels []string `json:"allowed_models"`
		CreatedAt     string   `json:"created_at"`
		LastUsedAt    *string  `json:"last_used_at"`
		APIKey        string   `json:"api_key"`
	} `json:"data"`
}

func decodeConsoleKeyList(t *testing.T, rec *httptest.ResponseRecorder) consoleKeyListBody {
	t.Helper()
	var out consoleKeyListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list response %q: %v", rec.Body.String(), err)
	}
	return out
}

func consoleErrorCodeAndDetail(t *testing.T, rec *httptest.ResponseRecorder, detail string) (string, any) {
	t.Helper()
	var envelope struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope %q: %v", rec.Body.String(), err)
	}
	code, _ := envelope.Error["code"].(string)
	return code, envelope.Error[detail]
}

// A member's list is exactly the keys they own. Another Account's key and an
// administrator-created key (owner NULL) are not merely hidden from the
// rendered table; the query never selects them.
func TestConsoleKeysListsOnlyTheSessionsKeys(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)

	tenant := consoleKeysTenant(t, pg, "keys-own", []string{model.AllModelsWildcard})
	alice := tenantAssignmentAccount(t, pg, uniqueAccountName("keys-alice"), []string{RoleMember})
	bob := tenantAssignmentAccount(t, pg, uniqueAccountName("keys-bob"), []string{RoleMember})
	consoleKeysAssign(t, pg, alice.ID, tenant.ID)
	consoleKeysAssign(t, pg, bob.ID, tenant.ID)

	aliceToken := tenantAssignmentToken(t, s, alice.Username)
	bobToken := tenantAssignmentToken(t, s, bob.Username)

	aliceRec := consoleKeysCall(t, s, aliceToken, http.MethodPost, `{"name":"alice-key","allowed_models":["*"]}`)
	if aliceRec.Code != http.StatusCreated {
		t.Fatalf("create alice key = %d: %s", aliceRec.Code, aliceRec.Body.String())
	}
	aliceKey := decodeConsoleKeyCreate(t, aliceRec)
	if aliceKey.APIKey == "" {
		t.Fatal("create response must be the one place the full key appears")
	}

	bobRec := consoleKeysCall(t, s, bobToken, http.MethodPost, `{"name":"bob-key","allowed_models":["*"]}`)
	if bobRec.Code != http.StatusCreated {
		t.Fatalf("create bob key = %d: %s", bobRec.Code, bobRec.Body.String())
	}

	// An administrator-created key has no owner and must stay invisible to
	// every personal account.
	if _, err := pg.CreateAPIKey(context.Background(), model.APIKeyAdmin{
		TenantID: tenant.ID, Name: "admin-key", KeyPrefix: "sk-admin", Status: 1,
		AllowedModels: []string{model.AllModelsWildcard},
	}, "digest-admin-key"); err != nil {
		t.Fatalf("seed unowned key: %v", err)
	}

	rec := consoleKeysCall(t, s, aliceToken, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeConsoleKeyList(t, rec)
	if len(body.Data) != 1 {
		t.Fatalf("alice sees %d keys, want 1: %s", len(body.Data), rec.Body.String())
	}
	if body.Data[0].ID != aliceKey.ID || body.Data[0].Name != "alice-key" {
		t.Fatalf("alice sees %+v, want her own key %s", body.Data[0], aliceKey.ID)
	}

	// The full key is the one thing that may never appear again. The list JSON
	// has no field for it and the plaintext must not occur anywhere in it.
	raw := rec.Body.String()
	if body.Data[0].APIKey != "" || strings.Contains(raw, aliceKey.APIKey) {
		t.Fatalf("list leaked the full key: %s", raw)
	}
	if strings.Contains(raw, "bob-key") || strings.Contains(raw, "admin-key") {
		t.Fatalf("list exposed another account's key: %s", raw)
	}
}

// The body cannot redirect owner or tenant: neither field exists on the
// request type, and both are read from the session inside the transaction.
func TestConsoleKeyCreateIgnoresForgedOwnerAndTenant(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)

	home := consoleKeysTenant(t, pg, "keys-home", []string{model.AllModelsWildcard})
	other := consoleKeysTenant(t, pg, "keys-other", []string{model.AllModelsWildcard})
	alice := tenantAssignmentAccount(t, pg, uniqueAccountName("forge-alice"), []string{RoleMember})
	bob := tenantAssignmentAccount(t, pg, uniqueAccountName("forge-bob"), []string{RoleMember})
	consoleKeysAssign(t, pg, alice.ID, home.ID)
	consoleKeysAssign(t, pg, bob.ID, other.ID)

	token := tenantAssignmentToken(t, s, alice.Username)
	body := `{"name":"forged","allowed_models":["*"],"tenant_id":"` + other.ID + `","owner_user_id":"` + bob.ID + `"}`
	rec := consoleKeysCall(t, s, token, http.MethodPost, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}

	var ownerID, tenantID string
	if err := pg.Pool.QueryRow(context.Background(), `
SELECT owner_user_id::text, tenant_id::text
FROM gateway_api_key WHERE id=$1::uuid
`, decodeConsoleKeyCreate(t, rec).ID).Scan(&ownerID, &tenantID); err != nil {
		t.Fatalf("read created key: %v", err)
	}
	if ownerID != alice.ID {
		t.Fatalf("owner = %s, want the session account %s", ownerID, alice.ID)
	}
	if tenantID != home.ID {
		t.Fatalf("tenant = %s, want the membership %s (the body claimed %s)", tenantID, home.ID, other.ID)
	}
}

// An Account with no Tenant has nothing to issue a key against; the refusal is
// distinct and actionable rather than a generic conflict.
func TestConsoleKeyCreateRefusesUnassignedAccount(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)

	member := tenantAssignmentAccount(t, pg, uniqueAccountName("keys-unassigned"), []string{RoleMember})
	token := tenantAssignmentToken(t, s, member.Username)

	rec := consoleKeysCall(t, s, token, http.MethodPost, `{"name":"no-tenant","allowed_models":["*"]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("create without a Tenant = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if code, _ := consoleErrorCodeAndDetail(t, rec, "code"); code != "tenant_not_assigned" {
		t.Fatalf("error code = %q, want tenant_not_assigned", code)
	}
}

// A Tenant whose allowlist is [] has denied every model. No personal key may
// be written, even one that asks for the wildcard.
func TestConsoleKeyCreateRefusesEmptyTenantAllowlist(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)

	tenant := consoleKeysTenant(t, pg, "keys-empty", []string{})
	member := tenantAssignmentAccount(t, pg, uniqueAccountName("keys-empty-member"), []string{RoleMember})
	consoleKeysAssign(t, pg, member.ID, tenant.ID)
	token := tenantAssignmentToken(t, s, member.Username)

	rec := consoleKeysCall(t, s, token, http.MethodPost, `{"name":"denied","allowed_models":["*"]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("create on an empty allowlist = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if code, _ := consoleErrorCodeAndDetail(t, rec, "code"); code != "tenant_models_empty" {
		t.Fatalf("error code = %q, want tenant_models_empty", code)
	}
}

// A requested explicit model outside the Tenant's list is refused, and the
// offending alias is on the error so the console can point at the selection.
func TestConsoleKeyCreateRefusesModelOutsideTenantAllowlist(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)

	tenant := consoleKeysTenant(t, pg, "keys-subset", []string{"campus-chat", "campus-embed"})
	member := tenantAssignmentAccount(t, pg, uniqueAccountName("keys-subset-member"), []string{RoleMember})
	consoleKeysAssign(t, pg, member.ID, tenant.ID)
	token := tenantAssignmentToken(t, s, member.Username)

	rec := consoleKeysCall(t, s, token, http.MethodPost,
		`{"name":"outside","allowed_models":["campus-chat","campus-forbidden"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create outside the allowlist = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	code, modelDetail := consoleErrorCodeAndDetail(t, rec, "model")
	if code != "model_not_allowed" {
		t.Fatalf("error code = %q, want model_not_allowed", code)
	}
	if modelDetail != "campus-forbidden" {
		t.Fatalf("error model = %v, want the offending alias", modelDetail)
	}
}

// The wildcard contract, both Tenant shapes. An explicit Tenant expands the
// requested "*" to today's list; a wildcard Tenant keeps "*"; an explicit
// request is stored as asked.
func TestConsoleKeyWildcardExpansion(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)

	explicit := consoleKeysTenant(t, pg, "keys-explicit", []string{"campus-chat", "campus-embed"})
	explicitMember := tenantAssignmentAccount(t, pg, uniqueAccountName("keys-explicit-member"), []string{RoleMember})
	consoleKeysAssign(t, pg, explicitMember.ID, explicit.ID)

	rec := consoleKeysCall(t, s, tenantAssignmentToken(t, s, explicitMember.Username), http.MethodPost,
		`{"name":"expanded","allowed_models":["*"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("wildcard on an explicit Tenant = %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeConsoleKeyCreate(t, rec)
	if len(created.AllowedModels) != 2 || created.AllowedModels[0] != "campus-chat" || created.AllowedModels[1] != "campus-embed" {
		t.Fatalf("expanded scope = %v, want the Tenant's explicit list", created.AllowedModels)
	}

	wild := consoleKeysTenant(t, pg, "keys-wild", []string{model.AllModelsWildcard})
	wildMember := tenantAssignmentAccount(t, pg, uniqueAccountName("keys-wild-member"), []string{RoleMember})
	consoleKeysAssign(t, pg, wildMember.ID, wild.ID)

	rec = consoleKeysCall(t, s, tenantAssignmentToken(t, s, wildMember.Username), http.MethodPost,
		`{"name":"kept-wild","allowed_models":["*"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("wildcard on a wildcard Tenant = %d: %s", rec.Code, rec.Body.String())
	}
	created = decodeConsoleKeyCreate(t, rec)
	if len(created.AllowedModels) != 1 || created.AllowedModels[0] != model.AllModelsWildcard {
		t.Fatalf("wildcard scope = %v, want [\"*\"]", created.AllowedModels)
	}

	// A subset request on an explicit Tenant is stored exactly as a subset.
	rec = consoleKeysCall(t, s, tenantAssignmentToken(t, s, explicitMember.Username), http.MethodPost,
		`{"name":"subset","allowed_models":["campus-embed"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("subset on an explicit Tenant = %d: %s", rec.Code, rec.Body.String())
	}
	created = decodeConsoleKeyCreate(t, rec)
	if len(created.AllowedModels) != 1 || created.AllowedModels[0] != "campus-embed" {
		t.Fatalf("subset scope = %v, want [campus-embed]", created.AllowedModels)
	}
}

// The limit is a hard ceiling even when creates race: the account row lock
// serialises the count and the insert, so the number of successes is exactly
// the configured limit and every other attempt gets the distinct refusal.
func TestConsoleKeyLimitHoldsUnderConcurrentCreates(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	const limit = 3
	s := consoleKeysServer(t, pg, limit)

	tenant := consoleKeysTenant(t, pg, "keys-limit", []string{model.AllModelsWildcard})
	member := tenantAssignmentAccount(t, pg, uniqueAccountName("keys-limit-member"), []string{RoleMember})
	consoleKeysAssign(t, pg, member.ID, tenant.ID)
	token := tenantAssignmentToken(t, s, member.Username)

	const attempts = 8
	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = consoleKeysCall(t, s, token, http.MethodPost,
				`{"name":"concurrent-`+string(rune('a'+i))+`","allowed_models":["*"]}`)
		}(i)
	}
	wg.Wait()

	created, refused := 0, 0
	for _, rec := range results {
		switch rec.Code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			code, _ := consoleErrorCodeAndDetail(t, rec, "code")
			if code != "key_limit_reached" {
				t.Fatalf("concurrent refusal code = %q, want key_limit_reached: %s", code, rec.Body.String())
			}
			refused++
		default:
			t.Fatalf("concurrent create = %d: %s", rec.Code, rec.Body.String())
		}
	}
	if created != limit {
		t.Fatalf("%d concurrent creates succeeded, want exactly the limit %d", created, limit)
	}
	if refused != attempts-limit {
		t.Fatalf("%d concurrent creates were refused, want %d", refused, attempts-limit)
	}

	var owned int64
	if err := pg.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM gateway_api_key WHERE owner_user_id=$1::uuid`, member.ID).Scan(&owned); err != nil {
		t.Fatalf("count owned keys: %v", err)
	}
	if owned != limit {
		t.Fatalf("database holds %d keys, want %d", owned, limit)
	}
}

// /api/console/me carries the owner's key count as a hint. It is counted in
// SQL with the same owner predicate as the list, so another Account's keys and
// administrator-created (unowned) keys cannot inflate it.
func TestConsoleMeReportsOnlyTheSessionsKeyCount(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)

	tenant := consoleKeysTenant(t, pg, "me-count", []string{model.AllModelsWildcard})
	alice := tenantAssignmentAccount(t, pg, uniqueAccountName("me-count-alice"), []string{RoleMember})
	bob := tenantAssignmentAccount(t, pg, uniqueAccountName("me-count-bob"), []string{RoleMember})
	consoleKeysAssign(t, pg, alice.ID, tenant.ID)
	consoleKeysAssign(t, pg, bob.ID, tenant.ID)
	aliceToken := tenantAssignmentToken(t, s, alice.Username)

	for _, name := range []string{"alice-first", "alice-second"} {
		rec := consoleKeysCall(t, s, aliceToken, http.MethodPost, `{"name":"`+name+`","allowed_models":["*"]}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s = %d: %s", name, rec.Code, rec.Body.String())
		}
	}
	bobToken := tenantAssignmentToken(t, s, bob.Username)
	if rec := consoleKeysCall(t, s, bobToken, http.MethodPost, `{"name":"bob-key","allowed_models":["*"]}`); rec.Code != http.StatusCreated {
		t.Fatalf("create bob key = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := pg.CreateAPIKey(context.Background(), model.APIKeyAdmin{
		TenantID: tenant.ID, Name: "admin-key", KeyPrefix: "sk-admin-count", Status: 1,
		AllowedModels: []string{model.AllModelsWildcard},
	}, "digest-admin-count"); err != nil {
		t.Fatalf("seed unowned key: %v", err)
	}

	meRec := consoleCall(t, s, aliceToken, "/api/console/me")
	if meRec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/me = %d: %s", meRec.Code, meRec.Body.String())
	}
	var me struct {
		KeyCount int64 `json:"key_count"`
	}
	if err := json.Unmarshal(meRec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.KeyCount != 2 {
		t.Fatalf("key_count = %d, want alice's 2 keys only", me.KeyCount)
	}
}

// Every state-changing personal request leaves an admin audit row, with the
// Account's username as the actor. This is the ticket's audit guarantee at the
// HTTP seam: the row is written by the console middleware, not by the handler.
func TestConsoleKeyMutationWritesAnAdminAuditRow(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)

	tenant := consoleKeysTenant(t, pg, "keys-audit", []string{model.AllModelsWildcard})
	alice := tenantAssignmentAccount(t, pg, uniqueAccountName("keys-audit-alice"), []string{RoleMember})
	consoleKeysAssign(t, pg, alice.ID, tenant.ID)
	token := tenantAssignmentToken(t, s, alice.Username)

	rec := consoleKeysCall(t, s, token, http.MethodPost, `{"name":"audited-key","allowed_models":["*"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}

	rows, err := pg.ListAdminAudit(context.Background(), 20)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	var found bool
	for _, row := range rows {
		if row.Action != http.MethodPost || row.Resource != "/api/console/keys" || row.Actor != alice.Username {
			continue
		}
		found = true
		if row.StatusCode != http.StatusCreated {
			t.Fatalf("audit status = %d, want 201", row.StatusCode)
		}
		if row.RequestID != "console-keys-test" {
			t.Fatalf("audit request id = %q", row.RequestID)
		}
	}
	if !found {
		t.Fatalf("no audit row for the personal key create; rows = %+v", rows)
	}
}
