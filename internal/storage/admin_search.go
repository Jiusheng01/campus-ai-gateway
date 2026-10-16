package storage

import (
	"context"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// RequestLogFilter narrows a request-log read.
//
// Every string and time field treats its zero value as "do not narrow on this
// dimension".
//
// Status and Failover are the exception, and the exception is a trap worth
// naming: they predate the range and identity filters and already had a
// meaning, so they keep their integer sentinels rather than becoming pointers.
// For Failover that means the zero value is 0 — "failover = false", the
// opposite of "any". A caller that wants every row must say Failover: -1. The
// only production caller, requestLogFilterFrom, always sets it explicitly for
// exactly this reason.
type RequestLogFilter struct {
	Limit     int
	Offset    int
	Model     string
	Provider  string
	Status    int // 0 any, otherwise the exact status_code
	RequestID string
	Failover  int // -1 any, 0 false, 1 true
	TenantID  string
	APIKeyID  string
	// From and To bound created_at. A zero time means "unbounded on that
	// side"; they are converted to SQL NULL so the predicate stays a plain
	// index-friendly comparison rather than a sentinel the planner has to
	// evaluate against every row.
	From time.Time
	To   time.Time
}

// requestLogColumns is the projection every request-log read shares. It exists
// so the search and the CSV export cannot disagree about what a row contains.
const requestLogColumns = `
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
FROM gateway_request_log`

// requestLogWhere is the predicate shared by the table, the summary and the
// export.
//
// It is one constant rather than three copies because the summary and the table
// are read side by side: if they drifted, the aggregate would describe a
// different set of rows than the list directly beneath it, and the operator
// would have no way to tell which number was wrong.
//
// The positions are fixed at $1..$9 so the fragment can be reused verbatim;
// each caller appends its own LIMIT/OFFSET after it. Every identity and time
// parameter is typed with an explicit cast so a NULL is unambiguous to the
// planner — `''::uuid` would raise instead of matching nothing.
const requestLogWhere = `
WHERE ($1='' OR requested_model=$1)
  AND ($2='' OR COALESCE(provider,'')=$2)
  AND ($3=0 OR status_code=$3)
  AND ($4='' OR request_id=$4)
  AND ($5 < 0 OR COALESCE(failover,false)=($5=1))
  AND ($6::uuid IS NULL OR tenant_id=$6::uuid)
  AND ($7::uuid IS NULL OR api_key_id=$7::uuid)
  AND ($8::timestamptz IS NULL OR created_at >= $8::timestamptz)
  AND ($9::timestamptz IS NULL OR created_at <= $9::timestamptz)`

// requestLogArgs returns the $1..$9 parameters requestLogWhere expects, in
// order. It is the only place the positional contract is spelled out, so a
// filter field added to the struct cannot be wired into one query and forgotten
// in another.
func requestLogArgs(f RequestLogFilter) []any {
	return []any{
		f.Model,
		f.Provider,
		f.Status,
		f.RequestID,
		f.Failover,
		optionalString(f.TenantID),
		optionalString(f.APIKeyID),
		optionalTime(f.From),
		optionalTime(f.To),
	}
}

// optionalString maps the empty string — the filter's "unset" — onto SQL NULL.
// The distinction matters for the uuid columns: an empty string is not a
// matchable value, it is a type error waiting to happen.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (p *Postgres) SearchRequestLogs(ctx context.Context, f RequestLogFilter) ([]model.RequestLogAdmin, error) {
	args := append(requestLogArgs(f), f.Limit, f.Offset)
	rows, err := p.Pool.Query(ctx, requestLogColumns+requestLogWhere+`
ORDER BY id DESC
LIMIT $10 OFFSET $11
`, args...)
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

// SummarizeRequestLogs aggregates the whole filtered set.
//
// The aggregate is deliberately over the same predicate as the page, and never
// over the returned rows: an operator filtering to "today" wants today's error
// count, not the error count of the fifty rows currently on screen.
func (p *Postgres) SummarizeRequestLogs(ctx context.Context, f RequestLogFilter) (model.RequestLogSummary, error) {
	var v model.RequestLogSummary
	err := p.Pool.QueryRow(ctx, `
SELECT
	COUNT(*)::bigint,
	COUNT(*) FILTER (WHERE COALESCE(status_code,0) >= 400)::bigint,
	COALESCE(SUM(COALESCE(input_tokens,0)),0)::bigint,
	COALESCE(SUM(COALESCE(output_tokens,0)),0)::bigint,
	COALESCE(SUM(COALESCE(total_tokens,0)),0)::bigint,
	COALESCE(AVG(latency_ms),0)::float8,
	COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms),0)::float8,
	COUNT(*) FILTER (WHERE COALESCE(failover,false))::bigint
FROM gateway_request_log`+requestLogWhere,
		requestLogArgs(f)...,
	).Scan(
		&v.Requests, &v.Errors, &v.InputTokens, &v.OutputTokens, &v.TotalTokens,
		&v.AvgLatencyMS, &v.P95LatencyMS, &v.FailoverCount,
	)
	return v, err
}

// RequestLogSeries returns the traffic trend in fixed-width buckets.
//
// The bucket boundary is computed in the caller's UTC offset, not in UTC,
// because a "day" has to mean the operator's day: truncating in UTC moves every
// boundary for a deployment in UTC+8 and would file the eight hours after
// local midnight under the previous bar. The offset arrives as an integer
// because the browser is the only party that knows which zone the person
// reading the chart is in.
//
// labelFormat is applied by the database so the label is a wall-clock string
// the chart can render verbatim. Returning a timestamp instead would let the
// browser shift it a second time, which is the same bug in a different place.
//
// The caller is responsible for the range: [from, to) is a half-open interval
// so consecutive windows never double-count the boundary row.
func (p *Postgres) RequestLogSeries(
	ctx context.Context, from, to time.Time, unit string, offsetMinutes int, labelFormat string,
) ([]model.RequestLogBucket, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT
	to_char(
		date_trunc($1::text, (created_at + make_interval(mins => $2::int)) AT TIME ZONE 'UTC'),
		$3::text
	) AS bucket,
	COUNT(*)::bigint,
	COALESCE(SUM(COALESCE(total_tokens,0)),0)::bigint,
	COUNT(*) FILTER (WHERE COALESCE(status_code,0) >= 400)::bigint,
	COALESCE(AVG(latency_ms),0)::float8,
	COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY latency_ms),0)::float8
FROM gateway_request_log
WHERE created_at >= $4::timestamptz AND created_at < $5::timestamptz
GROUP BY bucket
ORDER BY bucket
`, unit, offsetMinutes, labelFormat, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RequestLogBucket
	for rows.Next() {
		var v model.RequestLogBucket
		if err := rows.Scan(&v.Bucket, &v.Requests, &v.Tokens, &v.Errors, &v.AvgLatencyMS, &v.P95LatencyMS); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RequestLogFacets returns the tenants and API keys that appear in the log.
//
// Both queries start from the log and join outward, so a tenant with no traffic
// never appears: the filter is a list of values that can actually narrow the
// table, not a copy of the management tables. The DISTINCT is what the new
// (tenant_id, created_at) and (api_key_id, created_at) indexes make cheap —
// without them this would be a full scan of the log for every page load.
func (p *Postgres) RequestLogFacets(ctx context.Context) (model.RequestLogFacets, error) {
	var out model.RequestLogFacets

	tenantRows, err := p.Pool.Query(ctx, `
SELECT DISTINCT t.id::text, t.code, t.name
FROM gateway_request_log l
JOIN gateway_tenant t ON t.id = l.tenant_id
ORDER BY t.name
`)
	if err != nil {
		return out, err
	}
	defer tenantRows.Close()
	for tenantRows.Next() {
		var v model.TenantFacet
		if err := tenantRows.Scan(&v.ID, &v.Code, &v.Name); err != nil {
			return out, err
		}
		out.Tenants = append(out.Tenants, v)
	}
	if err := tenantRows.Err(); err != nil {
		return out, err
	}

	keyRows, err := p.Pool.Query(ctx, `
SELECT DISTINCT k.id::text, k.name, k.key_prefix, k.tenant_id::text
FROM gateway_request_log l
JOIN gateway_api_key k ON k.id = l.api_key_id
ORDER BY k.name
`)
	if err != nil {
		return out, err
	}
	defer keyRows.Close()
	for keyRows.Next() {
		var v model.APIKeyFacet
		if err := keyRows.Scan(&v.ID, &v.Name, &v.KeyPrefix, &v.TenantID); err != nil {
			return out, err
		}
		out.Keys = append(out.Keys, v)
	}
	return out, keyRows.Err()
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
