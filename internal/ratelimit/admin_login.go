package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// AdminLoginLimiter throttles administrator login attempts.
//
// Three counters are kept per attempt:
//
//   - per client IP, which stops one host from spraying many accounts;
//   - per IP+username, which stops a single host from hammering one account;
//   - per username alone, which stops a distributed campaign against one
//     account from many source addresses. Without this third counter a botnet
//     trivially walks around the first two.
//
// All applicable counters must allow the attempt for it to proceed.
//
// Counters only advance on failure, so a legitimate user is never locked out by
// their own successful logins.
type AdminLoginLimiter struct {
	rdb         *redis.Client
	maxAttempts int64
	window      time.Duration
}

// adminUsernameLimitMultiplier widens the username-only budget relative to the
// per-IP budget. A shared campus NAT can legitimately exhaust the per-IP budget
// for several users at once, so the account-level budget is deliberately
// looser; it exists to stop a slow multi-source campaign, not to be tight.
const adminUsernameLimitMultiplier = 4

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

// adminLoginCounter is one throttle dimension: a Redis key and its budget.
type adminLoginCounter struct {
	key   string
	limit int64
}

// normalizeAdminUsername folds a username the same way the storage layer does
// (case-insensitive lookup), so "Admin" and "admin" share one throttle budget
// instead of getting two.
func normalizeAdminUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// adminLoginIPKey and adminLoginPairKey are the two per-source counters. They
// are also the pair a successful login clears.
func adminLoginIPKey(ip string) string {
	return "gw:admin:login:ip:" + adminLoginKeyPart(ip)
}

func adminLoginPairKey(ip, username string) string {
	return "gw:admin:login:pair:" + adminLoginKeyPart(ip) + ":" + adminLoginKeyPart(normalizeAdminUsername(username))
}

func adminLoginUserKey(username string) string {
	return "gw:admin:login:user:" + adminLoginKeyPart(normalizeAdminUsername(username))
}

// adminLoginKeyPart keeps keys bounded and avoids storing raw usernames or IPs
// in Redis key names; usernames are personal data.
func adminLoginKeyPart(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:8])
}

// counters returns every dimension that applies to this attempt.
func (l *AdminLoginLimiter) counters(ip, username string) []adminLoginCounter {
	out := []adminLoginCounter{
		{key: adminLoginIPKey(ip), limit: l.maxAttempts},
		{key: adminLoginPairKey(ip, username), limit: l.maxAttempts},
	}
	if normalizeAdminUsername(username) != "" {
		out = append(out, adminLoginCounter{key: adminLoginUserKey(username), limit: l.usernameLimit()})
	}
	return out
}

// usernameLimit is the account-level budget for a distributed guessing
// campaign. Derived from maxAttempts so the operator tunes a single knob.
func (l *AdminLoginLimiter) usernameLimit() int64 {
	if l.maxAttempts <= 0 {
		return 0
	}
	return l.maxAttempts * adminUsernameLimitMultiplier
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
	for _, counter := range l.counters(ip, username) {
		pipe := l.rdb.Pipeline()
		get := pipe.Get(ctx, counter.key)
		ttl := pipe.TTL(ctx, counter.key)
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			// Redis trouble must not lock everyone out of the console. The
			// caller logs the error: failing open silently would hide an outage.
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
		if err != nil || n < counter.limit {
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

// RecordAdminLoginFailure increments every counter that applies.
func (l *AdminLoginLimiter) RecordAdminLoginFailure(ctx context.Context, ip, username string) error {
	if l == nil || l.rdb == nil || l.maxAttempts <= 0 {
		return nil
	}
	window := l.windowSeconds()
	for _, counter := range l.counters(ip, username) {
		if err := incrWithTTLScript.Run(ctx, l.rdb, []string{counter.key}, window).Err(); err != nil {
			return err
		}
	}
	return nil
}

// resetKeys are the counters a successful login clears. The IP-wide counter is
// deliberately absent: see ResetAdminLogin for why.
func resetKeys(ip, username string) []string {
	keys := []string{adminLoginPairKey(ip, username)}
	if normalizeAdminUsername(username) != "" {
		keys = append(keys, adminLoginUserKey(username))
	}
	return keys
}

// ResetAdminLogin clears the per-source counters after a successful login, so a
// user who mistyped a few times starts clean.
//
// The IP-wide counter is deliberately NOT cleared: an attacker holding one
// legitimate low-privilege account could otherwise alternate a successful login
// with a spray to keep the whole IP budget empty. The account-level counter is
// cleared because a successful login proves the caller owns that account.
func (l *AdminLoginLimiter) ResetAdminLogin(ctx context.Context, ip, username string) error {
	if l == nil || l.rdb == nil {
		return nil
	}
	return l.rdb.Del(ctx, resetKeys(ip, username)...).Err()
}

// Describe returns a human-readable summary of the policy, used in logs.
func (l *AdminLoginLimiter) Describe() string {
	return fmt.Sprintf("%d failures per %s per IP and per IP+username, %d per username",
		l.maxAttempts, l.window, l.usernameLimit())
}
