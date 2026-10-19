package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// uniqueTenantID gives every run its own key space so repeated or parallel runs
// cannot interfere with each other.
func uniqueTenantID() string { return uuid.NewString() }

// cleanupTenantKeys removes every key this tenant's limits may have written. It
// deletes by the tenant id embedded in the keys rather than by an exact key
// name, so the test does not pin the key format.
func cleanupTenantKeys(t *testing.T, client *redis.Client, tenant string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		keys, err := client.Keys(ctx, "gw:*"+tenant+"*").Result()
		if err != nil || len(keys) == 0 {
			return
		}
		_ = client.Del(ctx, keys...).Err()
	})
}

// Exactly the limit, never more. A two-phase check-then-reserve would admit
// every goroutine that read a count below the limit; the single Lua script is
// what makes the count and the reservation one step.
func TestAcquireConcurrencySlotAdmitsExactlyTheLimit(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()
	tenant := uniqueTenantID()
	cleanupTenantKeys(t, client, tenant)

	limiter := New(client)
	const (
		limit       = 5
		concurrency = 40
	)

	start := make(chan struct{})
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		allowed  int
		denied   int
		releases []func()
		errs     []error
	)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, ok, err := limiter.AcquireConcurrencySlot(ctx, tenant, limit)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				errs = append(errs, err)
			case ok:
				allowed++
				releases = append(releases, release)
			default:
				denied++
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if allowed != limit {
		t.Fatalf("allowed = %d, want exactly %d", allowed, limit)
	}
	if denied != concurrency-limit {
		t.Fatalf("denied = %d, want %d", denied, concurrency-limit)
	}

	// Releasing every slot must empty the pool: a fresh limit's worth is
	// admitted again. If any slot had leaked, the later acquires would be denied
	// once the pool had already reached the limit.
	for _, r := range releases {
		r()
	}
	var reacquired int
	for i := 0; i < limit; i++ {
		_, ok, err := limiter.AcquireConcurrencySlot(ctx, tenant, limit)
		if err != nil {
			t.Fatalf("re-acquire after release: %v", err)
		}
		if ok {
			reacquired++
		}
	}
	if reacquired != limit {
		t.Fatalf("after releasing every slot only %d of %d could be re-acquired", reacquired, limit)
	}
}

// A slot whose holder never releases it — a crashed process, a wedged request —
// must not pin a slot forever. A short lease stands in for waiting out the
// production one.
func TestAcquireConcurrencySlotReclaimsAnExpiredSlot(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()
	tenant := uniqueTenantID()
	cleanupTenantKeys(t, client, tenant)

	limiter := New(client).WithConcurrencyLease(50 * time.Millisecond)
	if _, ok, err := limiter.AcquireConcurrencySlot(ctx, tenant, 1); err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := limiter.AcquireConcurrencySlot(ctx, tenant, 1); ok {
		t.Fatal("a second acquire must be denied while the slot is held")
	}

	time.Sleep(80 * time.Millisecond)

	if _, ok, err := limiter.AcquireConcurrencySlot(ctx, tenant, 1); err != nil || !ok {
		t.Fatalf("the expired slot must be reclaimed, got ok=%v err=%v", ok, err)
	}
}

// Releasing twice — or releasing after the slot was already reaped — must be a
// no-op, not a decrement that drives the count below zero and lets a request
// through for free.
func TestReleaseConcurrencySlotIsIdempotent(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()
	tenant := uniqueTenantID()
	cleanupTenantKeys(t, client, tenant)

	limiter := New(client)
	release, ok, err := limiter.AcquireConcurrencySlot(ctx, tenant, 1)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	release()
	release()

	if _, ok, err := limiter.AcquireConcurrencySlot(ctx, tenant, 1); err != nil || !ok {
		t.Fatalf("the slot must be free again, got ok=%v err=%v", ok, err)
	}
}

func TestAcquireConcurrencySlotZeroLimitIsUnlimited(t *testing.T) {
	client := redisTestClient(t)
	limiter := New(client)
	release, ok, err := limiter.AcquireConcurrencySlot(context.Background(), uniqueTenantID(), 0)
	if err != nil || !ok {
		t.Fatalf("a zero limit means unlimited: ok=%v err=%v", ok, err)
	}
	release()
}

// An unreachable Redis must refuse the request, not admit it: admitting on a
// shared-state failure would turn an outage into unbounded upstream load
// (fail-closed, ADR-0007).
func TestAcquireConcurrencySlotRefusesWhenRedisIsUnreachable(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })

	limiter := New(client)
	_, ok, err := limiter.AcquireConcurrencySlot(context.Background(), uniqueTenantID(), 5)
	if err == nil {
		t.Fatal("an unreachable Redis must return an error")
	}
	if ok {
		t.Fatal("an unreachable Redis must not admit the request")
	}
}

// The Tenant is the rate-limit boundary (ADR-0015): every limit is counted
// against the tenant id, so two keys of one tenant share one budget while
// another tenant is untouched. The functions take a tenant id, not a key id,
// which is exactly the property under test.
func TestRateLimitsAreSharedPerTenantNotPerKey(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()
	limiter := New(client)

	tenantA, tenantB := uniqueTenantID(), uniqueTenantID()
	cleanupTenantKeys(t, client, tenantA)
	cleanupTenantKeys(t, client, tenantB)

	// RPM: the first request takes the tenant's only slot; a second request for
	// the same tenant is denied, while the other tenant is unaffected.
	if ok, err := limiter.AllowRPM(ctx, tenantA, 1); err != nil || !ok {
		t.Fatalf("first RPM for tenant A: ok=%v err=%v", ok, err)
	}
	if ok, err := limiter.AllowRPM(ctx, tenantA, 1); err != nil || ok {
		t.Fatalf("a second RPM for tenant A must be denied against the shared limit: ok=%v err=%v", ok, err)
	}
	if ok, err := limiter.AllowRPM(ctx, tenantB, 1); err != nil || !ok {
		t.Fatalf("tenant B must be unaffected by tenant A's budget: ok=%v err=%v", ok, err)
	}

	// TPM: same shape — 500 then 600 exceeds the shared 1000.
	if _, ok, err := limiter.ReserveTPM(ctx, tenantA, 500, 1000); err != nil || !ok {
		t.Fatalf("first TPM for tenant A: ok=%v err=%v", ok, err)
	}
	if _, ok, err := limiter.ReserveTPM(ctx, tenantA, 600, 1000); err != nil || ok {
		t.Fatalf("a second TPM for tenant A must be denied against the shared limit: ok=%v err=%v", ok, err)
	}
	if _, ok, err := limiter.ReserveTPM(ctx, tenantB, 900, 1000); err != nil || !ok {
		t.Fatalf("tenant B must be unaffected: ok=%v err=%v", ok, err)
	}

	// Concurrency: one tenant, one shared pool.
	releaseA, ok, err := limiter.AcquireConcurrencySlot(ctx, tenantA, 1)
	if err != nil || !ok {
		t.Fatalf("first slot for tenant A: ok=%v err=%v", ok, err)
	}
	defer releaseA()
	if _, ok, _ := limiter.AcquireConcurrencySlot(ctx, tenantA, 1); ok {
		t.Fatal("a second slot for tenant A must be denied against the shared pool")
	}
	releaseB, ok, err := limiter.AcquireConcurrencySlot(ctx, tenantB, 1)
	if err != nil || !ok {
		t.Fatalf("tenant B must get its own slot: ok=%v err=%v", ok, err)
	}
	releaseB()
}
