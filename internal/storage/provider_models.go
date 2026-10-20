package storage

import (
	"context"
	"errors"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// providerModelColumns is the projection every Provider Model read and write
// acknowledgement shares.
const providerModelColumns = `id::text, provider_id::text, upstream_model, source, status, created_at, updated_at`

// ProviderByID reads one channel, including the resolution data its data plane
// needs. It exists for the actions that operate on a single channel rather than
// the whole list — currently the Provider Model list and its upstream refresh.
func (p *Postgres) ProviderByID(ctx context.Context, id string) (model.Provider, error) {
	var v model.Provider
	row := p.Pool.QueryRow(ctx, `
SELECT `+providerColumns+`
FROM gateway_provider p
JOIN gateway_credential c ON c.id = p.credential_id
WHERE p.id=$1::uuid
`, id)
	if err := scanProviderRow(row, &v); err != nil {
		return model.Provider{}, err
	}
	return v, nil
}

// ListProviderModels returns a channel's Provider Model list, alphabetically.
//
// A channel that does not exist is pgx.ErrNoRows rather than an empty list: the
// API has to tell "this channel has no models yet" apart from "there is no such
// channel".
func (p *Postgres) ListProviderModels(ctx context.Context, providerID string) ([]model.ProviderModel, error) {
	var exists bool
	if err := p.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM gateway_provider WHERE id=$1::uuid)", providerID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, pgx.ErrNoRows
	}
	rows, err := p.Pool.Query(ctx, `
SELECT `+providerModelColumns+`
FROM gateway_provider_model
WHERE provider_id=$1::uuid
ORDER BY upstream_model ASC, id ASC
`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.ProviderModel
	index := make(map[string]int)
	for rows.Next() {
		var v model.ProviderModel
		if err := rows.Scan(&v.ID, &v.ProviderID, &v.UpstreamModel, &v.Source, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		index[v.ID] = len(out)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	// One query for every link, not one per candidate: the relationship comes
	// from gateway_model_route, so disabled channels/models and manually entered
	// Routes are not hidden by the Model Catalog's filters.
	links, err := p.Pool.Query(ctx, `
SELECT pm.id::text, r.id::text, m.id::text, m.alias, r.status
FROM gateway_provider_model pm
JOIN gateway_model_route r
  ON r.provider_id = pm.provider_id AND r.upstream_model = pm.upstream_model
JOIN gateway_model m ON m.id = r.model_id
WHERE pm.provider_id=$1::uuid
ORDER BY pm.upstream_model, m.alias, r.priority
`, providerID)
	if err != nil {
		return nil, err
	}
	defer links.Close()
	for links.Next() {
		var providerModelID string
		var link model.ProviderModelLink
		if err := links.Scan(&providerModelID, &link.RouteID, &link.ModelID, &link.Alias, &link.RouteStatus); err != nil {
			return nil, err
		}
		if i, ok := index[providerModelID]; ok {
			out[i].Links = append(out[i].Links, link)
		}
	}
	return out, links.Err()
}

// SeedProviderModels adds names from a non-manual source, skipping any name the
// channel already lists. ON CONFLICT DO NOTHING is what makes a preset seed and
// an upstream refresh additive: an existing row keeps its original source, and
// a manual row is never rewritten or removed.
func (p *Postgres) SeedProviderModels(ctx context.Context, providerID string, names []string, source string) error {
	return seedProviderModels(ctx, p.Pool, providerID, names, source)
}

// MergeFetchedProviderModels merges upstream-discovered names into the list and
// reports how many were new. It is additive by construction: there is no DELETE
// here, so a refresh cannot destroy the existing list.
func (p *Postgres) MergeFetchedProviderModels(ctx context.Context, providerID string, names []string) (int, error) {
	tag, err := insertProviderModels(ctx, p.Pool, providerID, names, model.ProviderModelSourceFetched)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// AddProviderModel adds one name manually. It reports whether it created a row:
// a name already listed as a preset or fetched row is returned as it is, with
// its original source, rather than duplicated or relabelled.
func (p *Postgres) AddProviderModel(ctx context.Context, providerID, upstreamModel string) (model.ProviderModel, bool, error) {
	name := strings.TrimSpace(upstreamModel)
	var v model.ProviderModel
	err := p.Pool.QueryRow(ctx, `
INSERT INTO gateway_provider_model (provider_id, upstream_model, source, status)
VALUES ($1::uuid, $2, $3, 1)
ON CONFLICT (provider_id, upstream_model) DO NOTHING
RETURNING `+providerModelColumns,
		providerID, name, model.ProviderModelSourceManual,
	).Scan(&v.ID, &v.ProviderID, &v.UpstreamModel, &v.Source, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err == nil {
		return v, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return model.ProviderModel{}, false, err
	}
	// The name is already listed — as a preset, a fetched row or an earlier
	// manual addition. Return the row that exists, with the source it has.
	if err := p.Pool.QueryRow(ctx, `
SELECT `+providerModelColumns+`
FROM gateway_provider_model
WHERE provider_id=$1::uuid AND upstream_model=$2
`, providerID, name).Scan(&v.ID, &v.ProviderID, &v.UpstreamModel, &v.Source, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return model.ProviderModel{}, false, err
	}
	return v, false, nil
}

// DeleteProviderModel removes one entry from a channel's list. The model id is
// scoped to the provider so one channel's id cannot delete another's row.
func (p *Postgres) DeleteProviderModel(ctx context.Context, providerID, modelID string) error {
	tag, err := p.Pool.Exec(ctx, `
DELETE FROM gateway_provider_model
WHERE provider_id=$1::uuid AND id=$2::uuid
`, providerID, modelID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// execer is satisfied by both a pool and a transaction, so a seed can join the
// provider-create transaction and standalone refresh can run on its own.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func seedProviderModels(ctx context.Context, e execer, providerID string, names []string, source string) error {
	_, err := insertProviderModels(ctx, e, providerID, names, source)
	return err
}

func insertProviderModels(ctx context.Context, e execer, providerID string, names []string, source string) (pgconn.CommandTag, error) {
	clean := normaliseProviderModelNames(names)
	if len(clean) == 0 {
		return pgconn.CommandTag{}, nil
	}
	return e.Exec(ctx, `
INSERT INTO gateway_provider_model (provider_id, upstream_model, source, status)
SELECT $1::uuid, name, $3, 1
FROM unnest($2::text[]) AS name
ON CONFLICT (provider_id, upstream_model) DO NOTHING
`, providerID, clean, source)
}

// normaliseProviderModelNames trims, drops empty entries and de-duplicates, so
// a provider that repeats a name in its own model list does not turn into two
// INSERT candidates for one unique row.
func normaliseProviderModelNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}
