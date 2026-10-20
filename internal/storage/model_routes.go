package storage

import (
	"context"
	"errors"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

var (
	ErrLastModelRoute        = errors.New("cannot delete the last model route")
	ErrLastEnabledModelRoute = errors.New("cannot disable or delete the last enabled model route")
)

func (p *Postgres) ListModelRoutes(ctx context.Context, modelID string) ([]model.ModelRoute, error) {
	var exists bool
	if err := p.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM gateway_model WHERE id=$1::uuid)", modelID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, pgx.ErrNoRows
	}
	rows, err := p.Pool.Query(ctx, `
SELECT
	r.id::text,
	r.model_id::text,
	m.alias,
	r.provider_id::text,
	p.name,
	r.upstream_model,
	r.priority,
	r.weight,
	r.timeout_ms,
	r.status,
	r.created_at,
	r.updated_at
FROM gateway_model_route r
JOIN gateway_model m ON m.id=r.model_id
JOIN gateway_provider p ON p.id=r.provider_id
WHERE r.model_id=$1::uuid
ORDER BY r.priority ASC, r.weight DESC, r.created_at ASC
`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.ModelRoute
	for rows.Next() {
		var v model.ModelRoute
		if err := rows.Scan(
			&v.ID,
			&v.ModelID,
			&v.ModelAlias,
			&v.ProviderID,
			&v.ProviderName,
			&v.UpstreamModel,
			&v.Priority,
			&v.Weight,
			&v.TimeoutMS,
			&v.Status,
			&v.CreatedAt,
			&v.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListRouteViews is the aggregate route policy read. It adds model display
// name/version and provider/credential context, but every routing value still
// comes from gateway_model_route.
func (p *Postgres) ListRouteViews(ctx context.Context) ([]model.RouteView, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT
	r.id::text, r.model_id::text, m.alias, r.provider_id::text, p.name,
	r.upstream_model, r.priority, r.weight, r.timeout_ms, r.status, r.created_at, r.updated_at,
	COALESCE(m.display_name,''), m.config_version, p.status,
	((c.source_type='inline' AND COALESCE(c.ciphertext,'')<>'')
	  OR (c.source_type='env' AND COALESCE(c.secret_ref,'')<>''))
FROM gateway_model_route r
JOIN gateway_model m ON m.id=r.model_id
JOIN gateway_provider p ON p.id=r.provider_id
JOIN gateway_credential c ON c.id=p.credential_id
ORDER BY m.alias ASC, r.priority ASC, r.weight DESC, r.created_at ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.RouteView
	for rows.Next() {
		var v model.RouteView
		if err := rows.Scan(
			&v.ID, &v.ModelID, &v.ModelAlias, &v.ProviderID, &v.ProviderName,
			&v.UpstreamModel, &v.Priority, &v.Weight, &v.TimeoutMS, &v.Status,
			&v.CreatedAt, &v.UpdatedAt,
			&v.ModelDisplayName, &v.ConfigVersion, &v.ProviderStatus, &v.CredentialConfigured,
		); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateModelRoute(ctx context.Context, modelID string, v model.ModelRoute) (model.ModelRoute, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return v, err
	}
	defer tx.Rollback(ctx)

	if err := lockModelRouteParent(ctx, tx, modelID); err != nil {
		return v, err
	}

	err = tx.QueryRow(ctx, `
INSERT INTO gateway_model_route(model_id, provider_id, upstream_model, priority, weight, timeout_ms, status)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7)
RETURNING id::text, model_id::text, provider_id::text, upstream_model, priority, weight, timeout_ms, status, created_at, updated_at
`,
		modelID, v.ProviderID, v.UpstreamModel, v.Priority, v.Weight, v.TimeoutMS, v.Status,
	).Scan(
		&v.ID, &v.ModelID, &v.ProviderID, &v.UpstreamModel,
		&v.Priority, &v.Weight, &v.TimeoutMS, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		return v, err
	}
	if err := loadModelRouteNames(ctx, tx, &v); err != nil {
		return v, err
	}
	if err := tx.Commit(ctx); err != nil {
		return v, err
	}
	return v, nil
}

func (p *Postgres) UpdateModelRoute(ctx context.Context, routeID string, v model.ModelRoute) (model.ModelRoute, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return v, err
	}
	defer tx.Rollback(ctx)

	var modelID string
	if err := tx.QueryRow(ctx, `
SELECT model_id::text
FROM gateway_model_route
WHERE id=$1::uuid
`, routeID).Scan(&modelID); err != nil {
		return v, err
	}
	if err := lockModelRouteParent(ctx, tx, modelID); err != nil {
		return v, err
	}

	var oldStatus int
	if err := tx.QueryRow(ctx, `
SELECT status
FROM gateway_model_route
WHERE id=$1::uuid
FOR UPDATE
`, routeID).Scan(&oldStatus); err != nil {
		return v, err
	}

	if oldStatus == 1 && v.Status == 2 {
		var enabledOthers int
		if err := tx.QueryRow(ctx, `
SELECT COUNT(*)
FROM gateway_model_route
WHERE model_id=$1::uuid AND id<>$2::uuid AND status=1
`, modelID, routeID).Scan(&enabledOthers); err != nil {
			return v, err
		}
		if enabledOthers == 0 {
			return v, ErrLastEnabledModelRoute
		}
	}

	err = tx.QueryRow(ctx, `
UPDATE gateway_model_route
SET provider_id=$2::uuid,
	upstream_model=$3,
	priority=$4,
	weight=$5,
	timeout_ms=$6,
	status=$7,
	updated_at=NOW()
WHERE id=$1::uuid
RETURNING id::text, model_id::text, provider_id::text, upstream_model, priority, weight, timeout_ms, status, created_at, updated_at
`,
		routeID, v.ProviderID, v.UpstreamModel, v.Priority, v.Weight, v.TimeoutMS, v.Status,
	).Scan(
		&v.ID, &v.ModelID, &v.ProviderID, &v.UpstreamModel,
		&v.Priority, &v.Weight, &v.TimeoutMS, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		return v, err
	}
	if err := loadModelRouteNames(ctx, tx, &v); err != nil {
		return v, err
	}
	if err := tx.Commit(ctx); err != nil {
		return v, err
	}
	return v, nil
}

func (p *Postgres) DeleteModelRoute(ctx context.Context, routeID string) error {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var modelID string
	if err := tx.QueryRow(ctx, `
SELECT model_id::text
FROM gateway_model_route
WHERE id=$1::uuid
`, routeID).Scan(&modelID); err != nil {
		return err
	}
	if err := lockModelRouteParent(ctx, tx, modelID); err != nil {
		return err
	}

	var status int
	if err := tx.QueryRow(ctx, `
SELECT status
FROM gateway_model_route
WHERE id=$1::uuid
FOR UPDATE
`, routeID).Scan(&status); err != nil {
		return err
	}

	var total, enabled int
	if err := tx.QueryRow(ctx, `
SELECT COUNT(*), COUNT(*) FILTER (WHERE status=1)
FROM gateway_model_route
WHERE model_id=$1::uuid
`, modelID).Scan(&total, &enabled); err != nil {
		return err
	}
	if total <= 1 {
		return ErrLastModelRoute
	}
	if status == 1 && enabled <= 1 {
		return ErrLastEnabledModelRoute
	}

	if _, err := tx.Exec(ctx, `DELETE FROM gateway_model_route WHERE id=$1::uuid`, routeID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockModelRouteParent(ctx context.Context, tx pgx.Tx, modelID string) error {
	var id string
	return tx.QueryRow(ctx, `
SELECT id::text
FROM gateway_model
WHERE id=$1::uuid
FOR UPDATE
`, modelID).Scan(&id)
}

func loadModelRouteNames(ctx context.Context, tx pgx.Tx, v *model.ModelRoute) error {
	return tx.QueryRow(ctx, `
SELECT m.alias, p.name
FROM gateway_model m
JOIN gateway_provider p ON p.id=$2::uuid
WHERE m.id=$1::uuid
`, v.ModelID, v.ProviderID).Scan(&v.ModelAlias, &v.ProviderName)
}
