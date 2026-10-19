package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/redis/go-redis/v9"
)


var tenantRequestScript = redis.NewScript(`
local limit = tonumber(ARGV[1])
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
if limit > 0 and current + 1 > limit then
  return 0
end
local next = redis.call('INCR', KEYS[1])
if next == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return 1
`)

var tenantTokenScript = redis.NewScript(`
local amount = tonumber(ARGV[1])
local daily_limit = tonumber(ARGV[2])
local monthly_limit = tonumber(ARGV[3])
local daily = tonumber(redis.call('GET', KEYS[1]) or '0')
local monthly = tonumber(redis.call('GET', KEYS[2]) or '0')
if daily_limit > 0 and daily + amount > daily_limit then
  return 0
end
if monthly_limit > 0 and monthly + amount > monthly_limit then
  return 0
end
local d = redis.call('INCRBY', KEYS[1], amount)
local m = redis.call('INCRBY', KEYS[2], amount)
if d == amount then redis.call('EXPIRE', KEYS[1], ARGV[4]) end
if m == amount then redis.call('EXPIRE', KEYS[2], ARGV[5]) end
return 1
`)

var incrementScript = redis.NewScript(`
local current = redis.call('INCRBY', KEYS[1], ARGV[1])
if current == tonumber(ARGV[1]) then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return current
`)

type Limiter struct {
	rdb *redis.Client

	// rebuildSrc, when set, lets the hot path detect that a tenant quota
	// counter is missing (e.g. Redis was flushed while the gateway kept
	// running) and reconstruct it from the settled request log on demand. See
	// SetQuotaRebuildSource and ensureQuotaCounters.
	rebuildSrc QuotaRebuildSource
	rebuildMu  sync.Mutex
}

// QuotaRebuildSource supplies the settled request-log rows a Quota Counter
// rebuild needs. The gateway wires it to the storage layer; providing it from
// the app keeps the limiter free of any storage dependency while still letting
// the hot path trigger a rebuild when it finds a counter missing.
type QuotaRebuildSource func(ctx context.Context) ([]model.QuotaUsageRow, error)

type TPMReservation struct {
	rdb    *redis.Client
	key    string
	amount int64
}

type TenantQuotaReservation struct {
	rdb       *redis.Client
	dailyKey  string
	monthlyKey string
	amount    int64
}

func New(rdb *redis.Client) *Limiter {
	return &Limiter{rdb: rdb}
}

// SetQuotaRebuildSource registers the loader that supplies settled request-log
// rows when the hot path finds a tenant quota counter missing. Passing nil
// disables on-demand rebuild (the default — a checkout or test that configures
// no source keeps the previous behaviour).
func (l *Limiter) SetQuotaRebuildSource(src QuotaRebuildSource) {
	l.rebuildSrc = src
}

// ensureQuotaCounters is the "counter missing" trigger of a Quota Counter
// rebuild. When a rebuild source is configured and the counter the caller is
// about to touch does not exist — the signature of a flushed cache, or a
// counter that was never armed — it reconstructs all tenant counters from the
// settled request log before the gate reads them.
//
// probeKey is the specific counter the caller intends to mutate; the two paths
// probe different keys because they depend on different counters (monthly
// requests vs daily tokens). A missing key under a configured source means the
// cache is not holding the settled baseline, so the gate would otherwise admit
// against zero and over-run the tenant's quota. The rebuild is idempotent
// (SET, not INCRBY) and serialised so concurrent requests cannot stampede the
// storage loader.
func (l *Limiter) ensureQuotaCounters(ctx context.Context, probeKey string) error {
	if l.rebuildSrc == nil {
		return nil
	}
	exists, err := l.rdb.Exists(ctx, probeKey).Result()
	if err != nil {
		return err
	}
	if exists == 1 {
		return nil
	}
	l.rebuildMu.Lock()
	defer l.rebuildMu.Unlock()
	rows, err := l.rebuildSrc(ctx)
	if err != nil {
		return err
	}
	_, err = l.RebuildQuotaCounters(ctx, rows)
	return err
}

func minuteBucket() string {
	return time.Now().UTC().Format("200601021504")
}

func tenantDayBucket() string {
	return time.Now().UTC().Format("20060102")
}

func tenantMonthBucket() string {
	return time.Now().UTC().Format("200601")
}

// The rebuild path and the hot path must agree on the exact key each counter
// lives under and how long it lives, or a "repaired" cache would reconstruct a
// different counter than the one the gate reads. These are the single source of
// truth for both, so the format is defined once.

// quotaRequestsKey is the monthly request counter for a tenant.
func quotaRequestsKey(tenantID, month string) string {
	return fmt.Sprintf("gw:quota:tenant:%s:requests:%s", tenantID, month)
}

// quotaDailyTokensKey is the daily token counter for a tenant.
func quotaDailyTokensKey(tenantID, day string) string {
	return fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", tenantID, day)
}

// quotaMonthlyTokensKey is the monthly token counter for a tenant.
func quotaMonthlyTokensKey(tenantID, month string) string {
	return fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", tenantID, month)
}

// TTLs are kept in sync with the historical behaviour of the live INCR path:
// daily counters roll over after three days, monthly after forty.
const (
	quotaDailyTTL   = 3 * 24 * time.Hour
	quotaMonthlyTTL = 40 * 24 * time.Hour
)

func (l *Limiter) AllowTenantRequest(ctx context.Context, tenantID string, monthlyLimit int64) (bool, error) {
	if monthlyLimit <= 0 {
		return true, nil
	}
	key := quotaRequestsKey(tenantID, tenantMonthBucket())
	if err := l.ensureQuotaCounters(ctx, key); err != nil {
		return false, err
	}
	allowed, err := tenantRequestScript.Run(ctx, l.rdb, []string{key}, monthlyLimit, int64(quotaMonthlyTTL/time.Second)).Int()
	return allowed == 1, err
}

func (l *Limiter) ReserveTenantTokens(ctx context.Context, tenantID string, amount, dailyLimit, monthlyLimit int64) (*TenantQuotaReservation, bool, error) {
	if amount <= 0 || (dailyLimit <= 0 && monthlyLimit <= 0) {
		return nil, true, nil
	}
	dailyKey := quotaDailyTokensKey(tenantID, tenantDayBucket())
	monthlyKey := quotaMonthlyTokensKey(tenantID, tenantMonthBucket())
	if err := l.ensureQuotaCounters(ctx, dailyKey); err != nil {
		return nil, false, err
	}
	allowed, err := tenantTokenScript.Run(
		ctx, l.rdb, []string{dailyKey, monthlyKey},
		amount, dailyLimit, monthlyLimit,
		int64(quotaDailyTTL/time.Second), int64(quotaMonthlyTTL/time.Second),
	).Int()
	if err != nil {
		return nil, false, err
	}
	if allowed != 1 {
		return nil, false, nil
	}
	return &TenantQuotaReservation{rdb: l.rdb, dailyKey: dailyKey, monthlyKey: monthlyKey, amount: amount}, true, nil
}

func (r *TenantQuotaReservation) Reconcile(ctx context.Context, actual int64) error {
	if r == nil || r.amount <= 0 || actual < 0 {
		return nil
	}
	delta := actual - r.amount
	if delta == 0 {
		return nil
	}
	pipe := r.rdb.TxPipeline()
	if delta > 0 {
		pipe.IncrBy(ctx, r.dailyKey, delta)
		pipe.IncrBy(ctx, r.monthlyKey, delta)
	} else {
		pipe.DecrBy(ctx, r.dailyKey, -delta)
		pipe.DecrBy(ctx, r.monthlyKey, -delta)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *TenantQuotaReservation) Refund(ctx context.Context) error {
	if r == nil || r.amount <= 0 {
		return nil
	}
	pipe := r.rdb.TxPipeline()
	pipe.DecrBy(ctx, r.dailyKey, r.amount)
	pipe.DecrBy(ctx, r.monthlyKey, r.amount)
	_, err := pipe.Exec(ctx)
	return err
}

func (l *Limiter) AllowRPM(ctx context.Context, keyID string, limit int64) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	key := fmt.Sprintf("gw:rl:key:%s:rpm:%s", keyID, minuteBucket())
	n, err := incrementScript.Run(ctx, l.rdb, []string{key}, 1, 120).Int64()
	return n <= limit, err
}

func (l *Limiter) ReserveTPM(ctx context.Context, keyID string, amount, limit int64) (*TPMReservation, bool, error) {
	if amount <= 0 || limit <= 0 {
		return nil, true, nil
	}
	key := fmt.Sprintf("gw:rl:key:%s:tpm:%s", keyID, minuteBucket())
	n, err := incrementScript.Run(ctx, l.rdb, []string{key}, amount, 120).Int64()
	if err != nil {
		return nil, false, err
	}
	if n > limit {
		_ = l.rdb.DecrBy(ctx, key, amount).Err()
		return nil, false, nil
	}
	return &TPMReservation{rdb: l.rdb, key: key, amount: amount}, true, nil
}

func (r *TPMReservation) Reconcile(ctx context.Context, actual int64) error {
	if r == nil || r.amount <= 0 || actual < 0 {
		return nil
	}
	delta := actual - r.amount
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		_, err := r.rdb.IncrBy(ctx, r.key, delta).Result()
		return err
	}
	_, err := r.rdb.DecrBy(ctx, r.key, -delta).Result()
	return err
}

func (r *TPMReservation) Refund(ctx context.Context) error {
	if r == nil || r.amount <= 0 {
		return nil
	}
	_, err := r.rdb.DecrBy(ctx, r.key, r.amount).Result()
	return err
}

func (l *Limiter) AcquireConcurrency(ctx context.Context, keyID string, limit int64) (func(), bool, error) {
	if limit <= 0 {
		return func() {}, true, nil
	}
	key := fmt.Sprintf("gw:concurrency:key:%s", keyID)
	n, err := l.rdb.Incr(ctx, key).Result()
	if err != nil {
		return nil, false, err
	}
	_ = l.rdb.Expire(ctx, key, 15*time.Minute).Err()
	if n > limit {
		_ = l.rdb.Decr(ctx, key).Err()
		return nil, false, nil
	}
	release := func() {
		_ = l.rdb.Decr(context.Background(), key).Err()
	}
	return release, true, nil
}
