package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/google/uuid"
)

// These tests exercise ticket 06 at the HTTP seam against a migrated Postgres:
// edit, the dedicated status sub-action, and permanent deletion. The ownership
// rule and the model-scope bound are enforced in SQL, so an in-memory fake
// could only confirm them about itself.

func consoleKeyRequest(t *testing.T, s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://gateway.local"+path, strings.NewReader(body))
	req.Header.Set("X-Request-Id", "console-key-actions-test")
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

func consoleKeyCreateOwned(t *testing.T, s *Server, token, name, body string) model.APIKeyCreateResult {
	t.Helper()
	rec := consoleKeyRequest(t, s, token, http.MethodPost, "/api/console/keys", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create key %q = %d: %s", name, rec.Code, rec.Body.String())
	}
	return decodeConsoleKeyCreate(t, rec)
}

func consoleKeyDBState(t *testing.T, pg *storage.Postgres, id string) (tenantID, ownerID, name string, status int, models []string) {
	t.Helper()
	var modelsJSON []byte
	err := pg.Pool.QueryRow(context.Background(), `
SELECT tenant_id::text, owner_user_id::text, name, status, allowed_models
FROM gateway_api_key WHERE id=$1::uuid
`, id).Scan(&tenantID, &ownerID, &name, &status, &modelsJSON)
	if err != nil {
		t.Fatalf("read key %s: %v", id, err)
	}
	if err := json.Unmarshal(modelsJSON, &models); err != nil {
		t.Fatalf("decode key models: %v", err)
	}
	return tenantID, ownerID, name, status, models
}

func consoleKeyDigest(t *testing.T, pg *storage.Postgres, id string) string {
	t.Helper()
	var digest string
	if err := pg.Pool.QueryRow(context.Background(),
		`SELECT key_digest FROM gateway_api_key WHERE id=$1::uuid`, id).Scan(&digest); err != nil {
		t.Fatalf("read key digest: %v", err)
	}
	return digest
}

// consoleKeyDataPlaneCall pushes the credential through the real data-plane
// authentication middleware. A 200 means the key is in service; a 401 means it
// is not, which is the observable definition of disable/enable.
func consoleKeyDataPlaneCall(t *testing.T, s *Server, plain string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	req.Header.Set("X-Request-Id", "console-key-actions-data-plane")
	rec := httptest.NewRecorder()
	s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec
}

func consoleKeyFixture(t *testing.T, pg *storage.Postgres, prefix string, tenantModels []string) (*Server, string, string, model.AdminUser, model.AdminUser, model.Tenant) {
	t.Helper()
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)
	tenant := consoleKeysTenant(t, pg, prefix+"-tenant", tenantModels)
	account := tenantAssignmentAccount(t, pg, uniqueAccountName(prefix+"-member"), []string{RoleMember})
	consoleKeysAssign(t, pg, account.ID, tenant.ID)
	other := tenantAssignmentAccount(t, pg, uniqueAccountName(prefix+"-other"), []string{RoleMember})
	consoleKeysAssign(t, pg, other.ID, tenant.ID)
	return s, tenantAssignmentToken(t, s, account.Username), tenantAssignmentToken(t, s, other.Username), account, other, tenant
}

// Another Account's id is not "forbidden" on any verb: GET, PUT, DELETE and the
// status sub-action all answer 404, and the key behind them is unchanged. That
// is the property that stops the endpoint from confirming an id exists.
func TestConsoleKeyEndpointsAnswerNotFoundForAnotherAccountsKey(t *testing.T) {
	pg := usagePostgres(t)
	s, aliceToken, bobToken, _, _, _ := consoleKeyFixture(t, pg, "actions-cross", []string{model.AllModelsWildcard})

	bobKey := consoleKeyCreateOwned(t, s, bobToken, "bob-key", `{"name":"bob-key","allowed_models":["*"]}`)
	path := "/api/console/keys/" + bobKey.ID

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"get", http.MethodGet, path, ""},
		{"update", http.MethodPut, path, `{"name":"stolen","allowed_models":["*"]}`},
		{"delete", http.MethodDelete, path, ""},
		{"status", http.MethodPut, path + "/status", `{"status":2}`},
	}
	for _, c := range cases {
		rec := consoleKeyRequest(t, s, aliceToken, c.method, c.path, c.body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s on another account's key = %d, want 404: %s", c.name, rec.Code, rec.Body.String())
		}
		if code := errorCode(t, rec); code != "api_key_not_found" {
			t.Fatalf("%s error code = %q, want api_key_not_found", c.name, code)
		}
	}

	_, ownerID, name, status, _ := consoleKeyDBState(t, pg, bobKey.ID)
	if name != "bob-key" || status != 1 {
		t.Fatalf("another account's key was modified: name=%q status=%d", name, status)
	}
	if ownerID == "" {
		t.Fatal("another account's key lost its owner")
	}

	// A non-UUID and an unknown segment are the same answer, and never reach
	// the database cast.
	for _, p := range []string{"/api/console/keys/not-a-uuid", "/api/console/keys/" + uuid.NewString()} {
		rec := consoleKeyRequest(t, s, aliceToken, http.MethodGet, p, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", p, rec.Code)
		}
	}
}

// Pausing and resuming must not touch the credential: the digest is identical
// before and after, so re-enabling restores service with the same key.
func TestConsoleKeyDisableThenReenableRestoresTheSameCredential(t *testing.T) {
	pg := usagePostgres(t)
	s, token, _, _, _, _ := consoleKeyFixture(t, pg, "actions-pause", []string{model.AllModelsWildcard})

	created := consoleKeyCreateOwned(t, s, token, "paused-key", `{"name":"paused-key","allowed_models":["*"]}`)
	path := "/api/console/keys/" + created.ID + "/status"
	digestBefore := consoleKeyDigest(t, pg, created.ID)

	if rec := consoleKeyDataPlaneCall(t, s, created.APIKey); rec.Code != http.StatusOK {
		t.Fatalf("newly created key = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	rec := consoleKeyRequest(t, s, token, http.MethodPut, path, `{"status":2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable = %d: %s", rec.Code, rec.Body.String())
	}
	if _, _, _, status, _ := consoleKeyDBState(t, pg, created.ID); status != 2 {
		t.Fatalf("stored status = %d, want 2", status)
	}
	if rec := consoleKeyDataPlaneCall(t, s, created.APIKey); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled key = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if got := consoleKeyDigest(t, pg, created.ID); got != digestBefore {
		t.Fatal("disabling rotated the credential; re-enabling would not restore the same key")
	}

	rec = consoleKeyRequest(t, s, token, http.MethodPut, path, `{"status":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-enable = %d: %s", rec.Code, rec.Body.String())
	}
	if _, _, _, status, _ := consoleKeyDBState(t, pg, created.ID); status != 1 {
		t.Fatalf("stored status = %d, want 1", status)
	}
	if rec := consoleKeyDataPlaneCall(t, s, created.APIKey); rec.Code != http.StatusOK {
		t.Fatalf("re-enabled key = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := consoleKeyDigest(t, pg, created.ID); got != digestBefore {
		t.Fatal("re-enabling changed the credential digest")
	}

	// The zero value is not a status. Refusing it keeps "unknown" from being
	// written as neither enabled nor disabled.
	rec = consoleKeyRequest(t, s, token, http.MethodPut, path, `{"status":0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status 0 = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "invalid_api_key_status" {
		t.Fatalf("status 0 code = %q, want invalid_api_key_status", code)
	}
}

// Deletion removes the row and its credential. Request Records are not tied to
// the key by a foreign key: they keep the api_key_id they were written with and
// the owner snapshot, so the owner's history remains attributable.
func TestConsoleKeyDeleteRemovesTheRowAndKeepsTheOwnerSnapshot(t *testing.T) {
	pg := usagePostgres(t)
	s, token, _, account, _, tenant := consoleKeyFixture(t, pg, "actions-delete", []string{model.AllModelsWildcard})

	created := consoleKeyCreateOwned(t, s, token, "delete-me", `{"name":"delete-me","allowed_models":["*"]}`)

	const requestID = "req_console_key_actions_delete"
	if _, err := pg.Pool.Exec(context.Background(), `
INSERT INTO gateway_request_log(request_id, tenant_id, api_key_id, owner_user_id, requested_model, status_code, created_at)
VALUES ($1, $2::uuid, $3::uuid, $4::uuid, 'campus-chat', 200, NOW())
`, requestID, tenant.ID, created.ID, account.ID); err != nil {
		t.Fatalf("seed Request Record: %v", err)
	}

	rec := consoleKeyRequest(t, s, token, http.MethodDelete, "/api/console/keys/"+created.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body.String())
	}

	var remaining int
	if err := pg.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*)::int FROM gateway_api_key WHERE id=$1::uuid`, created.ID).Scan(&remaining); err != nil {
		t.Fatalf("count key row: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("key row still exists after delete")
	}
	if rec := consoleKeyDataPlaneCall(t, s, created.APIKey); rec.Code != http.StatusUnauthorized {
		t.Fatalf("deleted key = %d, want 401: %s", rec.Code, rec.Body.String())
	}

	var owner, keyID string
	if err := pg.Pool.QueryRow(context.Background(), `
SELECT owner_user_id::text, api_key_id::text
FROM gateway_request_log WHERE request_id=$1
`, requestID).Scan(&owner, &keyID); err != nil {
		t.Fatalf("the Request Record did not survive deletion: %v", err)
	}
	if owner != account.ID {
		t.Fatalf("snapshot owner = %s, want %s", owner, account.ID)
	}
	if keyID != created.ID {
		t.Fatalf("snapshot key id = %s, want %s", keyID, created.ID)
	}

	// The second delete answers 404 rather than pretending it removed a row.
	if rec := consoleKeyRequest(t, s, token, http.MethodDelete, "/api/console/keys/"+created.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("repeat delete = %d, want 404", rec.Code)
	}
}

// An edit goes through the same Tenant bound as creation: a model outside the
// allowlist is refused and the stored scope is left as it was.
func TestConsoleKeyUpdateRefusesModelOutsideTenantAllowlist(t *testing.T) {
	pg := usagePostgres(t)
	s, token, _, _, _, _ := consoleKeyFixture(t, pg, "actions-subset", []string{"campus-chat", "campus-embed"})

	created := consoleKeyCreateOwned(t, s, token, "scoped", `{"name":"scoped","allowed_models":["campus-chat"]}`)

	rec := consoleKeyRequest(t, s, token, http.MethodPut, "/api/console/keys/"+created.ID,
		`{"name":"scoped","allowed_models":["campus-chat","campus-forbidden"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("edit outside the allowlist = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	code, offending := consoleErrorCodeAndDetail(t, rec, "model")
	if code != "model_not_allowed" || offending != "campus-forbidden" {
		t.Fatalf("refusal = %q/%v, want model_not_allowed/campus-forbidden", code, offending)
	}

	_, _, name, _, models := consoleKeyDBState(t, pg, created.ID)
	if name != "scoped" || len(models) != 1 || models[0] != "campus-chat" {
		t.Fatalf("failed edit changed the row: name=%q models=%v", name, models)
	}
}

// The edit writes name, model scope, IP list and expiry, and nothing else. A
// forged tenant_id, owner_user_id or status in the body has no field to land
// in, so the row keeps its tenant, owner and state.
func TestConsoleKeyUpdateWritesOnlyEditableFields(t *testing.T) {
	pg := usagePostgres(t)
	s, token, _, account, other, tenant := consoleKeyFixture(t, pg, "actions-edit", []string{"campus-chat", "campus-embed"})
	forgedTenant := consoleKeysTenant(t, pg, "actions-edit-forged", []string{model.AllModelsWildcard})

	created := consoleKeyCreateOwned(t, s, token, "before", `{"name":"before","allowed_models":["campus-chat"]}`)
	expire := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	body := `{"name":"after","allowed_models":["*"],"allowed_ips":["10.0.0.0/8"],` +
		`"expire_at":"` + expire.Format(time.RFC3339) + `",` +
		`"tenant_id":"` + forgedTenant.ID + `","owner_user_id":"` + other.ID + `","status":2}`

	rec := consoleKeyRequest(t, s, token, http.MethodPut, "/api/console/keys/"+created.ID, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit = %d: %s", rec.Code, rec.Body.String())
	}
	var out model.APIKeyAdmin
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode edit response: %v", err)
	}
	if out.Name != "after" || out.Status != 1 {
		t.Fatalf("response name/status = %q/%d, want after/1", out.Name, out.Status)
	}
	if len(out.AllowedModels) != 2 || out.AllowedModels[0] != "campus-chat" || out.AllowedModels[1] != "campus-embed" {
		t.Fatalf("response models = %v, want the Tenant's expanded list", out.AllowedModels)
	}
	if len(out.AllowedIPs) != 1 || out.AllowedIPs[0] != "10.0.0.0/8" {
		t.Fatalf("response ips = %v", out.AllowedIPs)
	}
	if out.ExpireAt == nil || !out.ExpireAt.Equal(expire) {
		t.Fatalf("response expire_at = %v, want %s", out.ExpireAt, expire)
	}

	tenantID, ownerID, name, status, models := consoleKeyDBState(t, pg, created.ID)
	if tenantID != tenant.ID {
		t.Fatalf("tenant = %s, want the original %s", tenantID, tenant.ID)
	}
	if ownerID != account.ID {
		t.Fatalf("owner = %s, want the session account %s (the body claimed %s)", ownerID, account.ID, other.ID)
	}
	if status != 1 {
		t.Fatalf("status = %d; the edit body must not set it", status)
	}
	if name != "after" || len(models) != 2 {
		t.Fatalf("stored edit = %q/%v", name, models)
	}

	// GET is the safe summary: the editable fields are readable, the digest and
	// the plaintext are not in the projection at all.
	getRec := consoleKeyRequest(t, s, token, http.MethodGet, "/api/console/keys/"+created.ID, "")
	if getRec.Code != http.StatusOK {
		t.Fatalf("get = %d: %s", getRec.Code, getRec.Body.String())
	}
	raw := getRec.Body.String()
	if strings.Contains(raw, "key_digest") || strings.Contains(raw, `"api_key"`) {
		t.Fatalf("GET exposed credential material: %s", raw)
	}
	var got model.APIKeyAdmin
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got.ID != created.ID || got.Name != "after" {
		t.Fatalf("GET returned %s/%q, want %s/after", got.ID, got.Name, created.ID)
	}
}

// Expiry is part of the editable surface, and an explicit null clears it rather
// than leaving the previous value behind.
func TestConsoleKeyUpdateCanClearExpiry(t *testing.T) {
	pg := usagePostgres(t)
	s, token, _, _, _, _ := consoleKeyFixture(t, pg, "actions-expiry", []string{model.AllModelsWildcard})

	created := consoleKeyCreateOwned(t, s, token, "expiring",
		`{"name":"expiring","allowed_models":["*"],"expire_at":"2027-01-01T00:00:00Z"}`)
	if created.ExpireAt == nil {
		t.Fatal("fixture did not create a key with an expiry")
	}

	rec := consoleKeyRequest(t, s, token, http.MethodPut, "/api/console/keys/"+created.ID,
		`{"name":"expiring","allowed_models":["*"],"expire_at":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear expiry = %d: %s", rec.Code, rec.Body.String())
	}
	var expireAt *time.Time
	if err := pg.Pool.QueryRow(context.Background(),
		`SELECT expire_at FROM gateway_api_key WHERE id=$1::uuid`, created.ID).Scan(&expireAt); err != nil {
		t.Fatalf("read expiry: %v", err)
	}
	if expireAt != nil {
		t.Fatalf("expire_at = %v, want NULL", *expireAt)
	}
}
