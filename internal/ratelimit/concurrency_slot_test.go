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

// Exactly the limit, never more. A two-phase check-then-reserve would admit
// every goroutine that read a count below the limit; the single Lua script is
// what makes the count and the reservation one step.
func TestAcquireConcurrencySlotAdmitsExactlyTheLimit(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()
	tenant := uniqueTenantID()
	key := concurrencySlotKey(tenant)
	t.Cleanup(func() { _ = client.Del(context.Background(), key).Err() })

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
	if n, err := client.ZCard(ctx, key).Result(); err != nil || n != limit {
		t.Fatalf("slots held = %d (err %v), want %d", n, err, limit)
	}

	for _, r := range releases {
		r()
	}
	if n, err := client.Exists(ctx, key).Result(); err != nil || n != 0 {
		t.Fatalf("releasing every slot must empty the set, but it still exists (err %v)", err)
	}
}

// A slot whose holder never releases it — a crashed process, a wedged request —
// must not pin a slot forever. Aging the stored deadline past now stands in for
// waiting out the lease.
func TestAcquireConcurrencySlotReclaimsAnExpiredSlot(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()
	tenant := uniqueTenantID()
	key := concurrencySlotKey(tenant)
	t.Cleanup(func() { _ = client.Del(context.Background(), key).Err() })

	limiter := New(client)
	if _, ok, err := limiter.AcquireConcurrencySlot(ctx, tenant, 1); err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := limiter.AcquireConcurrencySlot(ctx, tenant, 1); ok {
		t.Fatal("a second acquire must be denied while the slot is held")
	}

	members, err := client.ZRange(ctx, key, 0, -1).Result()
	if err != nil || len(members) != 1 {
		t.Fatalf("expected exactly one held member, got %v (err %v)", members, err)
	}
	past := float64(time.Now().Add(-time.Minute).UnixMilli())
	if err := client.ZAdd(ctx, key, redis.Z{Score: past, Member: members[0]}).Err(); err != nil {
		t.Fatal(err)
	}

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
	key := concurrencySlotKey(tenant)
	t.Cleanup(func() { _ = client.Del(context.Background(), key).Err() })

	limiter := New(client)
	release, ok, err := limiter.AcquireConcurrencySlot(ctx, tenant, 1)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	release()
	release()

	if n, err := client.ZCard(ctx, key).Result(); err != nil || n != 0 {
		t.Fatalf("slots held = %d (err %v), want 0 after release", n, err)
	}
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
