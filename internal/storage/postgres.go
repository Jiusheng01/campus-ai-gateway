package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct{ Pool *pgxpool.Pool }

func OpenPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return &Postgres{Pool: p}, nil
}

func (p *Postgres) Close() { p.Pool.Close() }

func (p *Postgres) FindAPIKeyByDigest(ctx context.Context, digest string) (model.APIKey, error) {
	q := `SELECT k.id::text, k.tenant_id::text, k.name, k.key_prefix, k.key_digest, k.status, k.expire_at,
k.allowed_models, k.allowed_ips, COALESCE(k.route_group,''),
t.status, t.daily_token_limit, t.monthly_token_limit, t.monthly_request_limit,
t.rpm_limit, t.tpm_limit, t.max_concurrency
FROM gateway_api_key k
JOIN gateway_tenant t ON t.id=k.tenant_id
WHERE k.key_digest=$1
LIMIT 1`
	var k model.APIKey
	var m, i []byte
	err := p.Pool.QueryRow(ctx, q, digest).Scan(
		&k.ID,
		&k.TenantID,
		&k.Name,
		&k.KeyPrefix,
		&k.KeyDigest,
		&k.Status,
		&k.ExpireAt,
		&m,
		&i,
		&k.RouteGroup,
		&k.TenantStatus,
		&k.DailyTokenLimit,
		&k.MonthlyTokenLimit,
		&k.MonthlyRequestLimit,
		&k.RPMLimit,
		&k.TPMLimit,
		&k.MaxConcurrency,
	)
	if err != nil {
		return model.APIKey{}, err
	}
	_ = json.Unmarshal(m, &k.AllowedModels)
	_ = json.Unmarshal(i, &k.AllowedIPs)
	return k, nil
}

// InsertRequestAttempts records the Routing Attempts of one request.
//
// Best-effort by contract: attempt rows are Request Record data (ADR-0004), so a
// write that fails is a small, accepted loss, not a reason to fail the request
// that already answered. It is a single multi-row insert so a request's chain
// lands in one statement.
func (p *Postgres) InsertRequestAttempts(ctx context.Context, requestID string, attempts []model.RequestAttempt) error {
	if len(attempts) == 0 {
		return nil
	}
	const q = `
INSERT INTO gateway_request_attempt(
	request_id, attempt_no, provider, upstream_model, outcome, status_code, latency_ms, error_kind
)
SELECT $1, a.attempt_no, NULLIF(a.provider,''), NULLIF(a.upstream_model,''), a.outcome,
       NULLIF(a.status_code,0), a.latency_ms, NULLIF(a.error_kind,'')
FROM unnest($2::int[], $3::text[], $4::text[], $5::text[], $6::int[], $7::bigint[], $8::text[])
     AS a(attempt_no, provider, upstream_model, outcome, status_code, latency_ms, error_kind)
`
	nos := make([]int32, len(attempts))
	providers := make([]string, len(attempts))
	upstreams := make([]string, len(attempts))
	outcomes := make([]string, len(attempts))
	statuses := make([]int32, len(attempts))
	latencies := make([]int64, len(attempts))
	kinds := make([]string, len(attempts))
	for i, a := range attempts {
		nos[i] = int32(a.AttemptNo)
		providers[i] = a.Provider
		upstreams[i] = a.UpstreamModel
		outcomes[i] = a.Outcome
		statuses[i] = int32(a.StatusCode)
		latencies[i] = a.LatencyMS
		kinds[i] = a.ErrorKind
	}
	_, err := p.Pool.Exec(ctx, q, requestID, nos, providers, upstreams, outcomes, statuses, latencies, kinds)
	return err
}

// RequestAttempts returns one request's attempts in attempt order.
func (p *Postgres) RequestAttempts(ctx context.Context, requestID string) ([]model.RequestAttempt, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT attempt_no, COALESCE(provider,''), COALESCE(upstream_model,''), outcome,
       COALESCE(status_code,0), latency_ms, COALESCE(error_kind,'')
FROM gateway_request_attempt
WHERE request_id=$1
ORDER BY attempt_no
`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RequestAttempt
	for rows.Next() {
		var a model.RequestAttempt
		if err := rows.Scan(&a.AttemptNo, &a.Provider, &a.UpstreamModel, &a.Outcome, &a.StatusCode, &a.LatencyMS, &a.ErrorKind); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ShadowReport compares the direct plane's shadow decisions with what
// ThinkWatch actually served, over the window from `since`.
//
// The comparison is the direct plane's LEADER (attempt_no = 1 of its shadow
// chain) against the channel ThinkWatch reported. ThinkWatch's `provider` is the
// channel that WON, so a request it failed over on would disagree with the
// direct plane's first pick even when both planes chose the same first line;
// those requests are excluded, leaving the ones where a disagreement is a real
// difference of first choice.
//
// The two planes name channels independently — the direct plane knows a
// gateway_provider.name, ThinkWatch reports its own resolved channel name — so a
// disagreement is evidence to read, not a verdict.
//
// sampled counts requests (leader rows), not attempt rows, so it is the same
// denominator `mismatched` is a share of.
func (p *Postgres) ShadowReport(ctx context.Context, since time.Time, limit int) (sampled, mismatched int64, rows []model.ShadowComparison, err error) {
	const leaders = `
	FROM gateway_request_attempt a
	WHERE a.outcome='shadow' AND a.attempt_no=1 AND a.created_at >= $1`
	const comparable = `
	FROM gateway_request_attempt a
	JOIN gateway_request_log l ON l.request_id = a.request_id
	WHERE a.outcome='shadow' AND a.attempt_no=1 AND a.created_at >= $1
	  AND COALESCE(l.attempts,1) <= 1`

	if err := p.Pool.QueryRow(ctx, `
SELECT
	(SELECT COUNT(*)`+leaders+`),
	(SELECT COUNT(*)`+comparable+` AND COALESCE(a.provider,'') <> COALESCE(l.provider,''))
`, since).Scan(&sampled, &mismatched); err != nil {
		return 0, 0, nil, err
	}

	r, err := p.Pool.Query(ctx, `
SELECT a.request_id, COALESCE(a.provider,''), COALESCE(a.upstream_model,''),
       COALESCE(l.provider,''), a.created_at`+comparable+`
  AND COALESCE(a.provider,'') <> COALESCE(l.provider,'')
ORDER BY a.created_at DESC
LIMIT $2
`, since, limit)
	if err != nil {
		return 0, 0, nil, err
	}
	defer r.Close()
	for r.Next() {
		var c model.ShadowComparison
		if err := r.Scan(&c.RequestID, &c.DirectProvider, &c.DirectUpstreamModel, &c.ThinkWatchProvider, &c.CreatedAt); err != nil {
			return 0, 0, nil, err
		}
		rows = append(rows, c)
	}
	return sampled, mismatched, rows, r.Err()
}

func (p *Postgres) TouchAPIKey(ctx context.Context, id string) {
	_, _ = p.Pool.Exec(ctx, "UPDATE gateway_api_key SET last_used_at=NOW() WHERE id=$1", id)
}

func (p *Postgres) InsertRequestLog(ctx context.Context, log model.RequestLog) error {
	const q = `
INSERT INTO gateway_request_log(
	request_id,
	tenant_id,
	api_key_id,
	client_ip,
	requested_model,
	actual_model,
	provider,
	upstream_model,
	route_priority,
	attempts,
	failover,
	stream,
	input_tokens,
	output_tokens,
	total_tokens,
	latency_ms,
	status_code,
	error_code
) VALUES (
	$1,
	NULLIF($2, '')::uuid,
	NULLIF($3, '')::uuid,
	NULLIF($4, ''),
	NULLIF($5, ''),
	NULLIF($6, ''),
	NULLIF($7, ''),
	NULLIF($8, ''),
	NULLIF($9, 0),
	NULLIF($10, 0),
	$11,
	$12,
	$13,
	$14,
	$15,
	$16,
	$17,
	NULLIF($18, '')
)
ON CONFLICT (request_id) DO NOTHING
`
	_, err := p.Pool.Exec(
		ctx,
		q,
		log.RequestID,
		log.TenantID,
		log.APIKeyID,
		log.ClientIP,
		log.RequestedModel,
		log.ActualModel,
		log.Provider,
		log.UpstreamModel,
		log.RoutePriority,
		log.Attempts,
		log.Failover,
		log.Stream,
		log.InputTokens,
		log.OutputTokens,
		log.TotalTokens,
		log.LatencyMS,
		log.StatusCode,
		log.ErrorCode,
	)
	return err
}
