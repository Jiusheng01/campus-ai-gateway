package storage

import (
	"context"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// Personal usage is read over a different predicate from the administrator's
// request-log queries, so it gets its own small set of functions instead of a
// new field on RequestLogFilter. The owner is a bound parameter in every one of
// them; there is no caller-controlled tenant or key dimension to widen.
//
// All three use the same half-open window [from, to) as the trend endpoint, so
// the totals, the per-model rows and the chart never describe different sets of
// rows at a bucket boundary.

// PersonalUsageTotals aggregates the member's own Request Records.
//
// Success is the complement of the administrator summary's error definition:
// status_code >= 400 is a failure (a NULL or zero status is not an API error),
// so successes + failures = requests. Tokens are summed per column so input,
// output and total can be reported separately; total is not derived in Go.
func (p *Postgres) PersonalUsageTotals(
	ctx context.Context, ownerID string, from, to time.Time,
) (model.ConsoleUsageTotals, error) {
	var v model.ConsoleUsageTotals
	err := p.Pool.QueryRow(ctx, `
SELECT
	COUNT(*)::bigint,
	COUNT(*) FILTER (WHERE COALESCE(status_code,0) < 400)::bigint,
	COUNT(*) FILTER (WHERE COALESCE(status_code,0) >= 400)::bigint,
	COALESCE(SUM(COALESCE(input_tokens,0)),0)::bigint,
	COALESCE(SUM(COALESCE(output_tokens,0)),0)::bigint,
	COALESCE(SUM(COALESCE(total_tokens,0)),0)::bigint
FROM gateway_request_log
WHERE owner_user_id = $1::uuid
  AND created_at >= $2::timestamptz
  AND created_at < $3::timestamptz
`, ownerID, from, to).Scan(
		&v.Requests, &v.Successes, &v.Failures,
		&v.InputTokens, &v.OutputTokens, &v.TotalTokens,
	)
	if err != nil {
		return model.ConsoleUsageTotals{}, err
	}
	if v.Requests > 0 {
		v.ErrorRate = float64(v.Failures) * 100 / float64(v.Requests)
	}
	return v, nil
}

// PersonalUsageByModel groups the member's own traffic by the requested model.
//
// requested_model is the alias the member sent, which is the alias they can
// recognise and act on. actual_model can differ after routing; the alias is the
// stable answer to "which of my models is spending the budget".
func (p *Postgres) PersonalUsageByModel(
	ctx context.Context, ownerID string, from, to time.Time,
) ([]model.ConsoleUsageModel, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT
	COALESCE(requested_model,'') AS alias,
	COUNT(*)::bigint,
	COALESCE(SUM(COALESCE(input_tokens,0)),0)::bigint,
	COALESCE(SUM(COALESCE(output_tokens,0)),0)::bigint,
	COALESCE(SUM(COALESCE(total_tokens,0)),0)::bigint
FROM gateway_request_log
WHERE owner_user_id = $1::uuid
  AND created_at >= $2::timestamptz
  AND created_at < $3::timestamptz
GROUP BY alias
ORDER BY 5 DESC, alias
`, ownerID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.ConsoleUsageModel, 0, 8)
	for rows.Next() {
		var v model.ConsoleUsageModel
		if err := rows.Scan(
			&v.Alias, &v.Requests, &v.InputTokens, &v.OutputTokens, &v.TotalTokens,
		); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// The trend series is shared with the administrator path as
// RequestLogSeries(ownerID, ...). The personal endpoint passes the session's
// account id, so the same bucket arithmetic owns both charts and a member can
// never read a row outside the snapshot that query binds.
