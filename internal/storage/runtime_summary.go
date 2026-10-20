package storage

import (
	"context"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

func runtimeTotalsQuery(filter string) string {
	return `
SELECT COUNT(*)::bigint,
	COUNT(*) FILTER (WHERE status_code >= 400)::bigint,
	COALESCE(AVG(CASE WHEN status_code < 400 THEN 100.0 ELSE 0.0 END), 0)::float8,
	COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0)::float8,
	COUNT(*) FILTER (WHERE COALESCE(failover,false))::bigint,
	COALESCE(SUM(COALESCE(total_tokens,0)),0)::bigint
FROM gateway_request_log
WHERE ` + filter + ` AND created_at >= $2 AND created_at < $3`
}

func scanRuntimeTotals(row interface{ Scan(...any) error }, v *model.RuntimeSummaryTotals) error {
	return row.Scan(&v.Requests, &v.Errors, &v.SuccessRate, &v.P95LatencyMS, &v.Failover, &v.Tokens)
}

func runtimeRecentQuery(filter string) string {
	return `
SELECT request_id, created_at, COALESCE(requested_model,''), COALESCE(actual_model,''),
	COALESCE(provider,''), COALESCE(upstream_model,''), COALESCE(route_priority,0),
	COALESCE(status_code,0), COALESCE(error_code,''), COALESCE(attempts,0),
	COALESCE(failover,false), COALESCE(input_tokens,0), COALESCE(output_tokens,0),
	COALESCE(total_tokens,0), COALESCE(latency_ms,0)
FROM gateway_request_log
WHERE ` + filter + ` AND created_at >= $2 AND created_at < $3
ORDER BY created_at DESC, id DESC
LIMIT ` + "20"
}

func scanRuntimeRecent(rows pgx.Rows) ([]model.RuntimeRequest, error) {
	var out []model.RuntimeRequest
	for rows.Next() {
		var v model.RuntimeRequest
		if err := rows.Scan(&v.RequestID, &v.CreatedAt, &v.RequestedModel, &v.ActualModel,
			&v.Provider, &v.UpstreamModel, &v.RoutePriority, &v.StatusCode, &v.ErrorCode,
			&v.Attempts, &v.Failover, &v.InputTokens, &v.OutputTokens, &v.TotalTokens, &v.LatencyMS); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) ModelRuntimeSummary(ctx context.Context, modelID string, from, to time.Time) (model.ModelRuntimeSummary, error) {
	var out model.ModelRuntimeSummary
	if err := p.Pool.QueryRow(ctx, `SELECT alias FROM gateway_model WHERE id=$1::uuid`, modelID).Scan(&out.Alias); err != nil {
		return out, err
	}
	out.From, out.To = from, to
	if err := scanRuntimeTotals(p.Pool.QueryRow(ctx, runtimeTotalsQuery("requested_model=$1"), out.Alias, from, to), &out.Totals); err != nil {
		return out, err
	}
	rows, err := p.Pool.Query(ctx, runtimeRecentQuery("requested_model=$1"), out.Alias, from, to)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Recent, err = scanRuntimeRecent(rows)
	if out.Recent == nil {
		out.Recent = []model.RuntimeRequest{}
	}
	return out, err
}

func (p *Postgres) ProviderRuntimeSummary(ctx context.Context, providerID string, from, to time.Time) (model.ProviderRuntimeSummary, error) {
	var out model.ProviderRuntimeSummary
	if err := p.Pool.QueryRow(ctx, `SELECT name FROM gateway_provider WHERE id=$1::uuid`, providerID).Scan(&out.Provider); err != nil {
		return out, err
	}
	out.From, out.To = from, to
	if err := scanRuntimeTotals(p.Pool.QueryRow(ctx, runtimeTotalsQuery("provider=$1"), out.Provider, from, to), &out.Totals); err != nil {
		return out, err
	}
	if err := p.Pool.QueryRow(ctx, `
SELECT
	COUNT(*) FILTER (WHERE outcome='served')::bigint,
	COUNT(*) FILTER (WHERE outcome='failed')::bigint,
	COUNT(*) FILTER (WHERE outcome='skipped')::bigint
FROM gateway_request_attempt
WHERE provider=$1 AND created_at >= $2 AND created_at < $3
`, out.Provider, from, to).Scan(&out.Attempts.Served, &out.Attempts.Failed, &out.Attempts.Skipped); err != nil {
		return out, err
	}
	rows, err := p.Pool.Query(ctx, runtimeRecentQuery("provider=$1"), out.Provider, from, to)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Recent, err = scanRuntimeRecent(rows)
	if out.Recent == nil {
		out.Recent = []model.RuntimeRequest{}
	}
	return out, err
}
