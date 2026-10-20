package storage

import (
	"context"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// RouteConfigSnapshot is one consistent read of the routing configuration.
// Providers, models and routes are read in a single repeatable-read, read-only
// transaction, so a table built from them cannot mix two moments.
type RouteConfigSnapshot struct {
	Providers []model.Provider
	Models    []model.ModelConfig
	Routes    map[string][]model.ModelRoute
}

func (p *Postgres) RouteConfigSnapshot(ctx context.Context) (RouteConfigSnapshot, error) {
	var out RouteConfigSnapshot
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
SELECT `+providerColumns+`
FROM gateway_provider p
JOIN gateway_credential c ON c.id = p.credential_id
ORDER BY p.created_at DESC
`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v model.Provider
		if err := scanProviderRow(rows, &v); err != nil {
			rows.Close()
			return out, err
		}
		out.Providers = append(out.Providers, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()

	modelRows, err := tx.Query(ctx, `
SELECT id::text, alias, COALESCE(display_name,''), COALESCE(description,''), status,
	config_version, created_at, updated_at
FROM gateway_model
ORDER BY created_at DESC
`)
	if err != nil {
		return out, err
	}
	for modelRows.Next() {
		var v model.ModelConfig
		if err := modelRows.Scan(&v.ID, &v.Alias, &v.DisplayName, &v.Description, &v.Status,
			&v.ConfigVersion, &v.CreatedAt, &v.UpdatedAt); err != nil {
			modelRows.Close()
			return out, err
		}
		out.Models = append(out.Models, v)
	}
	if err := modelRows.Err(); err != nil {
		modelRows.Close()
		return out, err
	}
	modelRows.Close()

	out.Routes = make(map[string][]model.ModelRoute, len(out.Models))
	routeRows, err := tx.Query(ctx, `
SELECT r.id::text, r.model_id::text, m.alias, r.provider_id::text, p.name,
	r.upstream_model, r.priority, r.weight, r.timeout_ms, r.status, r.created_at, r.updated_at
FROM gateway_model_route r
JOIN gateway_model m ON m.id=r.model_id
JOIN gateway_provider p ON p.id=r.provider_id
ORDER BY r.priority ASC, r.weight DESC, r.created_at ASC
`)
	if err != nil {
		return out, err
	}
	for routeRows.Next() {
		var v model.ModelRoute
		if err := routeRows.Scan(&v.ID, &v.ModelID, &v.ModelAlias, &v.ProviderID, &v.ProviderName,
			&v.UpstreamModel, &v.Priority, &v.Weight, &v.TimeoutMS, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
			routeRows.Close()
			return out, err
		}
		out.Routes[v.ModelID] = append(out.Routes[v.ModelID], v)
	}
	if err := routeRows.Err(); err != nil {
		routeRows.Close()
		return out, err
	}
	routeRows.Close()

	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}
