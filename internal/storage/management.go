package storage

import (
	"context"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
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
	err := p.Pool.QueryRow(ctx, `
INSERT INTO gateway_model(alias, provider_id, upstream_model, status)
VALUES ($1, $2::uuid, $3, $4)
RETURNING id::text, alias, provider_id::text, upstream_model, status, created_at, updated_at
`, v.Alias, v.ProviderID, v.UpstreamModel, v.Status).Scan(
		&v.ID, &v.Alias, &v.ProviderID, &v.UpstreamModel, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		return v, err
	}
	_ = p.Pool.QueryRow(ctx, "SELECT name FROM gateway_provider WHERE id=$1::uuid", v.ProviderID).Scan(&v.ProviderName)
	return v, nil
}

func (p *Postgres) UpdateModel(ctx context.Context, id string, v model.ModelConfig) (model.ModelConfig, error) {
	err := p.Pool.QueryRow(ctx, `
UPDATE gateway_model
SET alias=$2, provider_id=$3::uuid, upstream_model=$4, status=$5, updated_at=NOW()
WHERE id=$1::uuid
RETURNING id::text, alias, provider_id::text, upstream_model, status, created_at, updated_at
`, id, v.Alias, v.ProviderID, v.UpstreamModel, v.Status).Scan(
		&v.ID, &v.Alias, &v.ProviderID, &v.UpstreamModel, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		return v, err
	}
	_ = p.Pool.QueryRow(ctx, "SELECT name FROM gateway_provider WHERE id=$1::uuid", v.ProviderID).Scan(&v.ProviderName)
	return v, nil
}
