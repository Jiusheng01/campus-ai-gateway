package ratelimit

import (
	"context"
	"fmt"
	"time"

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
}

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

func minuteBucket() string {
	return time.Now().UTC().Format("200601021504")
}

func tenantDayBucket() string {
	return time.Now().UTC().Format("20060102")
}

func tenantMonthBucket() string {
	return time.Now().UTC().Format("200601")
}

func (l *Limiter) AllowTenantRequest(ctx context.Context, tenantID string, monthlyLimit int64) (bool, error) {
	if monthlyLimit <= 0 {
		return true, nil
	}
	key := fmt.Sprintf("gw:quota:tenant:%s:requests:%s", tenantID, tenantMonthBucket())
	allowed, err := tenantRequestScript.Run(ctx, l.rdb, []string{key}, monthlyLimit, int64((40*24*time.Hour)/time.Second)).Int()
	return allowed == 1, err
}

func (l *Limiter) ReserveTenantTokens(ctx context.Context, tenantID string, amount, dailyLimit, monthlyLimit int64) (*TenantQuotaReservation, bool, error) {
	if amount <= 0 || (dailyLimit <= 0 && monthlyLimit <= 0) {
		return nil, true, nil
	}
	dailyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:day:%s", tenantID, tenantDayBucket())
	monthlyKey := fmt.Sprintf("gw:quota:tenant:%s:tokens:month:%s", tenantID, tenantMonthBucket())
	allowed, err := tenantTokenScript.Run(
		ctx, l.rdb, []string{dailyKey, monthlyKey},
		amount, dailyLimit, monthlyLimit,
		int64((3*24*time.Hour)/time.Second), int64((40*24*time.Hour)/time.Second),
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
