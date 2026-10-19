package storage

import (
	"context"
	"encoding/json"

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
	// The tenant's limit columns are deliberately NOT selected here. They are
	// nullable (NULL = inherit), so they cannot be turned into a number without
	// walking the ancestor chain -- and the chain is exactly what t.path
	// encodes. Selecting them here would hand the admission path a NULL that
	// reads as "unlimited" and quietly bypass every parent's cap.
	q := `SELECT k.id::text, k.tenant_id::text, t.path, k.name, k.key_prefix, k.key_digest, k.status, k.expire_at,
k.allowed_models, k.allowed_ips, COALESCE(k.route_group,''),
t.status
FROM gateway_api_key k
JOIN gateway_tenant t ON t.id=k.tenant_id
WHERE k.key_digest=$1
LIMIT 1`
	var k model.APIKey
	var m, i []byte
	err := p.Pool.QueryRow(ctx, q, digest).Scan(
		&k.ID,
		&k.TenantID,
		&k.TenantPath,
		&k.Name,
		&k.KeyPrefix,
		&k.KeyDigest,
		&k.Status,
		&k.ExpireAt,
		&m,
		&i,
		&k.RouteGroup,
		&k.TenantStatus,
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
