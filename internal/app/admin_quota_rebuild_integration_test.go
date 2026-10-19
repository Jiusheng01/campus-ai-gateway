package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/ratelimit"
	"github.com/redis/go-redis/v9"
)

// The manual reconciliation endpoint's contract is the counters it leaves behind
// in Redis, derived from the settled request log — a property of the database
// and Redis together, so it runs against real ones and skips when either is
// absent.

// usageRedis connects to the Redis the development stack runs, or skips. It
// mirrors the ratelimit package's helper: an in-memory fake would only confirm
// that the handler and the fake agree with each other.
func usageRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("TEST_REDIS_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 2 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("no Redis at %s (%v); set TEST_REDIS_ADDR to run the quota rebuild handler test", addr, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestQuotaRebuildEndpointReconstructsTheCountersFromTheRequestLog drives the
// manual reconciliation path end to end: the handler aggregates the current
// month's settled request log and writes the day and month token counters and
// the month request counter the gate reads. The keys are spelled out as literals
// so a refactor of the key builders cannot silently change the wire format.
//
// It dispatches through handleAdmin, the same switch every other /admin route
// uses, so it also pins the registration: a path this switch stopped routing
// would answer 404 here.
func TestQuotaRebuildEndpointReconstructsTheCountersFromTheRequestLog(t *testing.T) {
	pg := usagePostgres(t)
	client := usageRedis(t)
	ctx := context.Background()

	usageClearTables(t, pg)
	// daily 1000, monthly-token 10000, monthly-request 20.
	fx := usageSeedTenantAndKey(t, pg, "rb-tenant", "k1", 1000, 10000, 20)
	usageInsertRows(t, pg, []usageRow{
		{"rb-1", usageFixture{tenantID: fx.tenantID, keyID: fx.keyID}, 300, 10, 200, false},
		{"rb-2", usageFixture{tenantID: fx.tenantID, keyID: fx.keyID}, 200, 10, 200, false},
	})

	day := time.Now().UTC().Format("20060102")
	month := time.Now().UTC().Format("200601")
	dailyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", fx.tenantID, day)
	monthlyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", fx.tenantID, month)
	reqKey := fmt.Sprintf("gw:quota:tenant:%s:requests:%s", fx.tenantID, month)
	t.Cleanup(func() { _ = client.Del(context.Background(), dailyKey, monthlyKey, reqKey).Err() })
	// A stale, inflated live counter must be reset to the log-derived value, not
	// accumulated on top of.
	if err := client.Set(ctx, dailyKey, 999999, 0).Err(); err != nil {
		t.Fatalf("seed stale counter: %v", err)
	}

	s := &Server{pg: pg, limit: ratelimit.New(client)}

	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodPost, "/admin/quota-rebuild", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		DailyTokenCounters     int `json:"daily_token_counters"`
		MonthlyTokenCounters   int `json:"monthly_token_counters"`
		MonthlyRequestCounters int `json:"monthly_request_counters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.DailyTokenCounters != 1 || body.MonthlyTokenCounters != 1 || body.MonthlyRequestCounters != 1 {
		t.Fatalf("rebuild counts = %+v, want 1/1/1 (one tenant, one settled day)", body)
	}

	// The counters the gate reads now hold the settled values: 500 tokens on the
	// day and in the month, two requests in the month. The stale 999999 is gone.
	assertCounter(t, client, dailyKey, 500)
	assertCounter(t, client, monthlyKey, 500)
	assertCounter(t, client, reqKey, 2)
}

// TestQuotaRebuildEndpointRejectsNonPost covers the guard: the endpoint changes
// state, so it answers POST only. It reaches the handler through handleAdmin, so
// the 405 also proves the route is dispatched rather than the method refused
// before routing.
func TestQuotaRebuildEndpointRejectsNonPost(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleAdmin(rec, httptest.NewRequest(http.MethodGet, "/admin/quota-rebuild", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

func assertCounter(t *testing.T, client *redis.Client, key string, want int64) {
	t.Helper()
	got, err := client.Get(context.Background(), key).Int64()
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	if got != want {
		t.Fatalf("%s = %d, want %d", key, got, want)
	}
}
