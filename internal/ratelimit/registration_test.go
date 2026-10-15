package ratelimit

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// --- fake client, for the shapes that do not need a server ------------------

// The atomicity of the limiter is a property of the Lua script and the Redis
// server, so it is proved against a real server below. These unit tests cover
// the decisions the Go side makes around the script's reply.

func TestConsumeRegistrationDisabledPolicySkipsRedis(t *testing.T) {
	// maxAttempts = 0 means "no registration throttle". The limiter must not
	// reach for Redis at all in that configuration: a deployment that turned the
	// throttle off should not start failing registrations when the cache is down.
	limiter := NewRegistrationLimiter(nil).WithPolicy(0, time.Hour)
	for i := 0; i < 10; i++ {
		allowed, retryAfter, err := limiter.ConsumeRegistration(context.Background(), "203.0.113.7")
		if !allowed || retryAfter != 0 || err != nil {
			t.Fatalf("attempt %d: got allowed=%v retryAfter=%v err=%v", i, allowed, retryAfter, err)
		}
	}
}

func TestParseCounterReply(t *testing.T) {
	if _, _, err := parseCounterReply("not an array"); err == nil {
		t.Fatal("a malformed reply must be an error, not an implicit allow")
	}
	if _, _, err := parseCounterReply([]any{int64(1)}); err == nil {
		t.Fatal("a short reply must be an error")
	}
	if _, _, err := parseCounterReply([]any{"1", "60"}); err == nil {
		t.Fatal("a reply with the wrong element types must be an error")
	}
	current, ttl, err := parseCounterReply([]any{int64(4), int64(120)})
	if err != nil || current != 4 || ttl != 120 {
		t.Fatalf("got %d/%d err=%v", current, ttl, err)
	}
}

// --- real Redis ------------------------------------------------------------

// redisTestClient connects to the Redis the development stack runs, or skips.
//
// The atomicity of INCR + TTL + decision cannot be demonstrated without a real
// server: an in-memory fake decides in Go, where the interleaving the Lua script
// exists to prevent is not even expressible. TEST_REDIS_ADDR overrides the
// address; the test is skipped when nothing answers.
func redisTestClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 2 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("no Redis at %s (%v); set TEST_REDIS_ADDR to run the atomicity test", addr, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// uniqueTestIP gives every run its own counter so parallel or repeated runs
// cannot interfere with each other.
func uniqueTestIP() string {
	return fmt.Sprintf("198.51.100.%d", 1+rand.Intn(254))
}

func TestRegistrationLimiterAtomicLimitAgainstRedis(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()

	const (
		maxAttempts = 5
		concurrency = 50
	)
	ip := uniqueTestIP()
	t.Cleanup(func() { _ = client.Del(context.Background(), registrationKey(ip)).Err() })

	limiter := NewRegistrationLimiter(client).WithPolicy(maxAttempts, time.Hour)

	// Every goroutine waits on the same channel so they all hit Redis as close
	// together as the scheduler allows. That is the interleaving that a
	// two-phase "check then record" limiter fails: all fifty would read a count
	// below the limit and all fifty would be admitted.
	start := make(chan struct{})
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
		denied  int
		errs    []error
	)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, _, err := limiter.ConsumeRegistration(ctx, ip)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				errs = append(errs, err)
			case ok:
				allowed++
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
	// Exactly the limit, not "at most" the limit: the counter is incremented
	// before the decision, so the fifth caller takes the last slot. An
	// implementation that refused the fifth would be off by one and would let a
	// caller through for free on every window.
	if allowed != maxAttempts {
		t.Fatalf("allowed = %d, want exactly %d (denied %d)", allowed, maxAttempts, denied)
	}
	if denied != concurrency-maxAttempts {
		t.Fatalf("denied = %d, want %d", denied, concurrency-maxAttempts)
	}

	stored, err := client.Get(ctx, registrationKey(ip)).Int64()
	if err != nil {
		t.Fatalf("counter unreadable: %v", err)
	}
	if stored != concurrency {
		t.Fatalf("counter = %d, want %d: every attempt must be charged", stored, concurrency)
	}
}

func TestRegistrationLimiterTTLStartsAtFirstAttemptAndIsNotExtended(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()
	ip := uniqueTestIP()
	key := registrationKey(ip)
	t.Cleanup(func() { _ = client.Del(context.Background(), key).Err() })

	window := 90 * time.Second
	limiter := NewRegistrationLimiter(client).WithPolicy(10, window)

	if ok, _, err := limiter.ConsumeRegistration(ctx, ip); err != nil || !ok {
		t.Fatalf("first attempt: ok=%v err=%v", ok, err)
	}
	first, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if first <= 0 || first > window {
		t.Fatalf("ttl after the first attempt = %v, want (0, %v]", first, window)
	}

	// A caller who keeps retrying must not be able to push the reset further
	// away. Without that, an attacker could hold an address locked indefinitely
	// — and, worse, keep their own budget alive forever.
	for i := 0; i < 5; i++ {
		if _, _, err := limiter.ConsumeRegistration(ctx, ip); err != nil {
			t.Fatalf("attempt %d: %v", i+2, err)
		}
	}
	later, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if later > first {
		t.Fatalf("ttl grew from %v to %v: repeated attempts must not extend the window", first, later)
	}
}

func TestRegistrationLimiterDeniesBeyondLimitWithRetryAfter(t *testing.T) {
	client := redisTestClient(t)
	ctx := context.Background()
	ip := uniqueTestIP()
	t.Cleanup(func() { _ = client.Del(context.Background(), registrationKey(ip)).Err() })

	limiter := NewRegistrationLimiter(client).WithPolicy(2, time.Hour)

	for i := 1; i <= 2; i++ {
		ok, _, err := limiter.ConsumeRegistration(ctx, ip)
		if err != nil || !ok {
			t.Fatalf("attempt %d: ok=%v err=%v", i, ok, err)
		}
	}
	ok, retryAfter, err := limiter.ConsumeRegistration(ctx, ip)
	if err != nil {
		t.Fatalf("third attempt: %v", err)
	}
	if ok {
		t.Fatal("the third attempt must be denied when the limit is two")
	}
	if retryAfter <= 0 {
		t.Fatalf("retryAfter = %v, want a positive duration so the client knows when to come back", retryAfter)
	}
	if retryAfter > time.Hour {
		t.Fatalf("retryAfter = %v, longer than the window", retryAfter)
	}
}

func TestRegistrationLimiterFailsWhenRedisIsUnreachable(t *testing.T) {
	// A closed port: the limiter must report an error rather than allow the
	// attempt, because registration fails closed.
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 300 * time.Millisecond,
		MaxRetries:  0,
	})
	t.Cleanup(func() { _ = client.Close() })

	limiter := NewRegistrationLimiter(client).WithPolicy(5, time.Hour)
	allowed, _, err := limiter.ConsumeRegistration(context.Background(), "203.0.113.9")
	if err == nil {
		t.Fatal("an unreachable counter must be an error")
	}
	if allowed {
		t.Fatal("an unreachable counter must not allow the attempt")
	}
}
