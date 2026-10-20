package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

var (
	ErrLastModelRoute        = errors.New("cannot delete the last model route")
	ErrLastEnabledModelRoute = errors.New("cannot disable or delete the last enabled model route")
	// ErrConfigVersionConflict means the caller's expected_version is stale.
	// The handler reads the current version to report it with 409.
	ErrConfigVersionConflict = errors.New("model config version conflict")
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

// ReplaceModelRoutes atomically replaces a model's route set.
//
// The model row is locked first, exactly like the single-row Route writes and
// the provider-delete cascade. expected_version is checked under that lock, so
// two administrators with the same version cannot both commit. Routes matched
// by id (or by their unique binding) keep their id; omitted routes are deleted.
func (p *Postgres) ReplaceModelRoutes(ctx context.Context, modelID string, expectedVersion int64, routes []model.ModelRoute) (model.ModelConfig, []model.ModelRoute, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return model.ModelConfig{}, nil, err
	}
	defer tx.Rollback(ctx)

	var current model.ModelConfig
	if err := tx.QueryRow(ctx, `
SELECT id::text, alias, COALESCE(display_name,''), COALESCE(description,''), status,
	config_version, created_at, updated_at
FROM gateway_model WHERE id=$1::uuid FOR UPDATE
`, modelID).Scan(&current.ID, &current.Alias, &current.DisplayName, &current.Description,
		&current.Status, &current.ConfigVersion, &current.CreatedAt, &current.UpdatedAt); err != nil {
		return model.ModelConfig{}, nil, err
	}
	if expectedVersion <= 0 || current.ConfigVersion != expectedVersion {
		return model.ModelConfig{}, nil, ErrConfigVersionConflict
	}

	type existingRoute struct {
		id            string
		providerID    string
		upstreamModel string
	}
	rows, err := tx.Query(ctx, `
SELECT id::text, provider_id::text, upstream_model
FROM gateway_model_route WHERE model_id=$1::uuid ORDER BY id FOR UPDATE
`, modelID)
	if err != nil {
		return model.ModelConfig{}, nil, err
	}
	byID := map[string]existingRoute{}
	byBinding := map[string]string{}
	for rows.Next() {
		var r existingRoute
		if err := rows.Scan(&r.id, &r.providerID, &r.upstreamModel); err != nil {
			rows.Close()
			return model.ModelConfig{}, nil, err
		}
		byID[r.id] = r
		byBinding[r.providerID+"\x00"+r.upstreamModel] = r.id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return model.ModelConfig{}, nil, err
	}

	used := map[string]bool{}
	desired := make([]model.ModelRoute, len(routes))
	bindings := map[string]bool{}
	for i, r := range routes {
		key := r.ProviderID + "\x00" + r.UpstreamModel
		if bindings[key] {
			return model.ModelConfig{}, nil, fmt.Errorf("duplicate route binding %s/%s", r.ProviderID, r.UpstreamModel)
		}
		bindings[key] = true
		if r.ID != "" {
			if _, ok := byID[r.ID]; !ok {
				return model.ModelConfig{}, nil, fmt.Errorf("route %s does not belong to model %s", r.ID, modelID)
			}
			if used[r.ID] {
				return model.ModelConfig{}, nil, fmt.Errorf("route %s appears more than once", r.ID)
			}
			used[r.ID] = true
		} else if id, ok := byBinding[key]; ok {
			r.ID = id
			if used[id] {
				return model.ModelConfig{}, nil, fmt.Errorf("route binding %s/%s appears more than once", r.ProviderID, r.UpstreamModel)
			}
			used[id] = true
		}
		desired[i] = r
	}
	if len(desired) == 0 {
		return model.ModelConfig{}, nil, ErrLastModelRoute
	}
	enabled := 0
	for _, r := range desired {
		if r.Status == 1 {
			enabled++
		}
	}
	if current.Status == 1 && enabled == 0 {
		return model.ModelConfig{}, nil, ErrLastEnabledModelRoute
	}

	var deleteIDs []string
	for id := range byID {
		if !used[id] {
			deleteIDs = append(deleteIDs, id)
		}
	}
	if len(deleteIDs) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM gateway_model_route WHERE model_id=$1::uuid AND id = ANY($2::uuid[])`, modelID, deleteIDs); err != nil {
			return model.ModelConfig{}, nil, err
		}
	}

	for _, r := range desired {
		if r.ID != "" {
			if _, err := tx.Exec(ctx, `
UPDATE gateway_model_route
SET provider_id=$2::uuid, upstream_model=$3, priority=$4, weight=$5, timeout_ms=$6, status=$7, updated_at=NOW()
WHERE id=$1::uuid AND model_id=$8::uuid
`, r.ID, r.ProviderID, r.UpstreamModel, r.Priority, r.Weight, r.TimeoutMS, r.Status, modelID); err != nil {
				return model.ModelConfig{}, nil, err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO gateway_model_route(model_id, provider_id, upstream_model, priority, weight, timeout_ms, status)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7)
`, modelID, r.ProviderID, r.UpstreamModel, r.Priority, r.Weight, r.TimeoutMS, r.Status); err != nil {
			return model.ModelConfig{}, nil, err
		}
	}

	newVersion, err := bumpModelVersion(ctx, tx, modelID)
	if err != nil {
		return model.ModelConfig{}, nil, err
	}
	current.ConfigVersion = newVersion

	result, err := listModelRoutesTx(ctx, tx, modelID)
	if err != nil {
		return model.ModelConfig{}, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.ModelConfig{}, nil, err
	}
	return current, result, nil
}

func bumpModelVersion(ctx context.Context, tx pgx.Tx, modelID string) (int64, error) {
	var version int64
	err := tx.QueryRow(ctx, `
UPDATE gateway_model SET config_version=config_version+1, updated_at=NOW()
WHERE id=$1::uuid RETURNING config_version
`, modelID).Scan(&version)
	return version, err
}

func listModelRoutesTx(ctx context.Context, tx pgx.Tx, modelID string) ([]model.ModelRoute, error) {
	rows, err := tx.Query(ctx, `
SELECT r.id::text, r.model_id::text, m.alias, r.provider_id::text, p.name,
	r.upstream_model, r.priority, r.weight, r.timeout_ms, r.status, r.created_at, r.updated_at
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
		if err := rows.Scan(&v.ID, &v.ModelID, &v.ModelAlias, &v.ProviderID, &v.ProviderName,
			&v.UpstreamModel, &v.Priority, &v.Weight, &v.TimeoutMS, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
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
	if _, err := bumpModelVersion(ctx, tx, modelID); err != nil {
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
	if _, err := bumpModelVersion(ctx, tx, modelID); err != nil {
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
	if _, err := bumpModelVersion(ctx, tx, modelID); err != nil {
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
