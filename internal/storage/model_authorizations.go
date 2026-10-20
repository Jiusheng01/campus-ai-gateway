package storage

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// ListModelAuthorizations reads the model-scoped authorization view. It never
// returns a key digest or plaintext.
func (p *Postgres) ListModelAuthorizations(ctx context.Context, modelID string) (string, []model.ModelAuthorization, error) {
	var alias string
	if err := p.Pool.QueryRow(ctx, `SELECT alias FROM gateway_model WHERE id=$1::uuid`, modelID).Scan(&alias); err != nil {
		return "", nil, err
	}
	rows, err := p.Pool.Query(ctx, `
SELECT k.id::text, k.name, k.tenant_id::text, t.name, k.status, k.allowed_models, k.last_used_at
FROM gateway_api_key k
JOIN gateway_tenant t ON t.id = k.tenant_id
ORDER BY k.created_at DESC
`)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()

	var out []model.ModelAuthorization
	for rows.Next() {
		var v model.ModelAuthorization
		var raw []byte
		if err := rows.Scan(&v.APIKeyID, &v.Name, &v.TenantID, &v.TenantName, &v.Status, &raw, &v.LastUsedAt); err != nil {
			return "", nil, err
		}
		var names []string
		_ = json.Unmarshal(raw, &names)
		for _, name := range names {
			if name == model.AllModelsWildcard {
				v.Wildcard = true
			}
			if name == alias {
				v.Allowed = true
			}
		}
		if v.Wildcard {
			v.Allowed = true
		}
		out = append(out, v)
	}
	return alias, out, rows.Err()
}

// SetModelAuthorization atomically adds or removes one alias on selected Keys.
// It reads and writes the current JSON array under a row lock, so a concurrent
// grant of another model is not lost. Wildcard keys are never silently
// rewritten: adding is already allowed, removing is refused.
func (p *Postgres) SetModelAuthorization(ctx context.Context, modelID string, keyIDs []string, allow bool) (string, []model.AuthorizationResult, error) {
	var alias string
	if err := p.Pool.QueryRow(ctx, `SELECT alias FROM gateway_model WHERE id=$1::uuid`, modelID).Scan(&alias); err != nil {
		return "", nil, err
	}
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback(ctx)

	out := make([]model.AuthorizationResult, 0, len(keyIDs))
	for _, keyID := range keyIDs {
		var raw []byte
		err := tx.QueryRow(ctx, `
SELECT allowed_models FROM gateway_api_key WHERE id=$1::uuid FOR UPDATE
`, keyID).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			out = append(out, model.AuthorizationResult{APIKeyID: keyID, Outcome: "not_found"})
			continue
		}
		if err != nil {
			return "", nil, err
		}
		var names []string
		_ = json.Unmarshal(raw, &names)
		wildcard := false
		has := false
		for _, name := range names {
			if name == model.AllModelsWildcard {
				wildcard = true
			}
			if name == alias {
				has = true
			}
		}
		if allow {
			if wildcard || has {
				out = append(out, model.AuthorizationResult{APIKeyID: keyID, Outcome: "already_allowed"})
				continue
			}
			names = append(names, alias)
			sort.Strings(names)
			encoded, _ := json.Marshal(names)
			if _, err := tx.Exec(ctx, `UPDATE gateway_api_key SET allowed_models=$2::jsonb, updated_at=NOW() WHERE id=$1::uuid`, keyID, string(encoded)); err != nil {
				return "", nil, err
			}
			out = append(out, model.AuthorizationResult{APIKeyID: keyID, Outcome: "added"})
			continue
		}
		if wildcard {
			out = append(out, model.AuthorizationResult{APIKeyID: keyID, Outcome: "wildcard_refused"})
			continue
		}
		if !has {
			out = append(out, model.AuthorizationResult{APIKeyID: keyID, Outcome: "not_present"})
			continue
		}
		filtered := make([]string, 0, len(names))
		for _, name := range names {
			if name != alias {
				filtered = append(filtered, name)
			}
		}
		encoded, _ := json.Marshal(filtered)
		if _, err := tx.Exec(ctx, `UPDATE gateway_api_key SET allowed_models=$2::jsonb, updated_at=NOW() WHERE id=$1::uuid`, keyID, string(encoded)); err != nil {
			return "", nil, err
		}
		out = append(out, model.AuthorizationResult{APIKeyID: keyID, Outcome: "removed"})
	}
	if err := tx.Commit(ctx); err != nil {
		return "", nil, err
	}
	return alias, out, nil
}
