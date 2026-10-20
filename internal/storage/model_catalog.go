package storage

import (
	"context"
	"errors"
	"strings"

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

// ErrAliasExists means the requested external alias already exists with a
// different provider/upstream binding. Appending is a deliberate operation, not
// a retry: the caller must say append_to_existing explicitly.
var ErrAliasExists = errors.New("model alias already exists with a different binding")

// PublishResult is the shared outcome of publishing a Provider Model under an
// external alias. Catalog open is the same operation with alias = upstream and
// append allowed.
type PublishResult struct {
	Model        model.ModelConfig `json:"model"`
	Route        model.ModelRoute  `json:"route"`
	Created      bool              `json:"created"`
	RouteCreated bool              `json:"route_created"`
	Version      int64             `json:"version"`
}

const publishedModelColumns = `id::text, alias, COALESCE(display_name,''), COALESCE(description,''), status,
	config_version, created_at, updated_at`

// OpenProviderModel makes one channel's Provider Model callable to clients
// using the upstream name as the external alias. It is the compatibility entry
// point over the shared publish service.
func (p *Postgres) OpenProviderModel(ctx context.Context, providerModelID string) (model.CatalogOpenResult, error) {
	res, err := p.PublishProviderModel(ctx, providerModelID, "", "", "", true)
	if err != nil {
		return model.CatalogOpenResult{}, err
	}
	return model.CatalogOpenResult{
		Model: res.Model, Route: res.Route, Created: res.Created, RouteCreated: res.RouteCreated,
	}, nil
}

// PublishProviderModel creates or appends one external binding atomically.
//
// A new alias creates the model and its first route together. An existing
// alias with the same binding is an idempotent no-op. An existing alias with a
// different binding is refused unless append is explicit; then the new route
// joins the next priority band and the model version advances.
func (p *Postgres) PublishProviderModel(ctx context.Context, providerModelID, alias, displayName, description string, appendExisting bool) (PublishResult, error) {
	var out PublishResult

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
	alias = strings.TrimSpace(alias)
	if alias == "" {
		alias = upstream
	}
	displayName = strings.TrimSpace(displayName)
	description = strings.TrimSpace(description)

	// INSERT ... ON CONFLICT makes concurrent first-publishes agree on one model.
	err = tx.QueryRow(ctx, `
INSERT INTO gateway_model(alias, display_name, description, status)
VALUES ($1, $2, $3, 1)
ON CONFLICT (alias) DO NOTHING
RETURNING `+publishedModelColumns+`
`, alias, displayName, description).Scan(
		&out.Model.ID, &out.Model.Alias, &out.Model.DisplayName, &out.Model.Description,
		&out.Model.Status, &out.Model.ConfigVersion, &out.Model.CreatedAt, &out.Model.UpdatedAt,
	)
	switch {
	case err == nil:
		out.Created = true
	case errors.Is(err, pgx.ErrNoRows):
		if err := tx.QueryRow(ctx, `
SELECT `+publishedModelColumns+`
FROM gateway_model
WHERE alias=$1
FOR UPDATE
`, alias).Scan(
			&out.Model.ID, &out.Model.Alias, &out.Model.DisplayName, &out.Model.Description,
			&out.Model.Status, &out.Model.ConfigVersion, &out.Model.CreatedAt, &out.Model.UpdatedAt,
		); err != nil {
			return out, err
		}
	default:
		return out, err
	}

	// The binding may already exist. It is the unique index that makes an exact
	// repeat a no-op, not a pre-check.
	var existing model.ModelRoute
	err = tx.QueryRow(ctx, `
SELECT id::text, model_id::text, provider_id::text, upstream_model, priority, weight, timeout_ms, status, created_at, updated_at
FROM gateway_model_route
WHERE model_id=$1::uuid AND provider_id=$2::uuid AND upstream_model=$3
`, out.Model.ID, providerID, upstream).Scan(
		&existing.ID, &existing.ModelID, &existing.ProviderID, &existing.UpstreamModel,
		&existing.Priority, &existing.Weight, &existing.TimeoutMS, &existing.Status,
		&existing.CreatedAt, &existing.UpdatedAt,
	)
	switch {
	case err == nil:
		existing.ModelAlias = out.Model.Alias
		existing.ProviderName = providerName
		out.Route = existing
		out.Version = out.Model.ConfigVersion
		if err := tx.Commit(ctx); err != nil {
			return out, err
		}
		return out, nil
	case errors.Is(err, pgx.ErrNoRows):
		if !out.Created && !appendExisting {
			return out, ErrAliasExists
		}
	default:
		return out, err
	}

	var priority int
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(MAX(priority), 0) + 1 FROM gateway_model_route WHERE model_id=$1::uuid
`, out.Model.ID).Scan(&priority); err != nil {
		return out, err
	}
	if out.Created {
		priority = 1
	}
	err = tx.QueryRow(ctx, `
INSERT INTO gateway_model_route(model_id, provider_id, upstream_model, priority, weight, timeout_ms, status)
VALUES ($1::uuid, $2::uuid, $3, $4, 100, 60000, 1)
RETURNING id::text, model_id::text, provider_id::text, upstream_model, priority, weight, timeout_ms, status, created_at, updated_at
`, out.Model.ID, providerID, upstream, priority).Scan(
		&out.Route.ID, &out.Route.ModelID, &out.Route.ProviderID, &out.Route.UpstreamModel,
		&out.Route.Priority, &out.Route.Weight, &out.Route.TimeoutMS, &out.Route.Status,
		&out.Route.CreatedAt, &out.Route.UpdatedAt,
	)
	if err != nil {
		return out, err
	}
	out.RouteCreated = true
	out.Route.ModelAlias = out.Model.Alias
	out.Route.ProviderName = providerName

	if !out.Created {
		if err := tx.QueryRow(ctx, `
UPDATE gateway_model
SET config_version=config_version+1, updated_at=NOW()
WHERE id=$1::uuid
RETURNING config_version
`, out.Model.ID).Scan(&out.Model.ConfigVersion); err != nil {
			return out, err
		}
	}
	out.Version = out.Model.ConfigVersion
	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}
