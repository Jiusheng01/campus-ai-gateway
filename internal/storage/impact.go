package storage

import (
	"context"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

func (p *Postgres) ProviderImpact(ctx context.Context, providerID string) (model.ProviderImpact, error) {
	var out model.ProviderImpact
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gateway_provider WHERE id=$1::uuid)`, providerID).Scan(&exists); err != nil {
		return out, err
	}
	if !exists {
		return out, pgx.ErrNoRows
	}
	if err := tx.QueryRow(ctx, `
SELECT COUNT(*)::int FROM gateway_model_route WHERE provider_id=$1::uuid
`, providerID).Scan(&out.Routes); err != nil {
		return out, err
	}
	if err := tx.QueryRow(ctx, `
SELECT COUNT(*)::int FROM gateway_provider_model WHERE provider_id=$1::uuid
`, providerID).Scan(&out.ProviderModels); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `
SELECT m.id::text, m.alias,
	COUNT(r.id)::int AS routes_to_provider,
	(COUNT(r.id) FILTER (WHERE r.provider_id=$1::uuid))::int AS provider_routes,
	(SELECT COUNT(*)::int FROM gateway_model_route x WHERE x.model_id=m.id) AS total_routes
FROM gateway_model m
JOIN gateway_model_route r ON r.model_id=m.id
WHERE r.provider_id=$1::uuid
GROUP BY m.id, m.alias
ORDER BY m.alias
`, providerID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var m model.ProviderImpactModel
		var providerRoutes, totalRoutes int
		var alias string
		if err := rows.Scan(&m.ModelID, &alias, &m.RoutesToProvider, &providerRoutes, &totalRoutes); err != nil {
			return out, err
		}
		m.Alias = alias
		m.RemainingRoutes = totalRoutes - providerRoutes
		m.Orphaned = m.RemainingRoutes == 0
		if m.Orphaned {
			out.OrphanedModels++
		}
		out.AffectedModels = append(out.AffectedModels, m)
	}
	if out.AffectedModels == nil {
		out.AffectedModels = []model.ProviderImpactModel{}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}

func (p *Postgres) ModelImpact(ctx context.Context, modelID string) (model.ModelImpact, error) {
	var out model.ModelImpact
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)

	if err := tx.QueryRow(ctx, `SELECT alias FROM gateway_model WHERE id=$1::uuid`, modelID).Scan(&out.Alias); err != nil {
		return out, err
	}
	if err := tx.QueryRow(ctx, `
SELECT
	COUNT(*) FILTER (WHERE r.status=1)::int,
	COUNT(DISTINCT r.provider_id) FILTER (WHERE r.status=1 AND p.status=1)::int
FROM gateway_model_route r
JOIN gateway_provider p ON p.id=r.provider_id
WHERE r.model_id=$1::uuid
`, modelID).Scan(&out.EnabledRoutes, &out.EnabledProviders); err != nil {
		return out, err
	}
	if err := tx.QueryRow(ctx, `
SELECT
	COUNT(*) FILTER (WHERE allowed_models @> to_jsonb($1::text) AND NOT (allowed_models @> '["*"]'::jsonb))::int,
	COUNT(*) FILTER (WHERE allowed_models @> '["*"]'::jsonb)::int
FROM gateway_api_key
`, out.Alias).Scan(&out.ExplicitKeys, &out.WildcardKeys); err != nil {
		return out, err
	}
	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}
