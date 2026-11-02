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
	"github.com/Jiusheng01/campus-ai-gateway/internal/ratelimit"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const ownerSnapshotHMACSecret = "owner-snapshot-test-hmac-secret"

// ownerSnapshotUpstream answers a minimal OpenAI chat completion. The response
// shape is what the data plane parses into Usage, so the Request Record is
// written exactly as it would be in production.
func ownerSnapshotUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-owner-snapshot",
			"object":  "chat.completion",
			"model":   "upstream-model",
			"choices": []map[string]any{{"index": 0, "message": map[string]string{"role": "assistant", "content": "ok"}}},
			"usage":   map[string]int{"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7},
		})
	}))
}

// ownerSnapshotServer is the data plane wired to the real database and Redis,
// with a warm route table pointing at the test upstream. It is built by hand
// for the same reason newDirectTestServer is: the seam under test is the HTTP
// data plane, not startup configuration.
func ownerSnapshotServer(t *testing.T, pg *storage.Postgres, client *redis.Client, baseURL string) *Server {
	t.Helper()
	s := &Server{
		cfg: Config{
			DataPlane:             DataPlaneDirect,
			ProviderHTTPAllowlist: []string{"127.0.0.1"},
			KeyHMACSecret:         ownerSnapshotHMACSecret,
		},
		pg:    pg,
		limit: ratelimit.New(client),
		routeCache: routeTable{models: map[string][]directRoute{
			"owner-model": {{
				Provider:      "owner-provider",
				BaseURL:       baseURL + "/v1",
				UpstreamModel: "upstream-model",
				CredentialEnv: "OWNER_SNAPSHOT_UPSTREAM_KEY",
				Priority:      1,
			}},
		}},
		routeAt:   time.Now(),
		transport: &transportPool{},
		selector:  newRouteSelector(),
		breaker:   newCircuitBreaker(),
		egress:    permissiveEgressPolicy(),
	}
	s.upstream = http.HandlerFunc(s.serveDirect)
	return s
}

func ownerSnapshotRequest(t *testing.T, s *Server, plainKey, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"owner-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("X-Request-Id", requestID)
	rec := httptest.NewRecorder()
	s.authMiddleware(http.HandlerFunc(s.handleV1)).ServeHTTP(rec, req)
	return rec
}

func ownerSnapshotRow(t *testing.T, pg *storage.Postgres, requestID string) (owner, keyID, tenantID *string, status int) {
	t.Helper()
	err := pg.Pool.QueryRow(context.Background(), `
SELECT owner_user_id::text, api_key_id::text, tenant_id::text, COALESCE(status_code, 0)
FROM gateway_request_log WHERE request_id=$1`, requestID).
		Scan(&owner, &keyID, &tenantID, &status)
	if err != nil {
		t.Fatalf("read Request Record %s: %v", requestID, err)
	}
	return owner, keyID, tenantID, status
}

func seedOwnerSnapshotKey(t *testing.T, pg *storage.Postgres, tenantID, name, plain string) model.APIKeyAdmin {
	t.Helper()
	key, err := pg.CreateAPIKey(context.Background(), model.APIKeyAdmin{
		TenantID:      tenantID,
		Name:          name,
		KeyPrefix:     "sk-owner",
		Status:        1,
		AllowedModels: []string{model.AllModelsWildcard},
	}, security.Digest(ownerSnapshotHMACSecret, plain))
	if err != nil {
		t.Fatalf("create key %s: %v", name, err)
	}
	return key
}

// The snapshot is the only new write the data plane performs. A key that has an
// owner must have that owner copied onto the Request Record the request
// produces, without changing whether the request is admitted or routed.
func TestDataPlaneSnapshotsTheKeysOwnerIntoTheRequestLog(t *testing.T) {
	pg := usagePostgres(t)
	client := usageRedis(t)
	t.Setenv("OWNER_SNAPSHOT_UPSTREAM_KEY", "sk-upstream")
	usageClearTables(t, pg)

	tenant := usageSeedTenant(t, pg, "snapshot-owned-tenant", 0, 0, 0)
	account, err := pg.CreateUser(context.Background(),
		"owner-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:8], "Owner", "hash", []string{"member"}, false)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	key := seedOwnerSnapshotKey(t, pg, tenant.ID, "owned-key", "sk-owned-plain")
	if _, err := pg.Pool.Exec(context.Background(),
		`UPDATE gateway_api_key SET owner_user_id=$1::uuid WHERE id=$2::uuid`,
		account.ID, key.ID); err != nil {
		t.Fatalf("assign owner: %v", err)
	}

	upstream := ownerSnapshotUpstream(t)
	defer upstream.Close()
	s := ownerSnapshotServer(t, pg, client, upstream.URL)

	rec := ownerSnapshotRequest(t, s, "sk-owned-plain", "req_owner_snapshot_owned")
	if rec.Code != http.StatusOK {
		t.Fatalf("request status = %d, body %s", rec.Code, rec.Body.String())
	}

	owner, keyID, tenantID, status := ownerSnapshotRow(t, pg, "req_owner_snapshot_owned")
	if owner == nil || *owner != account.ID {
		t.Fatalf("snapshot owner = %v, want %s", owner, account.ID)
	}
	if keyID == nil || *keyID != key.ID {
		t.Fatalf("snapshot key = %v, want %s", keyID, key.ID)
	}
	if tenantID == nil || *tenantID != tenant.ID {
		t.Fatalf("snapshot tenant = %v, want %s", tenantID, tenant.ID)
	}
	if status != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200", status)
	}
}

// An existing key with no owner is the upgrade case: it must keep authenticating
// and routing exactly as before, and its Request Record must carry NULL rather
// than a zero UUID or an empty string.
func TestUnownedKeyStillRoutesAndLogsANullOwnerSnapshot(t *testing.T) {
	pg := usagePostgres(t)
	client := usageRedis(t)
	t.Setenv("OWNER_SNAPSHOT_UPSTREAM_KEY", "sk-upstream")
	usageClearTables(t, pg)

	tenant := usageSeedTenant(t, pg, "snapshot-unowned-tenant", 0, 0, 0)
	key := seedOwnerSnapshotKey(t, pg, tenant.ID, "unowned-key", "sk-unowned-plain")

	var ownerBefore *string
	if err := pg.Pool.QueryRow(context.Background(),
		`SELECT owner_user_id::text FROM gateway_api_key WHERE id=$1::uuid`, key.ID).Scan(&ownerBefore); err != nil {
		t.Fatalf("seed key owner: %v", err)
	}
	if ownerBefore != nil {
		t.Fatalf("fixture must start with no owner, got %v", *ownerBefore)
	}

	upstream := ownerSnapshotUpstream(t)
	defer upstream.Close()
	s := ownerSnapshotServer(t, pg, client, upstream.URL)

	rec := ownerSnapshotRequest(t, s, "sk-unowned-plain", "req_owner_snapshot_unowned")
	if rec.Code != http.StatusOK {
		t.Fatalf("an unowned key must still route: status = %d, body %s", rec.Code, rec.Body.String())
	}

	owner, keyID, tenantID, status := ownerSnapshotRow(t, pg, "req_owner_snapshot_unowned")
	if owner != nil {
		t.Fatalf("an unowned key produced a non-NULL snapshot owner: %v", *owner)
	}
	if keyID == nil || *keyID != key.ID || tenantID == nil || *tenantID != tenant.ID {
		t.Fatalf("snapshot key/tenant = %v/%v, want %s/%s", keyID, tenantID, key.ID, tenant.ID)
	}
	if status != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200", status)
	}
}
