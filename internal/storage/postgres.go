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

// FindAPIKeyByDigest resolves a presented key to its owner and the owner's
// tenant path.
//
// The tenant's quota columns are deliberately NOT selected: they are nullable
// (NULL = inherit) and can only be turned into concrete numbers by walking the
// ancestor chain, which the caller does via FindTenantChain. The path is
// returned instead because it is the cheap handle that makes that walk a single
// indexed lookup.
func (p *Postgres) FindAPIKeyByDigest(ctx context.Context, digest string) (model.APIKey, error) {
	q := `SELECT k.id::text, k.tenant_id::text, t.path, k.name, k.key_prefix, k.key_digest, k.status, k.expire_at,
k.allowed_models, k.allowed_ips, k.rpm_limit, k.tpm_limit, k.max_concurrency, COALESCE(k.route_group,'')
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
		&k.RPMLimit,
		&k.TPMLimit,
		&k.MaxConcurrency,
		&k.RouteGroup,
	)
	if err != nil {
		return model.APIKey{}, err
	}
	_ = json.Unmarshal(m, &k.AllowedModels)
	_ = json.Unmarshal(i, &k.AllowedIPs)
	return k, nil
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
