package ratelimit

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestAdminLoginCountersCoverThreeDimensions pins that a login attempt is gated
// by the per-IP, per-IP+username and per-username counters. The third one is
// what stops a distributed campaign against a single account from many source
// addresses.
func TestAdminLoginCountersCoverThreeDimensions(t *testing.T) {
	l := NewAdminLoginLimiter(nil).WithPolicy(8, 15*time.Minute)

	counters := l.counters("203.0.113.9", "campus-admin")
	if len(counters) != 3 {
		t.Fatalf("expected 3 throttle dimensions, got %d: %+v", len(counters), counters)
	}

	byPrefix := map[string]adminLoginCounter{}
	for _, c := range counters {
		switch {
		case strings.HasPrefix(c.key, "gw:admin:login:ip:"):
			byPrefix["ip"] = c
		case strings.HasPrefix(c.key, "gw:admin:login:pair:"):
			byPrefix["pair"] = c
		case strings.HasPrefix(c.key, "gw:admin:login:user:"):
			byPrefix["user"] = c
		default:
			t.Fatalf("unexpected counter key %q", c.key)
		}
	}
	for _, name := range []string{"ip", "pair", "user"} {
		if _, ok := byPrefix[name]; !ok {
			t.Fatalf("missing the %q throttle dimension: %+v", name, counters)
		}
	}

	if byPrefix["ip"].limit != 8 || byPrefix["pair"].limit != 8 {
		t.Fatalf("per-source budgets must equal maxAttempts, got %d and %d",
			byPrefix["ip"].limit, byPrefix["pair"].limit)
	}
	if byPrefix["user"].limit != 8*adminUsernameLimitMultiplier {
		t.Fatalf("username budget = %d, want %d", byPrefix["user"].limit, 8*adminUsernameLimitMultiplier)
	}
}

// TestAdminLoginUsernameIsNormalised pins that "Admin" and "admin" share one
// throttle budget, matching the case-insensitive account lookup. Otherwise an
// attacker could multiply their budget by varying the letter case.
func TestAdminLoginUsernameIsNormalised(t *testing.T) {
	l := NewAdminLoginLimiter(nil).WithPolicy(8, 15*time.Minute)

	lower := l.counters("203.0.113.9", "campus-admin")
	mixed := l.counters("203.0.113.9", "  Campus-Admin  ")

	if len(lower) != len(mixed) {
		t.Fatalf("counter count differs: %d vs %d", len(lower), len(mixed))
	}
	for i := range lower {
		if lower[i].key != mixed[i].key {
			t.Fatalf("counter %d differs across casing: %q vs %q", i, lower[i].key, mixed[i].key)
		}
	}
}

// TestAdminLoginKeysDoNotLeakRawValues pins that usernames (personal data) and
// IPs are hashed rather than embedded in Redis key names.
func TestAdminLoginKeysDoNotLeakRawValues(t *testing.T) {
	key := adminLoginUserKey("campus-admin")
	if strings.Contains(key, "campus-admin") {
		t.Fatalf("the raw username must not appear in the key: %q", key)
	}
	pair := adminLoginPairKey("203.0.113.9", "campus-admin")
	if strings.Contains(pair, "203.0.113.9") || strings.Contains(pair, "campus-admin") {
		t.Fatalf("the raw pair must not appear in the key: %q", pair)
	}
}

// TestAdminLoginResetKeepsTheIPSprayCounter pins the deliberate asymmetry: a
// successful login clears the account-scoped budgets but never the IP-wide
// spray counter, so an attacker cannot launder a spray through a login they
// legitimately own.
func TestAdminLoginResetKeepsTheIPSprayCounter(t *testing.T) {
	keys := resetKeys("203.0.113.9", "campus-admin")
	ipKey := adminLoginIPKey("203.0.113.9")
	for _, k := range keys {
		if k == ipKey {
			t.Fatalf("a successful login must not clear the IP spray counter: %v", keys)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("expected the pair and username counters to be cleared, got %v", keys)
	}
}

// TestAdminLoginUsernameLimitScalesWithMaxAttempts keeps the single-knob
// contract: the account-level budget is derived, never configured separately.
func TestAdminLoginUsernameLimitScalesWithMaxAttempts(t *testing.T) {
	cases := map[int64]int64{0: 0, 4: 16, 8: 32, 30: 120}
	for maxAttempts, want := range cases {
		l := NewAdminLoginLimiter(nil).WithPolicy(maxAttempts, 15*time.Minute)
		if got := l.usernameLimit(); got != want {
			t.Fatalf("maxAttempts=%d: usernameLimit=%d, want %d", maxAttempts, got, want)
		}
	}
}

// TestAdminLoginDisabledPolicyAllowsEverything keeps the documented escape
// hatch: maxAttempts = 0 turns throttling off.
func TestAdminLoginDisabledPolicyAllowsEverything(t *testing.T) {
	l := NewAdminLoginLimiter(nil).WithPolicy(0, 15*time.Minute)
	allowed, _, err := l.AllowAdminLogin(context.Background(), "203.0.113.9", "campus-admin")
	if err != nil || !allowed {
		t.Fatalf("a zero policy must disable throttling, got allowed=%v err=%v", allowed, err)
	}
	if err := l.RecordAdminLoginFailure(context.Background(), "203.0.113.9", "campus-admin"); err != nil {
		t.Fatalf("record with throttling disabled: %v", err)
	}
}
