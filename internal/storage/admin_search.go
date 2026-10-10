package storage

import (
	"context"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

type RequestLogFilter struct {
	Limit     int
	Offset    int
	Model     string
	Provider  string
	Status    int
	RequestID string
	Failover  int // -1 any, 0 false, 1 true
}

func (p *Postgres) SearchRequestLogs(ctx context.Context, f RequestLogFilter) ([]model.RequestLogAdmin, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT
	request_id,
	COALESCE(tenant_id::text,''),
	COALESCE(api_key_id::text,''),
	COALESCE(client_ip,''),
	COALESCE(requested_model,''),
	COALESCE(actual_model,''),
	COALESCE(provider,''),
	COALESCE(upstream_model,''),
	COALESCE(route_priority,0),
	COALESCE(attempts,0),
	COALESCE(failover,false),
	stream,
	COALESCE(input_tokens,0),
	COALESCE(output_tokens,0),
	COALESCE(total_tokens,0),
	COALESCE(latency_ms,0),
	COALESCE(status_code,0),
	COALESCE(error_code,''),
	created_at
FROM gateway_request_log
WHERE ($1='' OR requested_model=$1)
  AND ($2='' OR COALESCE(provider,'')=$2)
  AND ($3=0 OR status_code=$3)
  AND ($4='' OR request_id=$4)
  AND ($5 < 0 OR COALESCE(failover,false)=($5=1))
ORDER BY id DESC
LIMIT $6 OFFSET $7
`, f.Model, f.Provider, f.Status, f.RequestID, f.Failover, f.Limit, f.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RequestLogAdmin
	for rows.Next() {
		var v model.RequestLogAdmin
		if err := rows.Scan(
			&v.RequestID, &v.TenantID, &v.APIKeyID, &v.ClientIP,
			&v.RequestedModel, &v.ActualModel, &v.Provider, &v.UpstreamModel,
			&v.RoutePriority, &v.Attempts, &v.Failover, &v.Stream,
			&v.InputTokens, &v.OutputTokens, &v.TotalTokens, &v.LatencyMS,
			&v.StatusCode, &v.ErrorCode, &v.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) ProviderStats(ctx context.Context) ([]model.ProviderStats, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT
	COALESCE(provider,'') AS provider,
	COUNT(*)::bigint,
	COALESCE(AVG(CASE WHEN status_code < 400 THEN 1.0 ELSE 0.0 END),0)::float8,
	COALESCE(AVG(latency_ms),0)::float8
FROM gateway_request_log
WHERE created_at >= date_trunc('day', NOW())
  AND COALESCE(provider,'') <> ''
GROUP BY provider
ORDER BY COUNT(*) DESC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProviderStats
	for rows.Next() {
		var v model.ProviderStats
		if err := rows.Scan(&v.Provider, &v.Requests, &v.SuccessRate, &v.AvgLatencyMS); err != nil {
			return nil, err
		}
		v.SuccessRate *= 100
		out = append(out, v)
	}
	return out, rows.Err()
}
