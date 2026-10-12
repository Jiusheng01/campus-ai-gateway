package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// AdminLoginLimiter throttles administrator login attempts.
//
// Two counters are kept per attempt: one keyed by client IP and one keyed by
// IP+username. The IP counter stops a single host from spraying many accounts;
// the pair counter stops a distributed attempt against one account. Both must
// allow the attempt for it to proceed.
//
// Counters only advance on failure, so a legitimate user is never locked out by
// their own successful logins.
type AdminLoginLimiter struct {
	rdb         *redis.Client
	maxAttempts int64
	window      time.Duration
}

// NewAdminLoginLimiter builds a limiter with a default policy of 8 failures per
// 15 minutes.
func NewAdminLoginLimiter(rdb *redis.Client) *AdminLoginLimiter {
	return &AdminLoginLimiter{rdb: rdb, maxAttempts: 8, window: 15 * time.Minute}
}

// WithPolicy overrides the default limits. A maxAttempts of 0 disables
// throttling entirely.
func (l *AdminLoginLimiter) WithPolicy(maxAttempts int64, window time.Duration) *AdminLoginLimiter {
	l.maxAttempts = maxAttempts
	if window > 0 {
		l.window = window
	}
	return l
}

const defaultAdminLoginWindow = 15 * time.Minute

// adminLoginIncrScript increments a counter and guarantees it has a TTL, even
// when the key already existed without one.
var adminLoginIncrScript = redis.NewScript(`
local current = redis.call('INCR', KEYS[1])
if current == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
local ttl = redis.call('TTL', KEYS[1])
if ttl < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
  ttl = ARGV[1]
end
return {current, ttl}
`)

func adminLoginKeys(ip, username string) []string {
	return []string{
		"gw:admin:login:ip:" + adminLoginKeyPart(ip),
		"gw:admin:login:pair:" + adminLoginKeyPart(ip) + ":" + adminLoginKeyPart(username),
	}
}

// adminLoginKeyPart keeps keys bounded and avoids storing raw usernames or IPs
// in Redis key names; usernames are personal data.
func adminLoginKeyPart(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:8])
}

func (l *AdminLoginLimiter) windowSeconds() int64 {
	window := int64(l.window / time.Second)
	if window <= 0 {
		window = int64(defaultAdminLoginWindow / time.Second)
	}
	return window
}

// AllowAdminLogin reports whether another attempt is allowed and, when it is
// not, how long until the window resets.
//
// The counters are not incremented here: a successful login must not consume
// budget. Failures are recorded through RecordAdminLoginFailure.
func (l *AdminLoginLimiter) AllowAdminLogin(ctx context.Context, ip, username string) (bool, time.Duration, error) {
	if l == nil || l.rdb == nil || l.maxAttempts <= 0 {
		return true, 0, nil
	}
	for _, key := range adminLoginKeys(ip, username) {
		pipe := l.rdb.Pipeline()
		get := pipe.Get(ctx, key)
		ttl := pipe.TTL(ctx, key)
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			// Redis trouble must not lock everyone out of the console.
			return true, 0, err
		}
		raw, err := get.Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			return true, 0, err
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < l.maxAttempts {
			continue
		}
		remaining, err := ttl.Result()
		if err != nil || remaining < 0 {
			remaining = time.Duration(l.windowSeconds()) * time.Second
		}
		return false, remaining, nil
	}
	return true, 0, nil
}

// RecordAdminLoginFailure increments both counters.
func (l *AdminLoginLimiter) RecordAdminLoginFailure(ctx context.Context, ip, username string) error {
	if l == nil || l.rdb == nil || l.maxAttempts <= 0 {
		return nil
	}
	window := l.windowSeconds()
	for _, key := range adminLoginKeys(ip, username) {
		if err := adminLoginIncrScript.Run(ctx, l.rdb, []string{key}, window).Err(); err != nil {
			return err
		}
	}
	return nil
}

// ResetAdminLogin clears the counters after a successful login, so a user who
// mistyped a few times starts clean.
func (l *AdminLoginLimiter) ResetAdminLogin(ctx context.Context, ip, username string) error {
	if l == nil || l.rdb == nil {
		return nil
	}
	return l.rdb.Del(ctx, adminLoginKeys(ip, username)...).Err()
}

// Describe returns a human-readable summary of the policy, used in logs.
func (l *AdminLoginLimiter) Describe() string {
	return fmt.Sprintf("%d failures per %s", l.maxAttempts, l.window)
}
