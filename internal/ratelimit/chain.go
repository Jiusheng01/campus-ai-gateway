package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/redis/go-redis/v9"
)

// chainBudgetScript checks a set of counters and increments them only if every
// one of them would stay within its own limit.
//
// The check-then-write shape is what makes the chain atomic: one Lua call is one
// Redis command from the outside, so no other client can slip a reservation in
// between "every level has room" and "every level has been charged". That is
// also why nothing here needs a rollback path -- a rejection writes nothing at
// all.
//
//	KEYS[i]        one counter
//	ARGV[1]        amount to add
//	ARGV[1+i]      TTL in seconds for KEYS[i]
//	ARGV[1+n+i]    limit for KEYS[i]; <= 0 means "not enforced"
//	returns        0 when admitted, else the 1-based index of the first
//	               rejecting key
//
// The TTL is set only on the write that creates the counter (it goes from 0 to
// exactly amount). Refreshing it on every write would keep a busy tenant's
// bucket alive forever, and a counter that never expires never resets.
var chainBudgetScript = redis.NewScript(`
local n = #KEYS
local amount = tonumber(ARGV[1])

for i = 1, n do
  local limit = tonumber(ARGV[1 + n + i])
  if limit > 0 then
    local current = tonumber(redis.call('GET', KEYS[i]) or '0')
    if current + amount > limit then
      return i
    end
  end
end

for i = 1, n do
  local next = redis.call('INCRBY', KEYS[i], amount)
  if next == amount then
    redis.call('EXPIRE', KEYS[i], ARGV[1 + i])
  end
end
return 0
`)

// QuotaRejection names the level that refused a reservation.
//
// A 429 that cannot say whose budget ran out is the difference between a quota
// system operators trust and one they route around, so the level travels with
// the answer instead of being logged and lost.
type QuotaRejection struct {
	// Label is human readable: "租户:计算机学院" or "模型:campus-smart(全校)".
	Label string
	// TenantID is the uuid of the rejecting tenant. Empty when the level is not
	// tenant-scoped.
	TenantID string
	// ModelAlias is set when the rejecting level is a model pool.
	ModelAlias string
}

// chainBucket is one counter participating in a chain check.
type chainBucket struct {
	// Key is the Redis counter.
	Key string
	// TTL is how long the counter lives. Each bucket carries its own because a
	// chain mixes day and month buckets, whose windows are different lengths.
	TTL time.Duration
	// Limit is the cap this bucket enforces. A bucket is only created for a
	// limit that is actually set, so this is always > 0.
	Limit int64
	// Label names the level in a rejection.
	Label      string
	TenantID   string
	ModelAlias string
}

// ChainReservation is a set of counters that were charged together. Settling it
// adjusts every counter by the same delta, which keeps the chain consistent:
// a request that estimated 100 tokens and used 140 adds 40 to the leaf and to
// every ancestor at once.
type ChainReservation struct {
	rdb     *redis.Client
	buckets []chainBucket
	amount  int64
}

// Reconcile replaces the reserved amount with the actual one.
func (r *ChainReservation) Reconcile(ctx context.Context, actual int64) error {
	if r == nil || r.amount <= 0 || actual < 0 {
		return nil
	}
	return r.adjust(ctx, actual-r.amount)
}

// Refund releases the whole reservation.
func (r *ChainReservation) Refund(ctx context.Context) error {
	if r == nil || r.amount <= 0 {
		return nil
	}
	return r.adjust(ctx, -r.amount)
}

func (r *ChainReservation) adjust(ctx context.Context, delta int64) error {
	if delta == 0 || len(r.buckets) == 0 {
		return nil
	}
	pipe := r.rdb.TxPipeline()
	for _, b := range r.buckets {
		if delta > 0 {
			pipe.IncrBy(ctx, b.Key, delta)
			continue
		}
		pipe.DecrBy(ctx, b.Key, -delta)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// runChain executes the script over the buckets. It returns the index of the
// bucket that rejected the request, or -1 when it was admitted.
func (l *Limiter) runChain(ctx context.Context, buckets []chainBucket, amount int64) (int, error) {
	if len(buckets) == 0 {
		return -1, nil
	}
	keys := make([]string, 0, len(buckets))
	args := make([]any, 0, 1+2*len(buckets))
	args = append(args, amount)
	for _, b := range buckets {
		keys = append(keys, b.Key)
		args = append(args, int64(b.TTL/time.Second))
	}
	for _, b := range buckets {
		args = append(args, b.Limit)
	}
	idx, err := chainBudgetScript.Run(ctx, l.rdb, keys, args...).Int()
	if err != nil {
		return -1, err
	}
	if idx == 0 {
		return -1, nil
	}
	return idx - 1, nil
}

// limitOrZero reads a three-state limit as a number the bucket builder can test.
// nil (inherit) and 0 (explicitly unlimited) both come back as 0, which means
// "this level does not cap anything" -- the same reading model.Unlimited gives.
func limitOrZero(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
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

// rejection builds the answer for the bucket at idx.
func rejection(buckets []chainBucket, idx int) *QuotaRejection {
	if idx < 0 || idx >= len(buckets) {
		return nil
	}
	b := buckets[idx]
	return &QuotaRejection{Label: b.Label, TenantID: b.TenantID, ModelAlias: b.ModelAlias}
}

// AllowTenantRequestChain enforces the monthly request quota of every level.
//
// Only the levels that declare a cap contribute a counter. A level that inherits
// (NULL) is skipped because the ancestor it inherits from is already in the
// list, and a level that says 0 is skipped because it explicitly opted out --
// neither can widen an ancestor's cap, since the ancestor's own counter is
// still checked.
func (l *Limiter) AllowTenantRequestChain(ctx context.Context, chain []model.TenantQuota) (bool, *QuotaRejection, error) {
	month := tenantMonthBucket()
	buckets := make([]chainBucket, 0, len(chain))
	for _, q := range chain {
		limit := limitOrZero(q.MonthlyRequestLimit)
		if limit <= 0 {
			continue
		}
		buckets = append(buckets, chainBucket{
			Key:      quotaRequestsKey(q.TenantID, month),
			TTL:      quotaMonthlyTTL,
			Limit:    limit,
			Label:    "租户:" + tenantLabel(q),
			TenantID: q.TenantID,
		})
	}
	if len(buckets) == 0 {
		return true, nil, nil
	}
	// A flushed cache must not read as "no usage yet", so probe before charging.
	if err := l.ensureQuotaCounters(ctx, buckets[0].Key, buckets[0].TTL); err != nil {
		return false, nil, err
	}
	idx, err := l.runChain(ctx, buckets, 1)
	if err != nil {
		return false, nil, err
	}
	return idx < 0, rejection(buckets, idx), nil
}

// AllowRPMChain enforces the per-minute request rate of every level.
func (l *Limiter) AllowRPMChain(ctx context.Context, chain []model.TenantQuota) (bool, *QuotaRejection, error) {
	minute := minuteBucket()
	buckets := make([]chainBucket, 0, len(chain))
	for _, q := range chain {
		limit := limitOrZero(q.RPMLimit)
		if limit <= 0 {
			continue
		}
		buckets = append(buckets, chainBucket{
			Key:      fmt.Sprintf("gw:rl:tenant:%s:rpm:%s", q.TenantID, minute),
			TTL:      120 * time.Second,
			Limit:    limit,
			Label:    "租户:" + tenantLabel(q),
			TenantID: q.TenantID,
		})
	}
	if len(buckets) == 0 {
		return true, nil, nil
	}
	idx, err := l.runChain(ctx, buckets, 1)
	if err != nil {
		return false, nil, err
	}
	return idx < 0, rejection(buckets, idx), nil
}

// ReserveTokenChain reserves tokens against the day and month bucket of every
// level, plus every model pool that applies.
//
// Reserving at every level is what makes the hierarchy inclusive: a leaf's usage
// lands on its own counters and on all of its ancestors' at once, so a parent's
// total is the sum of its subtree without any separate ledger or periodic
// rollup.
func (l *Limiter) ReserveTokenChain(
	ctx context.Context,
	chain []model.TenantQuota,
	modelLevels []model.ModelQuotaLevel,
	amount int64,
) (*ChainReservation, bool, *QuotaRejection, error) {
	if amount <= 0 {
		return nil, true, nil, nil
	}

	day := tenantDayBucket()
	month := tenantMonthBucket()
	buckets := make([]chainBucket, 0, 2*(len(chain)+len(modelLevels)))

	for _, q := range chain {
		daily := limitOrZero(q.DailyTokenLimit)
		monthly := limitOrZero(q.MonthlyTokenLimit)
		label := "租户:" + tenantLabel(q)
		// Each dimension gets its own bucket only when it is actually capped.
		// Creating a counter for an uncapped dimension would accumulate state
		// nothing reads, and would make a later cap change start from a
		// non-zero baseline.
		if daily > 0 {
			buckets = append(buckets, chainBucket{
				Key:      quotaDailyTokensKey(q.TenantID, day),
				TTL:      quotaDailyTTL,
				Limit:    daily,
				Label:    label,
				TenantID: q.TenantID,
			})
		}
		if monthly > 0 {
			buckets = append(buckets, chainBucket{
				Key:      quotaMonthlyTokensKey(q.TenantID, month),
				TTL:      quotaMonthlyTTL,
				Limit:    monthly,
				Label:    label,
				TenantID: q.TenantID,
			})
		}
	}

	for _, lv := range modelLevels {
		daily := limitOrZero(lv.DailyTokenLimit)
		monthly := limitOrZero(lv.MonthlyTokenLimit)
		if daily > 0 {
			buckets = append(buckets, chainBucket{
				Key:        modelQuotaKey(lv.TenantID, lv.ModelAlias, "day", day),
				TTL:        quotaDailyTTL,
				Limit:      daily,
				Label:      lv.Label,
				TenantID:   lv.TenantID,
				ModelAlias: lv.ModelAlias,
			})
		}
		if monthly > 0 {
			buckets = append(buckets, chainBucket{
				Key:        modelQuotaKey(lv.TenantID, lv.ModelAlias, "month", month),
				TTL:        quotaMonthlyTTL,
				Limit:      monthly,
				Label:      lv.Label,
				TenantID:   lv.TenantID,
				ModelAlias: lv.ModelAlias,
			})
		}
	}

	if len(buckets) == 0 {
		return nil, true, nil, nil
	}
	if err := l.ensureQuotaCounters(ctx, buckets[0].Key, buckets[0].TTL); err != nil {
		return nil, false, nil, err
	}
	idx, err := l.runChain(ctx, buckets, amount)
	if err != nil {
		return nil, false, nil, err
	}
	if idx >= 0 {
		return nil, false, rejection(buckets, idx), nil
	}
	return &ChainReservation{rdb: l.rdb, buckets: buckets, amount: amount}, true, nil, nil
}

// ReserveTPMChain reserves tokens against the per-minute token rate of every
// level.
func (l *Limiter) ReserveTPMChain(ctx context.Context, chain []model.TenantQuota, amount int64) (*ChainReservation, bool, *QuotaRejection, error) {
	if amount <= 0 {
		return nil, true, nil, nil
	}
	minute := minuteBucket()
	buckets := make([]chainBucket, 0, len(chain))
	for _, q := range chain {
		limit := limitOrZero(q.TPMLimit)
		if limit <= 0 {
			continue
		}
		buckets = append(buckets, chainBucket{
			Key:      fmt.Sprintf("gw:rl:tenant:%s:tpm:%s", q.TenantID, minute),
			TTL:      120 * time.Second,
			Limit:    limit,
			Label:    "租户:" + tenantLabel(q),
			TenantID: q.TenantID,
		})
	}
	if len(buckets) == 0 {
		return nil, true, nil, nil
	}
	idx, err := l.runChain(ctx, buckets, amount)
	if err != nil {
		return nil, false, nil, err
	}
	if idx >= 0 {
		return nil, false, rejection(buckets, idx), nil
	}
	return &ChainReservation{rdb: l.rdb, buckets: buckets, amount: amount}, true, nil, nil
}

// AcquireConcurrencyChain takes one Concurrency Slot at every level that caps
// concurrency.
//
// Unlike the counters above this is not one atomic call, because each level's
// slots live in its own sorted set. The levels are acquired in order and
// released in reverse on failure, which can leave a slot held for the duration
// of one attempt in a race -- acceptable here because a slot is a lease that
// reclaims itself, and because the alternative (a cross-key transaction) would
// serialise every request in the campus on one lock.
func (l *Limiter) AcquireConcurrencyChain(ctx context.Context, chain []model.TenantQuota) (func(), bool, *QuotaRejection, error) {
	var releases []func()
	releaseAll := func() {
		for _, r := range releases {
			r()
		}
	}

	for _, q := range chain {
		limit := limitOrZero(q.MaxConcurrency)
		if limit <= 0 {
			continue
		}
		release, allowed, err := l.AcquireConcurrencySlot(ctx, q.TenantID, limit)
		if err != nil {
			releaseAll()
			return nil, false, nil, err
		}
		if !allowed {
			releaseAll()
			return nil, false, &QuotaRejection{
				Label:    "租户:" + tenantLabel(q),
				TenantID: q.TenantID,
			}, nil
		}
		releases = append(releases, release)
	}
	return releaseAll, true, nil, nil
}

// modelQuotaKey is the counter a model quota lives under. The three shapes are
// separate namespaces so a tenant's total usage and its per-model usage can
// never collide, and so a model pool is not mistaken for a tenant named after a
// model:
//
//	tenant quota        gw:quota:tenant:<id>:tokens:<period>:<bucket>
//	system model pool   gw:quota:model:<alias>:tokens:<period>:<bucket>
//	tenant model quota  gw:quota:tenant:<id>:model:<alias>:tokens:<period>:<bucket>
//
// The first shape is byte-for-byte the key the gateway wrote before the model
// axis existed, so an existing deployment's counters keep working.
func modelQuotaKey(tenantID, alias, period, bucket string) string {
	switch {
	case tenantID == "":
		return fmt.Sprintf("gw:quota:model:%s:tokens:%s:%s", alias, period, bucket)
	default:
		return fmt.Sprintf("gw:quota:tenant:%s:model:%s:tokens:%s:%s", tenantID, alias, period, bucket)
	}
}
