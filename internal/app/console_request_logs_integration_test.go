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
)

// These tests exercise the personal Request Record endpoints at the HTTP seam
// against a migrated Postgres. The rules under test — the owner predicate, the
// snapshots that outlive a deleted key, and the fact that filters can only
// narrow — are properties of the SQL, so an in-memory fake could only confirm
// that it agreed with itself.

type consoleLogRow struct {
	requestID string
	tenantID  string
	keyID     string
	ownerID   string // empty means the pre-migration NULL snapshot
	clientIP  string
	requested string
	actual    string
	status    int
	tokens    int64
	latency   int64
	failover  bool
	createdAt time.Time
}

func insertConsoleLog(t *testing.T, pg *storage.Postgres, r consoleLogRow) {
	t.Helper()
	clientIP := r.clientIP
	if clientIP == "" {
		clientIP = "203.0.113.77"
	}
	_, err := pg.Pool.Exec(context.Background(), `
INSERT INTO gateway_request_log(
	request_id, tenant_id, api_key_id, owner_user_id, client_ip,
	requested_model, actual_model, provider, stream,
	input_tokens, output_tokens, total_tokens, latency_ms, status_code,
	failover, created_at
) VALUES (
	$1, $2::uuid, $3::uuid, NULLIF($4,'')::uuid, $5,
	$6, $7, 'provider-secret', false,
	$8, 0, $8, $9, $10, $11, $12
)`,
		r.requestID, r.tenantID, r.keyID, r.ownerID, clientIP,
		r.requested, r.actual, r.tokens, r.latency, r.status, r.failover, r.createdAt)
	if err != nil {
		t.Fatalf("insert request record %s: %v", r.requestID, err)
	}
}

func consoleLogsCall(t *testing.T, s *Server, token, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local"+path, nil)
	req.Header.Set("X-Request-Id", "console-logs-test")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

type consoleLogListBody struct {
	Data   []model.ConsoleRequestLog `json:"data"`
	Limit  int                       `json:"limit"`
	Offset int                       `json:"offset"`
}

func decodeConsoleLogList(t *testing.T, rec *httptest.ResponseRecorder) consoleLogListBody {
	t.Helper()
	var out consoleLogListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list response %q: %v", rec.Body.String(), err)
	}
	return out
}

func decodeConsoleLogDetail(t *testing.T, rec *httptest.ResponseRecorder) model.ConsoleRequestLogDetail {
	t.Helper()
	var envelope struct {
		Data model.ConsoleRequestLogDetail `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode detail response %q: %v", rec.Body.String(), err)
	}
	return envelope.Data
}

type consoleLogsFixture struct {
	server *Server
	alice  model.AdminUser
	bob    model.AdminUser
	aliceT string
	bobT   string
	// tenantA/tenantB and keyA/keyB are the identity filters an attacker might
	// try to use to reach across the owner boundary.
	tenantA, tenantB string
	keyA, keyB       string
}

func seedConsoleLogs(t *testing.T, pg *storage.Postgres) consoleLogsFixture {
	t.Helper()
	tenantAssignmentClear(t, pg)
	s := consoleKeysServer(t, pg, 10)

	tenantA := consoleKeysTenant(t, pg, "logs-a", []string{model.AllModelsWildcard})
	tenantB := consoleKeysTenant(t, pg, "logs-b", []string{model.AllModelsWildcard})
	alice := tenantAssignmentAccount(t, pg, uniqueAccountName("logs-alice"), []string{RoleMember})
	bob := tenantAssignmentAccount(t, pg, uniqueAccountName("logs-bob"), []string{RoleMember})
	consoleKeysAssign(t, pg, alice.ID, tenantA.ID)
	consoleKeysAssign(t, pg, bob.ID, tenantB.ID)

	// Keys are created through the personal endpoint, so the owner comes from
	// the session exactly as it does in production.
	aliceKey := decodeConsoleKeyCreate(t,
		consoleKeysCall(t, s, tenantAssignmentToken(t, s, alice.Username), http.MethodPost,
			`{"name":"alice-key","allowed_models":["*"]}`))
	bobKey := decodeConsoleKeyCreate(t,
		consoleKeysCall(t, s, tenantAssignmentToken(t, s, bob.Username), http.MethodPost,
			`{"name":"bob-key","allowed_models":["*"]}`))

	return consoleLogsFixture{
		server:  s,
		alice:   alice,
		bob:     bob,
		aliceT:  tenantAssignmentToken(t, s, alice.Username),
		bobT:    tenantAssignmentToken(t, s, bob.Username),
		tenantA: tenantA.ID,
		tenantB: tenantB.ID,
		keyA:    aliceKey.ID,
		keyB:    bobKey.ID,
	}
}

// A member's list is exactly the rows whose owner snapshot is the session
// account. Another Account's rows and pre-migration rows with no owner are not
// selected at all, and the projection carries none of the admin-only fields.
func TestConsoleRequestLogsListOnlyTheSessionsRecords(t *testing.T) {
	pg := usagePostgres(t)
	fx := seedConsoleLogs(t, pg)

	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	insertConsoleLog(t, pg, consoleLogRow{
		requestID: "req-alice-1", tenantID: fx.tenantA, keyID: fx.keyA, ownerID: fx.alice.ID,
		requested: "campus-chat", actual: "campus-chat", status: 200, tokens: 100, latency: 120, createdAt: base,
	})
	insertConsoleLog(t, pg, consoleLogRow{
		requestID: "req-alice-2", tenantID: fx.tenantA, keyID: fx.keyA, ownerID: fx.alice.ID,
		requested: "campus-embed", actual: "campus-embed", status: 500, tokens: 50, latency: 900, createdAt: base.Add(time.Minute),
	})
	insertConsoleLog(t, pg, consoleLogRow{
		requestID: "req-bob-1", tenantID: fx.tenantB, keyID: fx.keyB, ownerID: fx.bob.ID,
		requested: "campus-chat", actual: "campus-chat", status: 200, tokens: 999, latency: 10, createdAt: base.Add(2 * time.Minute),
	})
	// An administrator-created key, and therefore a pre-owner row: invisible here.
	insertConsoleLog(t, pg, consoleLogRow{
		requestID: "req-unowned-1", tenantID: fx.tenantA, keyID: fx.keyA,
		requested: "campus-chat", actual: "campus-chat", status: 200, tokens: 7, latency: 11, createdAt: base.Add(3 * time.Minute),
	})

	rec := consoleLogsCall(t, fx.server, fx.aliceT, "/api/console/request-logs")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeConsoleLogList(t, rec)
	if len(body.Data) != 2 {
		t.Fatalf("alice sees %d records, want her 2: %s", len(body.Data), rec.Body.String())
	}
	// Newest first.
	if body.Data[0].RequestID != "req-alice-2" || body.Data[1].RequestID != "req-alice-1" {
		t.Fatalf("order = %q, %q, want newest first", body.Data[0].RequestID, body.Data[1].RequestID)
	}
	for _, row := range body.Data {
		if row.RequestID == "req-bob-1" || row.RequestID == "req-unowned-1" {
			t.Fatalf("list leaked %s", row.RequestID)
		}
	}

	// The projection is narrower than the admin one: no client IP, tenant,
	// provider, upstream model or route priority on the wire.
	raw := rec.Body.String()
	for _, forbidden := range []string{`"client_ip"`, `"tenant_id"`, `"provider"`, `"upstream_model"`, `"route_priority"`, "203.0.113.77", "provider-secret"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("personal list exposed %q: %s", forbidden, raw)
		}
	}
	// The identifier the key filter uses is present and is the caller's key.
	if body.Data[0].APIKeyID != fx.keyA {
		t.Fatalf("api_key_id = %q, want %q", body.Data[0].APIKeyID, fx.keyA)
	}
}

// Every filter narrows. None of them — including another owner's key or tenant
// id supplied in the query string — can widen the session's set.
func TestConsoleRequestLogFiltersNarrowAndNeverWiden(t *testing.T) {
	pg := usagePostgres(t)
	fx := seedConsoleLogs(t, pg)

	base := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	insertConsoleLog(t, pg, consoleLogRow{
		requestID: "req-chat-ok", tenantID: fx.tenantA, keyID: fx.keyA, ownerID: fx.alice.ID,
		requested: "campus-chat", actual: "campus-chat-upstream", status: 200, tokens: 10, latency: 100, createdAt: base,
	})
	insertConsoleLog(t, pg, consoleLogRow{
		requestID: "req-embed-fail", tenantID: fx.tenantA, keyID: fx.keyA, ownerID: fx.alice.ID,
		requested: "campus-embed", actual: "campus-embed", status: 500, tokens: 20, latency: 200, createdAt: base.Add(time.Hour),
	})
	insertConsoleLog(t, pg, consoleLogRow{
		requestID: "req-bob", tenantID: fx.tenantB, keyID: fx.keyB, ownerID: fx.bob.ID,
		requested: "campus-chat", actual: "campus-chat", status: 200, tokens: 30, latency: 300, createdAt: base.Add(2 * time.Hour),
	})

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"requested model", "?model=campus-chat", []string{"req-chat-ok"}},
		{"actual model", "?model=campus-chat-upstream", []string{"req-chat-ok"}},
		{"status", "?status=500", []string{"req-embed-fail"}},
		{"time range", "?from=" + base.Add(20*time.Minute).Format(time.RFC3339) + "&to=" + base.Add(90*time.Minute).Format(time.RFC3339), []string{"req-embed-fail"}},
		{"key", "?api_key_id=" + fx.keyA, []string{"req-embed-fail", "req-chat-ok"}},
		{"combined", "?model=campus-embed&status=500", []string{"req-embed-fail"}},
		// No match rather than another owner's row.
		{"another owner's key", "?api_key_id=" + fx.keyB, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := consoleLogsCall(t, fx.server, fx.aliceT, "/api/console/request-logs"+tc.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("list %q = %d: %s", tc.query, rec.Code, rec.Body.String())
			}
			body := decodeConsoleLogList(t, rec)
			got := make([]string, 0, len(body.Data))
			for _, row := range body.Data {
				got = append(got, row.RequestID)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("filter %q returned %v, want %v", tc.query, got, tc.want)
			}
		})
	}

	// A client-supplied owner or tenant is ignored rather than trusted: the
	// session's owner predicate still applies, so Bob's ids return only Alice's
	// rows (all of them, because neither id is a narrowing condition).
	rec := consoleLogsCall(t, fx.server, fx.aliceT,
		"/api/console/request-logs?owner_user_id="+fx.bob.ID+"&tenant_id="+fx.tenantB)
	if rec.Code != http.StatusOK {
		t.Fatalf("forged filters = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeConsoleLogList(t, rec)
	if len(body.Data) != 2 {
		t.Fatalf("forged owner/tenant changed the result: %s", rec.Body.String())
	}
	for _, row := range body.Data {
		if row.RequestID == "req-bob" {
			t.Fatal("a forged owner predicate returned another account's row")
		}
	}
}

// An owner opens their record and sees its attempt chain; another account's
// record — and a record that does not exist — are both 404.
func TestConsoleRequestLogDetailIsOwnerScopedAndCarriesTheAttemptChain(t *testing.T) {
	pg := usagePostgres(t)
	fx := seedConsoleLogs(t, pg)

	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	insertConsoleLog(t, pg, consoleLogRow{
		requestID: "req-detail-alice", tenantID: fx.tenantA, keyID: fx.keyA, ownerID: fx.alice.ID,
		requested: "campus-chat", actual: "campus-chat-v2", status: 200, tokens: 42, latency: 321, createdAt: base,
	})
	insertConsoleLog(t, pg, consoleLogRow{
		requestID: "req-detail-bob", tenantID: fx.tenantB, keyID: fx.keyB, ownerID: fx.bob.ID,
		requested: "campus-chat", actual: "campus-chat", status: 200, tokens: 1, latency: 1, createdAt: base,
	})
	if err := pg.InsertRequestAttempts(context.Background(), "req-detail-alice", []model.RequestAttempt{
		{AttemptNo: 1, Provider: "primary", UpstreamModel: "upstream-a", Outcome: model.AttemptFailed, StatusCode: 502, LatencyMS: 200, ErrorKind: "upstream_5xx"},
		{AttemptNo: 2, Provider: "backup", UpstreamModel: "upstream-b", Outcome: model.AttemptServed, StatusCode: 200, LatencyMS: 121},
	}); err != nil {
		t.Fatalf("seed attempts: %v", err)
	}

	rec := consoleLogsCall(t, fx.server, fx.aliceT, "/api/console/request-logs/req-detail-alice")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail = %d: %s", rec.Code, rec.Body.String())
	}
	detail := decodeConsoleLogDetail(t, rec)
	if detail.RequestID != "req-detail-alice" || detail.ActualModel != "campus-chat-v2" || detail.TotalTokens != 42 {
		t.Fatalf("detail = %+v", detail.ConsoleRequestLog)
	}
	if len(detail.AttemptChain) != 2 {
		t.Fatalf("attempt chain = %+v, want 2 entries", detail.AttemptChain)
	}
	if detail.AttemptChain[0].Provider != "primary" || detail.AttemptChain[0].Outcome != model.AttemptFailed ||
		detail.AttemptChain[1].Provider != "backup" || detail.AttemptChain[1].Outcome != model.AttemptServed {
		t.Fatalf("attempt chain = %+v", detail.AttemptChain)
	}
	raw := rec.Body.String()
	for _, forbidden := range []string{`"client_ip"`, `"tenant_id"`, `"owner_user_id"`, "203.0.113.77"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("personal detail exposed %q: %s", forbidden, raw)
		}
	}

	// Bob's record answers exactly like a missing one.
	for _, target := range []string{"/api/console/request-logs/req-detail-bob", "/api/console/request-logs/req-does-not-exist"} {
		rec := consoleLogsCall(t, fx.server, fx.aliceT, target)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s as alice = %d, want 404: %s", target, rec.Code, rec.Body.String())
		}
		if code := errorCode(t, rec); code != "not_found" {
			t.Fatalf("GET %s error code = %q, want not_found", target, code)
		}
	}
}

// Deleting a key removes the credential row, not the audit trail: the owner
// snapshot keeps the records visible to their owner.
func TestConsoleRequestLogsSurviveKeyDeletion(t *testing.T) {
	pg := usagePostgres(t)
	fx := seedConsoleLogs(t, pg)

	insertConsoleLog(t, pg, consoleLogRow{
		requestID: "req-deleted-key", tenantID: fx.tenantA, keyID: fx.keyA, ownerID: fx.alice.ID,
		requested: "campus-chat", actual: "campus-chat", status: 200, tokens: 5, latency: 50,
		createdAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Second),
	})
	if err := pg.DeleteAPIKey(context.Background(), fx.keyA); err != nil {
		t.Fatalf("delete key: %v", err)
	}

	rec := consoleLogsCall(t, fx.server, fx.aliceT, "/api/console/request-logs")
	if rec.Code != http.StatusOK {
		t.Fatalf("list after delete = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeConsoleLogList(t, rec)
	if len(body.Data) != 1 || body.Data[0].RequestID != "req-deleted-key" {
		t.Fatalf("records after key deletion = %+v", body.Data)
	}
	if body.Data[0].APIKeyID != fx.keyA {
		t.Fatalf("api_key_id = %q, want the historical id %q", body.Data[0].APIKeyID, fx.keyA)
	}

	rec = consoleLogsCall(t, fx.server, fx.aliceT, "/api/console/request-logs/req-deleted-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail after delete = %d: %s", rec.Code, rec.Body.String())
	}
}

// Pagination is limit/offset over the owner's set, and the page boundaries do
// not repeat or skip a record.
func TestConsoleRequestLogsPaginateWithinTheOwnersSet(t *testing.T) {
	pg := usagePostgres(t)
	fx := seedConsoleLogs(t, pg)

	base := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	for i, id := range []string{"req-page-1", "req-page-2", "req-page-3"} {
		insertConsoleLog(t, pg, consoleLogRow{
			requestID: id, tenantID: fx.tenantA, keyID: fx.keyA, ownerID: fx.alice.ID,
			requested: "campus-chat", actual: "campus-chat", status: 200, tokens: int64(i + 1), latency: 10,
			createdAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	first := decodeConsoleLogList(t, consoleLogsCall(t, fx.server, fx.aliceT, "/api/console/request-logs?limit=2&offset=0"))
	if len(first.Data) != 2 || first.Data[0].RequestID != "req-page-3" || first.Data[1].RequestID != "req-page-2" {
		t.Fatalf("first page = %+v", first.Data)
	}
	if first.Limit != 2 || first.Offset != 0 {
		t.Fatalf("page echo = limit %d offset %d", first.Limit, first.Offset)
	}
	second := decodeConsoleLogList(t, consoleLogsCall(t, fx.server, fx.aliceT, "/api/console/request-logs?limit=2&offset=2"))
	if len(second.Data) != 1 || second.Data[0].RequestID != "req-page-1" {
		t.Fatalf("second page = %+v", second.Data)
	}
}

// A malformed filter is a refusal, never a silently dropped condition.
func TestConsoleRequestLogsRefuseMalformedFilters(t *testing.T) {
	pg := usagePostgres(t)
	fx := seedConsoleLogs(t, pg)

	for _, query := range []string{
		"?from=yesterday",
		"?to=2026-13-01T00:00:00Z",
		"?api_key_id=not-a-uuid",
		"?limit=abc",
		"?limit=0",
		"?offset=-1",
		"?status=two-hundred",
	} {
		rec := consoleLogsCall(t, fx.server, fx.aliceT, "/api/console/request-logs"+query)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s = %d, want 400: %s", query, rec.Code, rec.Body.String())
		}
		if code := errorCode(t, rec); code != "invalid_filter" {
			t.Fatalf("GET %s error code = %q, want invalid_filter", query, code)
		}
	}

	rec := consoleLogsCall(t, fx.server, "", "/api/console/request-logs")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}
