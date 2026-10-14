package ratelimit

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RegistrationLimiter throttles public account creation per source address.
//
// It is deliberately a different shape from AdminLoginLimiter: login counters
// advance only on failure, because a legitimate user must never be locked out
// by their own successes. Registration is the opposite — every attempt consumes
// budget, including the ones that fail on a duplicate username. Otherwise an
// automated script could probe for existing usernames at full speed as long as
// it always picked a name that was already taken.
type RegistrationLimiter struct {
	rdb         *redis.Client
	maxAttempts int64
	window      time.Duration
}

// NewRegistrationLimiter builds a limiter with a default policy of 5 attempts
// per hour per source address.
func NewRegistrationLimiter(rdb *redis.Client) *RegistrationLimiter {
	return &RegistrationLimiter{rdb: rdb, maxAttempts: 5, window: time.Hour}
}

// WithPolicy overrides the default limits. A maxAttempts of 0 disables
// throttling entirely.
func (l *RegistrationLimiter) WithPolicy(maxAttempts int64, window time.Duration) *RegistrationLimiter {
	l.maxAttempts = maxAttempts
	if window > 0 {
		l.window = window
	}
	return l
}

// registrationKey hashes the address for the same reason the login limiter
// does: a Redis key name is not a place to store a personal identifier.
func registrationKey(ip string) string {
	return "gw:register:ip:" + adminLoginKeyPart(ip)
}

func (l *RegistrationLimiter) windowSeconds() int64 {
	window := int64(l.window / time.Second)
	if window <= 0 {
		window = int64(time.Hour / time.Second)
	}
	return window
}

// AllowRegistration reports whether another attempt is permitted from this
// address, and when it is not, how long the caller should wait.
func (l *RegistrationLimiter) AllowRegistration(ctx context.Context, ip string) (bool, time.Duration, error) {
	if l == nil || l.rdb == nil || l.maxAttempts <= 0 {
		return true, 0, nil
	}
	pipe := l.rdb.Pipeline()
	get := pipe.Get(ctx, registrationKey(ip))
	ttl := pipe.TTL(ctx, registrationKey(ip))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return false, 0, err
	}
	raw, err := get.Result()
	if err != nil {
		if err == redis.Nil {
			return true, 0, nil
		}
		return false, 0, err
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < l.maxAttempts {
		return true, 0, nil
	}
	remaining, err := ttl.Result()
	if err != nil || remaining < 0 {
		remaining = time.Duration(l.windowSeconds()) * time.Second
	}
	return false, remaining, nil
}

// RecordRegistration consumes one unit of budget. It is called for every
// attempt that reaches the service, before the account is created.
func (l *RegistrationLimiter) RecordRegistration(ctx context.Context, ip string) error {
	if l == nil || l.rdb == nil || l.maxAttempts <= 0 {
		return nil
	}
	return incrWithTTLScript.Run(ctx, l.rdb, []string{registrationKey(ip)}, l.windowSeconds()).Err()
}

// Describe returns a human-readable summary of the policy, used in logs.
func (l *RegistrationLimiter) Describe() string {
	return strconv.FormatInt(l.maxAttempts, 10) + " registrations per " + l.window.String() + " per IP"
}
