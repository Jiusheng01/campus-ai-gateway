package ratelimit

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/redis/go-redis/v9"
)

// tenantRequestChainScript enforces the monthly request cap across every level
// of the tenant chain.
//
// The two passes are the point of the script. The whole chain is checked before
// anything is written, so a rejection at the deepest level cannot leave the
// ancestors' counters incremented -- otherwise a student who keeps hitting
// their own cap would still be burning through their teacher's and their
// department's budgets while being told "no".
//
// ARGV[1]      bucket TTL in seconds
// ARGV[2..]    per-key limit, index-aligned with KEYS (0 = unlimited)
// returns      0 when allowed, else the 1-based index of the first rejecting key
var tenantRequestChainScript = redis.NewScript(`
local n = #KEYS
for i = 1, n do
  local limit = tonumber(ARGV[i + 1])
  local current = tonumber(redis.call('GET', KEYS[i]) or '0')
  if limit > 0 and current + 1 > limit then
    return i
  end
end
for i = 1, n do
  local next = redis.call('INCR', KEYS[i])
  if next == 1 then
    redis.call('EXPIRE', KEYS[i], ARGV[1])
  end
end
return 0
`)

// tenantTokenChainScript reserves tokens against the daily and monthly bucket
// of every level in the tenant chain. Reserving at every level is what makes
// the hierarchy inclusive: a leaf's usage lands on its own counters and on all
// of its ancestors' at once, so a parent's total is the sum of its subtree
// without any separate ledger or periodic rollup.
//
// KEYS are level-major: for level i, KEYS[2i-1] is its daily bucket and
// KEYS[2i] its monthly bucket.
//
// ARGV[1]      amount to reserve
// ARGV[2]      daily bucket TTL in seconds
// ARGV[3]      monthly bucket TTL in seconds
// ARGV[4..]    per level (daily_limit, monthly_limit)
// returns      0 when reserved, else the 1-based index of the first rejecting level
//
// Like the request script, it checks everything before writing anything. The
// EXPIRE is only set on the write that creates a bucket (the counter going from
// 0 to exactly `amount`): refreshing the TTL on every write would keep a busy
// tenant's bucket alive forever, and a counter that never expires never resets.
var tenantTokenChainScript = redis.NewScript(`
local amount = tonumber(ARGV[1])
local ttl_daily = tonumber(ARGV[2])
local ttl_monthly = tonumber(ARGV[3])
local n = #KEYS / 2

for i = 1, n do
  local daily_limit = tonumber(ARGV[2 * i + 2])
  local monthly_limit = tonumber(ARGV[2 * i + 3])
  local daily = tonumber(redis.call('GET', KEYS[2 * i - 1]) or '0')
  local monthly = tonumber(redis.call('GET', KEYS[2 * i]) or '0')
  if daily_limit > 0 and daily + amount > daily_limit then
    return i
  end
  if monthly_limit > 0 and monthly + amount > monthly_limit then
    return i
  end
end

for i = 1, n do
  local d = redis.call('INCRBY', KEYS[2 * i - 1], amount)
  if d == amount then
    redis.call('EXPIRE', KEYS[2 * i - 1], ttl_daily)
  end
  local m = redis.call('INCRBY', KEYS[2 * i], amount)
  if m == amount then
    redis.call('EXPIRE', KEYS[2 * i], ttl_monthly)
  end
end
return 0
`)

var incrementScript = redis.NewScript(`
local current = redis.call('INCRBY', KEYS[1], ARGV[1])
if current == tonumber(ARGV[1]) then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return current
`)

// safeDecrScript decrements a counter without ever driving it below zero and
// without resurrecting a key that has already expired.
//
// A reservation can outlive its own time bucket -- a long generation crossing a
// minute boundary, or a settle that lands after the bucket TTL. A plain DECRBY
// would then recreate the key at a negative value (free quota) with no TTL at
// all (a permanent key leak). This script clamps instead.
var safeDecrScript = redis.NewScript(`
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
if current <= 0 then
  return 0
end
local amount = tonumber(ARGV[1])
if current < amount then
  amount = current
end
return redis.call('DECRBY', KEYS[1], amount)
`)

type Limiter struct {
	rdb *redis.Client
}

type TPMReservation struct {
	rdb    *redis.Client
	key    string
	amount int64
	// settled guards Reconcile/Refund so that at most one of them ever takes
	// effect. Without it a double settle (for example a rejection path that
	// refunds and the deferred settle that refunds again) would drive the
	// counter negative and hand out free quota.
	settled atomic.Bool
}

// TokenChainReservation holds a token reservation made against every level of a
// quota chain at once. Settling it adjusts all levels together, which is what
// keeps a parent's counter equal to the sum of its subtree.
type TokenChainReservation struct {
	rdb *redis.Client
	// keys alternates daily, monthly per active level, matching the order the
	// script was given.
	keys   []string
	amount int64
	// settled: see TPMReservation.settled.
	settled atomic.Bool
}

// TenantChainReservation is the historical name of TokenChainReservation. The
// type became axis-agnostic once model quotas were added -- it only ever held a
// list of keys -- so the old name is kept as an alias rather than churned
// through every caller.
type TenantChainReservation = TokenChainReservation

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

// AllowTenantRequestChain enforces the monthly request cap at every level of
// the chain, root first.
//
// It reports which tenant rejected the request so the caller can say whose
// budget ran out rather than a generic "quota exceeded".
//
// There is no per-model counterpart. The request cap is checked before the
// request body is read (a rejected request must not cost a 16 MB read), and at
// that point the model name is not known yet. See the per-model token quota for
// the axis that does know the model.
func (l *Limiter) AllowTenantRequestChain(ctx context.Context, chain []model.TenantQuota) (bool, *QuotaRejection, error) {
	type level struct {
		tenantID string
		label    string
		limit    int64
	}
	month := tenantMonthBucket()

	active := make([]level, 0, len(chain))
	keys := make([]string, 0, len(chain))
	args := make([]any, 0, len(chain)+1)
	args = append(args, int64((40*24*time.Hour)/time.Second))

	for _, q := range chain {
		// A level with no cap costs nothing and should not get a key written.
		if q.MonthlyRequestLimit <= 0 {
			continue
		}
		active = append(active, level{
			tenantID: q.TenantID,
			label:    "租户:" + tenantLabel(q),
			limit:    q.MonthlyRequestLimit,
		})
		keys = append(keys, fmt.Sprintf("gw:quota:tenant:%s:requests:%s", q.TenantID, month))
		args = append(args, q.MonthlyRequestLimit)
	}
	if len(keys) == 0 {
		return true, nil, nil
	}

	n, err := tenantRequestChainScript.Run(ctx, l.rdb, keys, args...).Int()
	if err != nil {
		return false, nil, err
	}
	if n == 0 {
		return true, nil, nil
	}
	return false, &QuotaRejection{Label: active[n-1].label, TenantID: active[n-1].tenantID}, nil
}

// QuotaLevel is one enforcing level of a token quota chain: a pair of Redis
// buckets plus the caps that apply to them.
//
// It replaced a tenant-specific struct so a second axis -- per-model quotas --
// could reuse the same Lua script and the same reservation type. The script is
// index driven: all it ever sees is "a list of keys, and a limit for each". It
// has no notion of what a key means, which is exactly why adding an axis needed
// no change to the script at all.
type QuotaLevel struct {
	// Label names this level in a rejection, e.g. "租户:计算机学院" or
	// "模型:campus-smart(全校)". Without it a 429 cannot say whose budget ran
	// out, which is the difference between a quota system operators trust and
	// one they route around.
	Label string
	// TenantID scopes the buckets to one tenant. Empty means the level is not
	// tenant-scoped, which is the case for a system-wide model pool.
	TenantID string
	// ModelAlias scopes the buckets to one model. Empty means the level is the
	// plain, all-models tenant quota.
	ModelAlias string

	// Effective limits, 0 meaning "no cap at this level".
	DailyLimit   int64
	MonthlyLimit int64
}

// redisKey builds one bucket name. The three shapes are separate namespaces so
// that a tenant's total usage and its per-model usage can never collide, and so
// that a model pool is not mistaken for a tenant named after a model:
//
//	tenant quota        gw:quota:tenant:<id>:tokens:<period>:<bucket>
//	system model pool   gw:quota:model:<alias>:tokens:<period>:<bucket>
//	tenant model quota  gw:quota:tenant:<id>:model:<alias>:tokens:<period>:<bucket>
//
// The first shape is byte-for-byte the key the gateway wrote before this
// change, so an existing deployment's counters keep working.
func (lv QuotaLevel) redisKey(period, bucket string) string {
	switch {
	case lv.ModelAlias == "":
		return fmt.Sprintf("gw:quota:tenant:%s:tokens:%s:%s", lv.TenantID, period, bucket)
	case lv.TenantID == "":
		return fmt.Sprintf("gw:quota:model:%s:tokens:%s:%s", lv.ModelAlias, period, bucket)
	default:
		return fmt.Sprintf("gw:quota:tenant:%s:model:%s:tokens:%s:%s", lv.TenantID, lv.ModelAlias, period, bucket)
	}
}

// tenantLabel names a tenant level for a rejection. The name is preferred over
// the id because the id means nothing to whoever reads the security event; the
// id is the fallback for a chain built without names (tests, mainly).
func tenantLabel(q model.TenantQuota) string {
	if q.Name != "" {
		return q.Name
	}
	return q.TenantID
}

// TenantQuotaLevels adapts a resolved tenant chain to quota levels.
func TenantQuotaLevels(chain []model.TenantQuota) []QuotaLevel {
	out := make([]QuotaLevel, 0, len(chain))
	for _, q := range chain {
		out = append(out, QuotaLevel{
			Label:        "租户:" + tenantLabel(q),
			TenantID:     q.TenantID,
			DailyLimit:   q.DailyTokenLimit,
			MonthlyLimit: q.MonthlyTokenLimit,
		})
	}
	return out
}

// ModelQuotaLevels adapts a resolved model quota chain to quota levels. The
// caller is responsible for ordering: whichever level comes first is the one
// reported when several would reject, so the shared system pool should precede
// the per-tenant levels.
func ModelQuotaLevels(levels []model.ModelQuotaLevel) []QuotaLevel {
	out := make([]QuotaLevel, 0, len(levels))
	for _, lv := range levels {
		out = append(out, QuotaLevel{
			Label:        lv.Label,
			TenantID:     lv.TenantID,
			ModelAlias:   lv.ModelAlias,
			DailyLimit:   lv.DailyTokenLimit,
			MonthlyLimit: lv.MonthlyTokenLimit,
		})
	}
	return out
}

// activeQuotaLevels keeps only the levels that impose a token cap. Dropping the
// uncapped ones is not just an optimisation: it keeps Redis from accumulating a
// pair of counters per ancestor for subjects that have no budget configured.
func activeQuotaLevels(levels []QuotaLevel) []QuotaLevel {
	out := make([]QuotaLevel, 0, len(levels))
	for _, lv := range levels {
		if lv.DailyLimit <= 0 && lv.MonthlyLimit <= 0 {
			continue
		}
		out = append(out, lv)
	}
	return out
}

// QuotaRejection names the level that refused a reservation.
type QuotaRejection struct {
	// Label is human readable: "租户:计算机学院" or "模型:campus-smart(全校)".
	Label string
	// TenantID is the uuid of the rejecting tenant. It is empty when the level
	// is a system-wide model pool, which belongs to no tenant -- the security
	// event records that as a NULL tenant rather than inventing one.
	TenantID string
	// ModelAlias is set when the rejecting level is a model pool, so the event
	// can point at the model as well as the subject.
	ModelAlias string
}

// ReserveTokenChain reserves amount against the daily and monthly bucket of
// every level, across every axis at once. On rejection it names the level whose
// budget would have been exceeded.
//
// The axes are combined into one call on purpose. The script checks every level
// before writing any of them, so a request that would blow the campus-wide pool
// for a model cannot leave the tenant's own counters incremented -- the same
// guarantee the tenant chain has always had, now extended to the model chain.
func (l *Limiter) ReserveTokenChain(
	ctx context.Context,
	levels []QuotaLevel,
	amount int64,
) (*TokenChainReservation, bool, *QuotaRejection, error) {
	if amount <= 0 {
		return nil, true, nil, nil
	}
	active := activeQuotaLevels(levels)
	if len(active) == 0 {
		return nil, true, nil, nil
	}

	day, month := tenantDayBucket(), tenantMonthBucket()
	keys := make([]string, 0, len(active)*2)
	args := make([]any, 0, 3+len(active)*2)
	args = append(args,
		amount,
		int64((3*24*time.Hour)/time.Second),
		int64((40*24*time.Hour)/time.Second),
	)
	for _, lv := range active {
		keys = append(keys, lv.redisKey("day", day), lv.redisKey("month", month))
		args = append(args, lv.DailyLimit, lv.MonthlyLimit)
	}

	n, err := tenantTokenChainScript.Run(ctx, l.rdb, keys, args...).Int()
	if err != nil {
		return nil, false, nil, err
	}
	if n != 0 {
		rej := active[n-1]
		return nil, false, &QuotaRejection{
			Label:      rej.Label,
			TenantID:   rej.TenantID,
			ModelAlias: rej.ModelAlias,
		}, nil
	}
	return &TokenChainReservation{rdb: l.rdb, keys: keys, amount: amount}, true, nil, nil
}

// ReserveTenantTokensChain is ReserveTokenChain for the tenant axis alone. It
// is kept because the tenant chain is checked on its own in tests and because
// the narrower signature (a tenant id rather than a rejection struct) reads
// better at call sites that only ever deal with tenants.
func (l *Limiter) ReserveTenantTokensChain(
	ctx context.Context,
	chain []model.TenantQuota,
	amount int64,
) (*TenantChainReservation, bool, string, error) {
	res, allowed, rejection, err := l.ReserveTokenChain(ctx, TenantQuotaLevels(chain), amount)
	if rejection == nil {
		return res, allowed, "", err
	}
	return res, allowed, rejection.TenantID, err
}

func (r *TenantChainReservation) Reconcile(ctx context.Context, actual int64) error {
	if r == nil || r.amount <= 0 || actual < 0 {
		return nil
	}
	if !r.settled.CompareAndSwap(false, true) {
		return nil
	}
	return r.adjust(ctx, actual-r.amount)
}

func (r *TenantChainReservation) Refund(ctx context.Context) error {
	if r == nil || r.amount <= 0 {
		return nil
	}
	if !r.settled.CompareAndSwap(false, true) {
		return nil
	}
	return r.adjust(ctx, -r.amount)
}

// adjust applies a signed delta to every level's buckets. Increases are plain
// increments; decreases go through safeDecrScript so that a bucket which has
// already expired or been drained is never driven negative, nor resurrected
// without a TTL.
func (r *TenantChainReservation) adjust(ctx context.Context, delta int64) error {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		pipe := r.rdb.TxPipeline()
		for _, key := range r.keys {
			pipe.IncrBy(ctx, key, delta)
		}
		_, err := pipe.Exec(ctx)
		return err
	}
	for _, key := range r.keys {
		if err := safeDecrScript.Run(ctx, r.rdb, []string{key}, -delta).Err(); err != nil {
			return err
		}
	}
	return nil
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
		// Roll the optimistic increment back. safeDecrScript rather than a plain
		// DECRBY: the minute bucket may have expired in between, and a bare
		// decrement would recreate it negative and without a TTL.
		_ = safeDecrScript.Run(ctx, l.rdb, []string{key}, amount).Err()
		return nil, false, nil
	}
	return &TPMReservation{rdb: l.rdb, key: key, amount: amount}, true, nil
}

func (r *TPMReservation) Reconcile(ctx context.Context, actual int64) error {
	if r == nil || r.amount <= 0 || actual < 0 {
		return nil
	}
	if !r.settled.CompareAndSwap(false, true) {
		return nil
	}
	return r.adjust(ctx, actual-r.amount)
}

func (r *TPMReservation) Refund(ctx context.Context) error {
	if r == nil || r.amount <= 0 {
		return nil
	}
	if !r.settled.CompareAndSwap(false, true) {
		return nil
	}
	return r.adjust(ctx, -r.amount)
}

// adjust mirrors TenantQuotaReservation.adjust for the single minute-bucket key.
func (r *TPMReservation) adjust(ctx context.Context, delta int64) error {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		_, err := r.rdb.IncrBy(ctx, r.key, delta).Result()
		return err
	}
	return safeDecrScript.Run(ctx, r.rdb, []string{r.key}, -delta).Err()
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
