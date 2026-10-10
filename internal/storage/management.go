package storage

import (
	"context"
	"encoding/json"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

func (p *Postgres) ListProviders(ctx context.Context) ([]model.Provider, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT id::text, name, base_url, protocol, credential_env, status, created_at, updated_at
FROM gateway_provider
ORDER BY created_at DESC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Provider
	for rows.Next() {
		var v model.Provider
		if err := rows.Scan(&v.ID, &v.Name, &v.BaseURL, &v.Protocol, &v.CredentialEnv, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateProvider(ctx context.Context, v model.Provider) (model.Provider, error) {
	err := p.Pool.QueryRow(ctx, `
INSERT INTO gateway_provider(name, base_url, protocol, credential_env, status)
VALUES ($1, $2, $3, $4, $5)
RETURNING id::text, name, base_url, protocol, credential_env, status, created_at, updated_at
`, v.Name, v.BaseURL, v.Protocol, v.CredentialEnv, v.Status).Scan(
		&v.ID, &v.Name, &v.BaseURL, &v.Protocol, &v.CredentialEnv, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	return v, err
}

func (p *Postgres) UpdateProvider(ctx context.Context, id string, v model.Provider) (model.Provider, error) {
	err := p.Pool.QueryRow(ctx, `
UPDATE gateway_provider
SET name=$2, base_url=$3, protocol=$4, credential_env=$5, status=$6, updated_at=NOW()
WHERE id=$1::uuid
RETURNING id::text, name, base_url, protocol, credential_env, status, created_at, updated_at
`, id, v.Name, v.BaseURL, v.Protocol, v.CredentialEnv, v.Status).Scan(
		&v.ID, &v.Name, &v.BaseURL, &v.Protocol, &v.CredentialEnv, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	return v, err
}

func (p *Postgres) ListModels(ctx context.Context) ([]model.ModelConfig, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT m.id::text, m.alias, m.provider_id::text, p.name, m.upstream_model, m.status, m.created_at, m.updated_at
FROM gateway_model m
JOIN gateway_provider p ON p.id=m.provider_id
ORDER BY m.created_at DESC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.ModelConfig
	for rows.Next() {
		var v model.ModelConfig
		if err := rows.Scan(&v.ID, &v.Alias, &v.ProviderID, &v.ProviderName, &v.UpstreamModel, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateModel(ctx context.Context, v model.ModelConfig) (model.ModelConfig, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return v, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
INSERT INTO gateway_model(alias, provider_id, upstream_model, status)
VALUES ($1, $2::uuid, $3, $4)
RETURNING id::text, alias, provider_id::text, upstream_model, status, created_at, updated_at
`, v.Alias, v.ProviderID, v.UpstreamModel, v.Status).Scan(
		&v.ID, &v.Alias, &v.ProviderID, &v.UpstreamModel, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		return v, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO gateway_model_route(model_id, provider_id, upstream_model, priority, weight, timeout_ms, status)
VALUES ($1::uuid, $2::uuid, $3, 1, 100, 60000, 1)
`, v.ID, v.ProviderID, v.UpstreamModel); err != nil {
		return v, err
	}
	if err := tx.QueryRow(ctx, "SELECT name FROM gateway_provider WHERE id=$1::uuid", v.ProviderID).Scan(&v.ProviderName); err != nil {
		return v, err
	}
	if err := tx.Commit(ctx); err != nil {
		return v, err
	}
	return v, nil
}

func (p *Postgres) UpdateModel(ctx context.Context, id string, v model.ModelConfig) (model.ModelConfig, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return v, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
UPDATE gateway_model
SET alias=$2, status=$3, updated_at=NOW()
WHERE id=$1::uuid
`, id, v.Alias, v.Status); err != nil {
		return v, err
	}

	var routeID string
	err = tx.QueryRow(ctx, `
SELECT id::text
FROM gateway_model_route
WHERE model_id=$1::uuid
ORDER BY CASE WHEN status=1 THEN 0 ELSE 1 END, priority ASC, weight DESC, created_at ASC
LIMIT 1
FOR UPDATE
`, id).Scan(&routeID)
	if err == pgx.ErrNoRows {
		if _, err := tx.Exec(ctx, `
INSERT INTO gateway_model_route(model_id, provider_id, upstream_model, priority, weight, timeout_ms, status)
VALUES ($1::uuid, $2::uuid, $3, 1, 100, 60000, 1)
`, id, v.ProviderID, v.UpstreamModel); err != nil {
			return v, err
		}
	} else if err != nil {
		return v, err
	} else {
		if _, err := tx.Exec(ctx, `
UPDATE gateway_model_route
SET provider_id=$2::uuid, upstream_model=$3, updated_at=NOW()
WHERE id=$1::uuid
`, routeID, v.ProviderID, v.UpstreamModel); err != nil {
			return v, err
		}
	}

	if err := syncLegacyModelRoute(ctx, tx, id); err != nil {
		return v, err
	}
	err = tx.QueryRow(ctx, `
SELECT m.id::text, m.alias, m.provider_id::text, p.name, m.upstream_model, m.status, m.created_at, m.updated_at
FROM gateway_model m
JOIN gateway_provider p ON p.id=m.provider_id
WHERE m.id=$1::uuid
`, id).Scan(
		&v.ID, &v.Alias, &v.ProviderID, &v.ProviderName, &v.UpstreamModel, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		return v, err
	}
	if err := tx.Commit(ctx); err != nil {
		return v, err
	}
	return v, nil
}

func (p *Postgres) ListTenants(ctx context.Context) ([]model.Tenant, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT id::text, code, name, status
FROM gateway_tenant
ORDER BY name
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Tenant
	for rows.Next() {
		var v model.Tenant
		if err := rows.Scan(&v.ID, &v.Code, &v.Name, &v.Status); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateTenant(ctx context.Context, v model.Tenant) (model.Tenant, error) {
	err := p.Pool.QueryRow(ctx, `
INSERT INTO gateway_tenant(code, name, status)
VALUES ($1, $2, $3)
RETURNING id::text, code, name, status
`, v.Code, v.Name, v.Status).Scan(&v.ID, &v.Code, &v.Name, &v.Status)
	return v, err
}

func (p *Postgres) UpdateTenant(ctx context.Context, id string, v model.Tenant) (model.Tenant, error) {
	err := p.Pool.QueryRow(ctx, `
UPDATE gateway_tenant
SET code=$2, name=$3, status=$4, updated_at=NOW()
WHERE id=$1::uuid
RETURNING id::text, code, name, status
`, id, v.Code, v.Name, v.Status).Scan(&v.ID, &v.Code, &v.Name, &v.Status)
	return v, err
}

func (p *Postgres) ListAPIKeys(ctx context.Context) ([]model.APIKeyAdmin, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT
	k.id::text, k.tenant_id::text, t.code, t.name, k.name, k.key_prefix, k.status,
	k.expire_at, k.last_used_at, k.allowed_models, k.allowed_ips,
	k.rpm_limit, k.tpm_limit, k.max_concurrency, COALESCE(k.route_group,''),
	k.created_at
FROM gateway_api_key k
JOIN gateway_tenant t ON t.id=k.tenant_id
ORDER BY k.created_at DESC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.APIKeyAdmin
	for rows.Next() {
		var v model.APIKeyAdmin
		var modelsJSON, ipsJSON []byte
		if err := rows.Scan(
			&v.ID, &v.TenantID, &v.TenantCode, &v.TenantName, &v.Name, &v.KeyPrefix, &v.Status,
			&v.ExpireAt, &v.LastUsedAt, &modelsJSON, &ipsJSON,
			&v.RPMLimit, &v.TPMLimit, &v.MaxConcurrency, &v.RouteGroup, &v.CreatedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(modelsJSON, &v.AllowedModels)
		_ = json.Unmarshal(ipsJSON, &v.AllowedIPs)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateAPIKey(ctx context.Context, v model.APIKeyAdmin, digest string) (model.APIKeyAdmin, error) {
	modelsJSON, _ := json.Marshal(v.AllowedModels)
	ipsJSON, _ := json.Marshal(v.AllowedIPs)
	err := p.Pool.QueryRow(ctx, `
INSERT INTO gateway_api_key(
	tenant_id, name, key_prefix, key_digest, status, expire_at,
	allowed_models, allowed_ips, rpm_limit, tpm_limit, max_concurrency, route_group
)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10, $11, NULLIF($12,''))
RETURNING id::text, created_at
`,
		v.TenantID, v.Name, v.KeyPrefix, digest, v.Status, v.ExpireAt,
		string(modelsJSON), string(ipsJSON), v.RPMLimit, v.TPMLimit, v.MaxConcurrency, v.RouteGroup,
	).Scan(&v.ID, &v.CreatedAt)
	if err != nil {
		return v, err
	}
	_ = p.Pool.QueryRow(ctx, "SELECT code, name FROM gateway_tenant WHERE id=$1::uuid", v.TenantID).Scan(&v.TenantCode, &v.TenantName)
	return v, nil
}

func (p *Postgres) UpdateAPIKey(ctx context.Context, id string, v model.APIKeyAdmin) (model.APIKeyAdmin, error) {
	modelsJSON, _ := json.Marshal(v.AllowedModels)
	ipsJSON, _ := json.Marshal(v.AllowedIPs)
	err := p.Pool.QueryRow(ctx, `
UPDATE gateway_api_key
SET tenant_id=$2::uuid,
	name=$3,
	status=$4,
	expire_at=$5,
	allowed_models=$6::jsonb,
	allowed_ips=$7::jsonb,
	rpm_limit=$8,
	tpm_limit=$9,
	max_concurrency=$10,
	route_group=NULLIF($11,''),
	updated_at=NOW()
WHERE id=$1::uuid
RETURNING key_prefix, created_at, last_used_at
`,
		id, v.TenantID, v.Name, v.Status, v.ExpireAt,
		string(modelsJSON), string(ipsJSON), v.RPMLimit, v.TPMLimit, v.MaxConcurrency, v.RouteGroup,
	).Scan(&v.KeyPrefix, &v.CreatedAt, &v.LastUsedAt)
	if err != nil {
		return v, err
	}
	v.ID = id
	_ = p.Pool.QueryRow(ctx, "SELECT code, name FROM gateway_tenant WHERE id=$1::uuid", v.TenantID).Scan(&v.TenantCode, &v.TenantName)
	return v, nil
}

func (p *Postgres) ListRequestLogs(ctx context.Context, limit int) ([]model.RequestLogAdmin, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT
	request_id,
	COALESCE(tenant_id::text,''),
	COALESCE(api_key_id::text,''),
	COALESCE(client_ip,''),
	COALESCE(requested_model,''),
	COALESCE(actual_model,''),
	COALESCE(provider,''),
	stream,
	COALESCE(input_tokens,0),
	COALESCE(output_tokens,0),
	COALESCE(total_tokens,0),
	COALESCE(latency_ms,0),
	COALESCE(status_code,0),
	COALESCE(error_code,''),
	created_at
FROM gateway_request_log
ORDER BY id DESC
LIMIT $1
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.RequestLogAdmin
	for rows.Next() {
		var v model.RequestLogAdmin
		if err := rows.Scan(
			&v.RequestID, &v.TenantID, &v.APIKeyID, &v.ClientIP,
			&v.RequestedModel, &v.ActualModel, &v.Provider, &v.Stream,
			&v.InputTokens, &v.OutputTokens, &v.TotalTokens, &v.LatencyMS,
			&v.StatusCode, &v.ErrorCode, &v.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) DashboardStats(ctx context.Context) (model.DashboardStats, error) {
	var v model.DashboardStats
	err := p.Pool.QueryRow(ctx, `
SELECT
	COUNT(*)::bigint,
	COALESCE(SUM(total_tokens),0)::bigint,
	COUNT(*) FILTER (WHERE status_code >= 400)::bigint,
	COALESCE(AVG(latency_ms),0)::float8
FROM gateway_request_log
WHERE created_at >= date_trunc('day', NOW())
`).Scan(&v.TodayRequests, &v.TodayTokens, &v.TodayErrors, &v.AvgLatencyMS)
	return v, err
}
