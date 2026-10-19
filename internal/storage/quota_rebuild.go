package storage

import (
	"context"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// DailySettledQuota aggregates the settled request log by tenant and UTC day in
// [from, to), so a Quota Counter rebuild can reconstruct the Redis cache from
// the durable truth.
//
// The day is bucketed in UTC with the same "YYYYMMDD" scheme the limiter uses
// for its day keys (tenantDayBucket), and the month counters a rebuild derives
// are "YYYYMM" of the same instant. Bucketing in UTC here matters: the request
// log's created_at is a timestamptz, and to_char would otherwise format it in
// the database session's zone, shifting the daily boundary away from the one
// the gate's live counters use.
func (p *Postgres) DailySettledQuota(ctx context.Context, from, to time.Time) ([]model.QuotaSettledRow, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT tenant_id::text,
       to_char(created_at AT TIME ZONE 'UTC', 'YYYYMMDD') AS day,
       COUNT(*)::bigint,
       COALESCE(SUM(COALESCE(total_tokens,0)),0)::bigint
FROM gateway_request_log
WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz
GROUP BY tenant_id, day
ORDER BY tenant_id, day
`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.QuotaSettledRow
	for rows.Next() {
		var v model.QuotaSettledRow
		if err := rows.Scan(&v.TenantID, &v.Day, &v.Requests, &v.Tokens); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
