package storage

import (
	"context"
	"errors"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// ModelCatalog groups every enabled channel's Provider Models by upstream name.
//
// It is one query rather than a per-name follow-up: the catalog is a single
// view, and a row whose "providers" list came from a second query could show a
// name that was removed between the two reads. The LEFT JOINs carry the two
// answers the view needs — whether a Virtual Model with this alias exists and
// whether this channel already has the matching Route — without turning an
// unopened model into a missing row.
//
// Disabled channels are left out: their names are configured but not currently
// callable, and the catalog's promise is "what can I use", not "what is stored".
func (p *Postgres) ModelCatalog(ctx context.Context) ([]model.CatalogModel, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT
	pm.id::text,
	pm.provider_id::text,
	p.name,
	pm.upstream_model,
	COALESCE(m.id::text, ''),
	COALESCE(r.id::text, '')
FROM gateway_provider_model pm
JOIN gateway_provider p ON p.id = pm.provider_id
LEFT JOIN gateway_model m ON m.alias = pm.upstream_model
LEFT JOIN gateway_model_route r
	ON r.model_id = m.id
	AND r.provider_id = pm.provider_id
	AND r.upstream_model = pm.upstream_model
WHERE pm.status = 1 AND p.status = 1
ORDER BY pm.upstream_model ASC, p.name ASC, pm.id ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.CatalogModel
	index := make(map[string]int)
	for rows.Next() {
		var providerModelID, providerID, providerName, upstream, modelID, routeID string
		if err := rows.Scan(&providerModelID, &providerID, &providerName, &upstream, &modelID, &routeID); err != nil {
			return nil, err
		}
		i, ok := index[upstream]
		if !ok {
			i = len(out)
			index[upstream] = i
			out = append(out, model.CatalogModel{UpstreamModel: upstream})
		}
		if modelID != "" {
			out[i].Opened = true
		}
		out[i].Providers = append(out[i].Providers, model.CatalogProvider{
			ID:              providerID,
			Name:            providerName,
			ProviderModelID: providerModelID,
			Opened:          routeID != "",
			ModelID:         modelID,
			RouteID:         routeID,
		})
	}
	return out, rows.Err()
}

// OpenProviderModel makes one channel's Provider Model callable to clients.
//
// If no Virtual Model has this alias, the model and its first Route are created
// together; if one exists, only the Route is appended. Either way the route
// carries the (model, provider, upstream) binding that the unique index
// enforces, so opening the same model again is a no-op rather than a duplicate
// line.
//
// An appended Route goes into the next priority band, so opening a second
// channel for the same name produces the cross-vendor failover the gateway
// exists for instead of silently splitting traffic between the two.
func (p *Postgres) OpenProviderModel(ctx context.Context, providerModelID string) (model.CatalogOpenResult, error) {
	var out model.CatalogOpenResult

	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)

	var providerID, providerName, upstream string
	if err := tx.QueryRow(ctx, `
SELECT pm.provider_id::text, p.name, pm.upstream_model
FROM gateway_provider_model pm
JOIN gateway_provider p ON p.id = pm.provider_id
WHERE pm.id=$1::uuid AND pm.status=1 AND p.status=1
FOR UPDATE OF pm
`, providerModelID).Scan(&providerID, &providerName, &upstream); err != nil {
		return out, err
	}

	// Create the Virtual Model unless the alias is taken. ON CONFLICT DO NOTHING
	// makes two concurrent opens of different channels agree on one model: the
	// loser blocks until the winner commits, then reads the row it did not
	// create. Locking the existing row serialises the Route priority decision
	// that follows.
	err = tx.QueryRow(ctx, `
INSERT INTO gateway_model(alias, status)
VALUES ($1, 1)
ON CONFLICT (alias) DO NOTHING
RETURNING id::text, alias, COALESCE(display_name,''), COALESCE(description,''), status,
	config_version, created_at, updated_at
`, upstream).Scan(&out.Model.ID, &out.Model.Alias, &out.Model.DisplayName, &out.Model.Description,
		&out.Model.Status, &out.Model.ConfigVersion, &out.Model.CreatedAt, &out.Model.UpdatedAt)
	switch {
	case err == nil:
		out.Created = true
	case errors.Is(err, pgx.ErrNoRows):
		if err := tx.QueryRow(ctx, `
SELECT id::text, alias, COALESCE(display_name,''), COALESCE(description,''), status,
	config_version, created_at, updated_at
FROM gateway_model
WHERE alias=$1
FOR UPDATE
`, upstream).Scan(&out.Model.ID, &out.Model.Alias, &out.Model.DisplayName, &out.Model.Description,
			&out.Model.Status, &out.Model.ConfigVersion, &out.Model.CreatedAt, &out.Model.UpdatedAt); err != nil {
			return out, err
		}
	default:
		return out, err
	}

	// The first Route of a model is the primary line (priority 1). Every route
	// opened afterwards is a backup. Computing the band inside the transaction
	// is what keeps two concurrent appends from both claiming priority 2.
	var priority int
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(MAX(priority), 0) + 1
FROM gateway_model_route
WHERE model_id=$1::uuid
`, out.Model.ID).Scan(&priority); err != nil {
		return out, err
	}

	err = tx.QueryRow(ctx, `
INSERT INTO gateway_model_route(model_id, provider_id, upstream_model, priority, weight, timeout_ms, status)
VALUES ($1::uuid, $2::uuid, $3, $4, 100, 60000, 1)
ON CONFLICT (model_id, provider_id, upstream_model) DO NOTHING
RETURNING id::text, model_id::text, provider_id::text, upstream_model, priority, weight, timeout_ms, status, created_at, updated_at
`, out.Model.ID, providerID, upstream, priority).Scan(
		&out.Route.ID, &out.Route.ModelID, &out.Route.ProviderID, &out.Route.UpstreamModel,
		&out.Route.Priority, &out.Route.Weight, &out.Route.TimeoutMS, &out.Route.Status,
		&out.Route.CreatedAt, &out.Route.UpdatedAt,
	)
	switch {
	case err == nil:
		out.RouteCreated = true
	case errors.Is(err, pgx.ErrNoRows):
		if err := tx.QueryRow(ctx, `
SELECT id::text, model_id::text, provider_id::text, upstream_model, priority, weight, timeout_ms, status, created_at, updated_at
FROM gateway_model_route
WHERE model_id=$1::uuid AND provider_id=$2::uuid AND upstream_model=$3
`, out.Model.ID, providerID, upstream).Scan(
			&out.Route.ID, &out.Route.ModelID, &out.Route.ProviderID, &out.Route.UpstreamModel,
			&out.Route.Priority, &out.Route.Weight, &out.Route.TimeoutMS, &out.Route.Status,
			&out.Route.CreatedAt, &out.Route.UpdatedAt,
		); err != nil {
			return out, err
		}
	default:
		return out, err
	}
	out.Route.ModelAlias = out.Model.Alias
	out.Route.ProviderName = providerName

	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}
