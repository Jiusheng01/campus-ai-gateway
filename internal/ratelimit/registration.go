package ratelimit

import (
	"context"
	"fmt"
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
// throttling entirely, and in that case Redis is never contacted.
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

// ConsumeRegistration charges one attempt and reports whether it is allowed.
//
// The decision and the increment are a single Redis operation, and that is the
// whole point of this method existing in this shape. Splitting it into "may I?"
// followed by "record that I did" is a time-of-check-to-time-of-use race: with
// a limit of five, fifty callers that arrive together can all read a count of
// four, all conclude they are within budget, and all proceed — so a single
// burst admits far more than the policy allows, which defeats the entire
// purpose of an anti-abuse counter on an endpoint anyone on the internet can
// reach.
//
// The Lua script increments first and answers afterwards, so the fifth caller
// takes the last slot and the sixth is refused no matter how many arrive at
// once. It returns the post-increment count, so the comparison is
// `current <= maxAttempts`: a limit of five lets exactly five through.
//
// The window starts at the first request and is never extended. The script only
// sets the expiry when it creates the key, so a caller who keeps retrying
// cannot push the reset further away and hold an address locked indefinitely;
// the repair branch exists only for a key that somehow lost its TTL.
func (l *RegistrationLimiter) ConsumeRegistration(ctx context.Context, ip string) (bool, time.Duration, error) {
	if l == nil || l.rdb == nil || l.maxAttempts <= 0 {
		return true, 0, nil
	}

	raw, err := incrWithTTLScript.Run(ctx, l.rdb, []string{registrationKey(ip)}, l.windowSeconds()).Result()
	if err != nil {
		return false, 0, err
	}
	current, ttlSeconds, err := parseCounterReply(raw)
	if err != nil {
		return false, 0, err
	}

	if current <= l.maxAttempts {
		return true, 0, nil
	}

	retryAfter := time.Duration(ttlSeconds) * time.Second
	if retryAfter <= 0 {
		retryAfter = time.Duration(l.windowSeconds()) * time.Second
	}
	return false, retryAfter, nil
}

// parseCounterReply unpacks the {current, ttl} array the script returns.
//
// A malformed reply is reported as an error rather than treated as "allowed":
// this counter is the only thing standing between the internet and unlimited
// account creation, so an unreadable answer has to fail closed like any other
// limiter outage.
func parseCounterReply(raw any) (int64, int64, error) {
	values, ok := raw.([]any)
	if !ok || len(values) != 2 {
		return 0, 0, fmt.Errorf("registration limiter: unexpected reply %T", raw)
	}
	current, ok := values[0].(int64)
	if !ok {
		return 0, 0, fmt.Errorf("registration limiter: counter is %T, want int64", values[0])
	}
	ttlSeconds, ok := values[1].(int64)
	if !ok {
		return 0, 0, fmt.Errorf("registration limiter: ttl is %T, want int64", values[1])
	}
	return current, ttlSeconds, nil
}

// Describe returns a human-readable summary of the policy, used in logs.
func (l *RegistrationLimiter) Describe() string {
	return strconv.FormatInt(l.maxAttempts, 10) + " registrations per " + l.window.String() + " per IP"
}
