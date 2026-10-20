package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// providerColumns is the provider projection every read and write shares. The
// Credential is joined rather than stored on the provider: its source and secret
// are one row, so a provider edit cannot leave the two disagreeing.
const providerColumns = `p.id::text, p.name, p.base_url, p.protocol, p.status, p.created_at, p.updated_at,
	c.id::text, c.source_type, COALESCE(c.secret_ref,''), COALESCE(c.ciphertext,'')`

func (p *Postgres) ListProviders(ctx context.Context) ([]model.Provider, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT `+providerColumns+`
FROM gateway_provider p
JOIN gateway_credential c ON c.id = p.credential_id
ORDER BY p.created_at DESC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Provider
	for rows.Next() {
		var v model.Provider
		if err := rows.Scan(&v.ID, &v.Name, &v.BaseURL, &v.Protocol, &v.Status, &v.CreatedAt, &v.UpdatedAt,
			&v.CredentialID, &v.CredentialSource, &v.CredentialSecretRef, &v.CredentialCiphertext); err != nil {
			return nil, err
		}
		v.CredentialEnv = v.CredentialSecretRef
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateProvider(ctx context.Context, v model.Provider) (model.Provider, error) {
	return p.CreateProviderWithPresetModels(ctx, v, nil)
}

// CreateProviderWithPresetModels creates the channel and seeds its Provider
// Model list in one transaction.
//
// The seed is part of the create rather than a second call the handler makes
// afterwards: a channel that exists without the list it was supposed to come
// with is exactly the half-configured state this work is meant to remove, and a
// second call leaves a window where it can fail while the channel stays.
func (p *Postgres) CreateProviderWithPresetModels(ctx context.Context, v model.Provider, presetModels []string) (model.Provider, error) {
	if err := normaliseNewProviderCredential(&v); err != nil {
		return v, err
	}
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return v, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
WITH new_credential AS (
	INSERT INTO gateway_credential (name, source_type, secret_ref, ciphertext)
	VALUES ('channel:' || gen_random_uuid()::text, $1, NULLIF($2,''), NULLIF($3,''))
	RETURNING id
)
INSERT INTO gateway_provider (name, base_url, protocol, credential_id, status)
SELECT $4, $5, $6, new_credential.id, $7 FROM new_credential
RETURNING id::text, name, base_url, protocol, status, created_at, updated_at, credential_id::text
`, v.CredentialSource, v.CredentialSecretRef, v.CredentialCiphertext,
		v.Name, v.BaseURL, v.Protocol, v.Status).Scan(
		&v.ID, &v.Name, &v.BaseURL, &v.Protocol, &v.Status, &v.CreatedAt, &v.UpdatedAt, &v.CredentialID,
	)
	if err != nil {
		return v, err
	}
	if err := seedProviderModels(ctx, tx, v.ID, presetModels, model.ProviderModelSourcePreset); err != nil {
		return v, err
	}
	if err := tx.Commit(ctx); err != nil {
		return v, err
	}
	v.CredentialEnv = v.CredentialSecretRef
	return v, nil
}

// DeleteProvider hard-deletes a channel and everything that only existed for
// it, in one transaction.
//
// A plain DELETE cannot do this: gateway_model_route.provider_id is NO ACTION,
// so a channel that still has Routes cannot be removed; and
// gateway_provider.credential_id points the other way, so deleting the channel
// would leave its Credential — and the sealed secret in it — behind.
//
// The cascade deliberately bypasses the single-Route invariant
// (ErrLastModelRoute): a Route is deletable here because the Virtual Model is
// then checked for remaining Routes, and one left with zero Routes is deleted
// too (ADR-0019). A Virtual Model that still has another Route is kept, and so
// is that Route.
//
// The Provider row is deleted before its Credential, not after as the prose
// order suggests: the foreign key is provider -> credential, so deleting the
// referenced Credential first would fail while the Provider still points at it.
func (p *Postgres) DeleteProvider(ctx context.Context, id string) (model.ProviderDeleteResult, error) {
	var out model.ProviderDeleteResult
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)

	// Read the models that route here now only to pre-lock them before the
	// Provider row. This is not the delete list: a Route committed between this
	// read and the Provider lock below is invisible here, so the models to remove
	// are derived from the Route DELETE's RETURNING rows instead.
	rows, err := tx.Query(ctx, `
SELECT DISTINCT model_id::text
FROM gateway_model_route
WHERE provider_id=$1::uuid
ORDER BY model_id`, id)
	if err != nil {
		return out, err
	}
	var modelIDs []string
	for rows.Next() {
		var modelID string
		if err := rows.Scan(&modelID); err != nil {
			rows.Close()
			return out, err
		}
		modelIDs = append(modelIDs, modelID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	// Lock order: Virtual Model rows first, then the Provider row.
	// CreateModelRoute, UpdateModelRoute and DeleteModelRoute all lock the model
	// first; the inserting or moving writes then take the Provider's FK KEY
	// SHARE. Locking the Provider here first would close a cycle with any of
	// them. Holding the Provider with FOR UPDATE then blocks a new Route from
	// completing its FK check, so no Route can be committed between that lock
	// and the DELETE below.
	//
	// Residual window: a Route write for a Virtual Model that had no Route here
	// at the read is not pre-locked. If another Route onto this Provider
	// committed for that model in the gap, the model is in the RETURNING set, and
	// a second Route write holding that model while waiting for the Provider's
	// KEY SHARE forms a cycle with this transaction (which holds the Provider and
	// needs the model). PostgreSQL detects the cycle and aborts one transaction;
	// because the cascade is one transaction, the abort leaves every row in place
	// rather than a half-deleted channel.
	if len(modelIDs) > 0 {
		locks, err := tx.Query(ctx, `
SELECT id::text
FROM gateway_model
WHERE id = ANY($1::uuid[])
ORDER BY id
FOR UPDATE`, modelIDs)
		if err != nil {
			return out, err
		}
		for locks.Next() {
			var locked string
			if err := locks.Scan(&locked); err != nil {
				locks.Close()
				return out, err
			}
		}
		locks.Close()
		if err := locks.Err(); err != nil {
			return out, err
		}
	}

	var credentialID string
	if err := tx.QueryRow(ctx, `
SELECT credential_id::text
FROM gateway_provider
WHERE id=$1::uuid
FOR UPDATE`, id).Scan(&credentialID); err != nil {
		return out, err
	}

	// RETURNING is the record of what this delete touched. A Route inserted after
	// the read above is caught here, so its model is considered for deletion even
	// though the precomputed list never named it.
	rows, err = tx.Query(ctx, `
DELETE FROM gateway_model_route
WHERE provider_id=$1::uuid
RETURNING model_id::text`, id)
	if err != nil {
		return out, err
	}
	affectedSet := make(map[string]struct{})
	for rows.Next() {
		var modelID string
		if err := rows.Scan(&modelID); err != nil {
			rows.Close()
			return out, err
		}
		affectedSet[modelID] = struct{}{}
		out.DeletedRoutes++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	affected := make([]string, 0, len(affectedSet))
	for modelID := range affectedSet {
		affected = append(affected, modelID)
	}
	sort.Strings(affected)

	if len(affected) > 0 {
		// A model that only became affected after the initial read was not locked
		// above, so lock the whole returned set in id order. The "no Routes left"
		// check runs only after those locks are held: a concurrent Route write
		// either committed before them (and the model survives) or waits for the
		// model lock and then finds no model to attach to.
		locks, err := tx.Query(ctx, `
SELECT id::text
FROM gateway_model
WHERE id = ANY($1::uuid[])
ORDER BY id
FOR UPDATE`, affected)
		if err != nil {
			return out, err
		}
		for locks.Next() {
			var locked string
			if err := locks.Scan(&locked); err != nil {
				locks.Close()
				return out, err
			}
		}
		locks.Close()
		if err := locks.Err(); err != nil {
			return out, err
		}

		tag, err := tx.Exec(ctx, `
DELETE FROM gateway_model m
WHERE m.id = ANY($1::uuid[])
  AND NOT EXISTS (SELECT 1 FROM gateway_model_route r WHERE r.model_id = m.id)`, affected)
		if err != nil {
			return out, err
		}
		out.DeletedVirtualModels = int(tag.RowsAffected())
	}

	// The table has ON DELETE CASCADE, but the rows are deleted explicitly: the
	// count is part of the answer the operator sees.
	tag, err := tx.Exec(ctx, `DELETE FROM gateway_provider_model WHERE provider_id=$1::uuid`, id)
	if err != nil {
		return out, err
	}
	out.DeletedProviderModels = int(tag.RowsAffected())

	tag, err = tx.Exec(ctx, `DELETE FROM gateway_provider WHERE id=$1::uuid`, id)
	if err != nil {
		return out, err
	}
	if tag.RowsAffected() != 1 {
		return out, pgx.ErrNoRows
	}

	// Only now, with the last reference gone, can the Credential be removed —
	// and only if no other Provider points at it.
	if _, err := tx.Exec(ctx, `
DELETE FROM gateway_credential c
WHERE c.id=$1::uuid
  AND NOT EXISTS (SELECT 1 FROM gateway_provider p WHERE p.credential_id=c.id)`, credentialID); err != nil {
		return out, err
	}

	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}

// UpdateProvider edits the provider and, when the caller supplies a credential
// source, its Credential. An update with no source leaves the Credential alone:
// that is how "leave the key field blank" edits the channel without touching the
// secret.
func (p *Postgres) UpdateProvider(ctx context.Context, id string, v model.Provider) (model.Provider, error) {
	if err := normaliseUpdatedProviderCredential(&v); err != nil {
		return v, err
	}

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return v, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
UPDATE gateway_provider
SET name=$2, base_url=$3, protocol=$4, status=$5, updated_at=NOW()
WHERE id=$1::uuid
RETURNING id::text, name, base_url, protocol, status, created_at, updated_at, credential_id::text
`, id, v.Name, v.BaseURL, v.Protocol, v.Status).Scan(
		&v.ID, &v.Name, &v.BaseURL, &v.Protocol, &v.Status, &v.CreatedAt, &v.UpdatedAt, &v.CredentialID,
	)
	if err != nil {
		return v, err
	}

	switch v.CredentialSource {
	case model.CredentialSourceEnv:
		if _, err := tx.Exec(ctx, `
UPDATE gateway_credential
SET source_type='env', secret_ref=$2, ciphertext=NULL, updated_at=NOW()
WHERE id=$1::uuid`, v.CredentialID, v.CredentialSecretRef); err != nil {
			return v, err
		}
	case model.CredentialSourceInline:
		if _, err := tx.Exec(ctx, `
UPDATE gateway_credential
SET source_type='inline', secret_ref=NULL, ciphertext=$2, updated_at=NOW()
WHERE id=$1::uuid`, v.CredentialID, v.CredentialCiphertext); err != nil {
			return v, err
		}
	}

	// Read the credential back rather than assuming it: the caller needs the
	// actual source after a no-change update so the response summary is right.
	if err := tx.QueryRow(ctx, `
SELECT id::text, source_type, COALESCE(secret_ref,''), COALESCE(ciphertext,'')
FROM gateway_credential
WHERE id=$1::uuid`, v.CredentialID).Scan(
		&v.CredentialID, &v.CredentialSource, &v.CredentialSecretRef, &v.CredentialCiphertext,
	); err != nil {
		return v, err
	}
	if err := tx.Commit(ctx); err != nil {
		return v, err
	}
	v.CredentialEnv = v.CredentialSecretRef
	return v, nil
}

// normaliseNewProviderCredential turns the write fields into exactly one source.
// It accepts credential_env on its own so the pre-Credential clients keep
// working, and it refuses a provider with no credential at all: a channel that
// cannot authenticate has no use.
func normaliseNewProviderCredential(v *model.Provider) error {
	source := strings.TrimSpace(v.CredentialSource)
	if source == "" {
		ref := strings.TrimSpace(v.CredentialEnv)
		if ref == "" {
			return errors.New("a provider requires a credential_secret or credential_env")
		}
		source = model.CredentialSourceEnv
		v.CredentialSecretRef = ref
	}
	switch source {
	case model.CredentialSourceEnv:
		ref := strings.TrimSpace(v.CredentialSecretRef)
		if ref == "" {
			ref = strings.TrimSpace(v.CredentialEnv)
		}
		if ref == "" {
			return errors.New("an env credential requires a variable name")
		}
		v.CredentialSource = model.CredentialSourceEnv
		v.CredentialSecretRef = ref
		v.CredentialEnv = ref
		v.CredentialCiphertext = ""
	case model.CredentialSourceInline:
		ciphertext := strings.TrimSpace(v.CredentialCiphertext)
		if ciphertext == "" {
			return errors.New("an inline credential requires a sealed value")
		}
		v.CredentialSource = model.CredentialSourceInline
		v.CredentialSecretRef = ""
		v.CredentialEnv = ""
		v.CredentialCiphertext = ciphertext
	default:
		return fmt.Errorf("unknown credential source %q", source)
	}
	return nil
}

// normaliseUpdatedProviderCredential is the update rule: no credential means
// "keep the existing one", while a credential_env on its own keeps the old
// environment-variable client working.
func normaliseUpdatedProviderCredential(v *model.Provider) error {
	if strings.TrimSpace(v.CredentialSource) == "" {
		ref := strings.TrimSpace(v.CredentialEnv)
		if ref == "" {
			v.CredentialSecretRef = ""
			v.CredentialCiphertext = ""
			return nil
		}
		v.CredentialSource = model.CredentialSourceEnv
		v.CredentialSecretRef = ref
	}
	return normaliseNewProviderCredential(v)
}

// modelColumns is the projection every model read and write acknowledgement
// shares. A model is only its client-facing identity and lifecycle state: the
// channel and upstream model belong to its Routes (ADR-0005), so there is no
// routing column here to fall out of step with them.
const modelColumns = `id::text, alias, status, created_at, updated_at`

func (p *Postgres) ListModels(ctx context.Context) ([]model.ModelConfig, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT `+modelColumns+`
FROM gateway_model
ORDER BY created_at DESC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.ModelConfig
	for rows.Next() {
		var v model.ModelConfig
		if err := rows.Scan(&v.ID, &v.Alias, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CreateModel creates the model and writes its first Route in one transaction.
//
// The caller supplies the channel and upstream model for that first line; there
// is no routing column on the model to write them to. A model without a Route
// cannot serve traffic, so the two are always created together.
func (p *Postgres) CreateModel(ctx context.Context, v model.ModelConfig) (model.ModelConfig, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return v, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
INSERT INTO gateway_model(alias, status)
VALUES ($1, $2)
RETURNING `+modelColumns+`
`, v.Alias, v.Status).Scan(&v.ID, &v.Alias, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return v, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO gateway_model_route(model_id, provider_id, upstream_model, priority, weight, timeout_ms, status)
VALUES ($1::uuid, $2::uuid, $3, 1, 100, 60000, 1)
`, v.ID, v.ProviderID, v.UpstreamModel); err != nil {
		return v, err
	}
	if err := tx.Commit(ctx); err != nil {
		return v, err
	}
	// The routing fields on the input describe the Route that was just written,
	// not anything stored on the model, so the acknowledgement does not repeat
	// them back as model state.
	v.ProviderID, v.ProviderName, v.UpstreamModel = "", "", ""
	return v, nil
}

// UpdateModel updates the model's identity and lifecycle state only.
//
// It deliberately touches no Route. An earlier implementation picked a Route
// (the best enabled one, then the first one) and overwrote its channel and
// upstream model, which made a model edit a second routing authority. Routes are
// now edited one at a time through the Route API; a binding supplied to this
// function is ignored rather than applied.
func (p *Postgres) UpdateModel(ctx context.Context, id string, v model.ModelConfig) (model.ModelConfig, error) {
	err := p.Pool.QueryRow(ctx, `
UPDATE gateway_model
SET alias=$2, status=$3, updated_at=NOW()
WHERE id=$1::uuid
RETURNING `+modelColumns+`
`, id, v.Alias, v.Status).Scan(&v.ID, &v.Alias, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return v, err
	}
	v.ProviderID, v.ProviderName, v.UpstreamModel = "", "", ""
	return v, nil
}

// tenantColumns is the projection every tenant read shares — the list, the
// single-row read the usage endpoint needs for its quota denominator, and the
// two writes' RETURNING clauses. Spelling it once means a column added to the
// tenant cannot be selected by three queries and forgotten by the fourth, which
// is exactly how a quota dimension silently reads as zero.
const tenantColumns = `id::text, code, name, status, daily_token_limit, monthly_token_limit, monthly_request_limit, monthly_budget_micros, rpm_limit, tpm_limit, max_concurrency`

func (p *Postgres) ListTenants(ctx context.Context) ([]model.Tenant, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT `+tenantColumns+`
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
		if err := rows.Scan(&v.ID, &v.Code, &v.Name, &v.Status, &v.DailyTokenLimit, &v.MonthlyTokenLimit, &v.MonthlyRequestLimit, &v.MonthlyBudgetMicros, &v.RPMLimit, &v.TPMLimit, &v.MaxConcurrency); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateTenant(ctx context.Context, v model.Tenant) (model.Tenant, error) {
	err := p.Pool.QueryRow(ctx, `
INSERT INTO gateway_tenant(code, name, status, daily_token_limit, monthly_token_limit, monthly_request_limit, monthly_budget_micros, rpm_limit, tpm_limit, max_concurrency)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING `+tenantColumns+`
`, v.Code, v.Name, v.Status, v.DailyTokenLimit, v.MonthlyTokenLimit, v.MonthlyRequestLimit, v.MonthlyBudgetMicros, v.RPMLimit, v.TPMLimit, v.MaxConcurrency).Scan(&v.ID, &v.Code, &v.Name, &v.Status, &v.DailyTokenLimit, &v.MonthlyTokenLimit, &v.MonthlyRequestLimit, &v.MonthlyBudgetMicros, &v.RPMLimit, &v.TPMLimit, &v.MaxConcurrency)
	return v, err
}

func (p *Postgres) UpdateTenant(ctx context.Context, id string, v model.Tenant) (model.Tenant, error) {
	err := p.Pool.QueryRow(ctx, `
UPDATE gateway_tenant
SET code=$2, name=$3, status=$4, daily_token_limit=$5, monthly_token_limit=$6, monthly_request_limit=$7, monthly_budget_micros=$8, rpm_limit=$9, tpm_limit=$10, max_concurrency=$11, updated_at=NOW()
WHERE id=$1::uuid
RETURNING `+tenantColumns+`
`, id, v.Code, v.Name, v.Status, v.DailyTokenLimit, v.MonthlyTokenLimit, v.MonthlyRequestLimit, v.MonthlyBudgetMicros, v.RPMLimit, v.TPMLimit, v.MaxConcurrency).Scan(&v.ID, &v.Code, &v.Name, &v.Status, &v.DailyTokenLimit, &v.MonthlyTokenLimit, &v.MonthlyRequestLimit, &v.MonthlyBudgetMicros, &v.RPMLimit, &v.TPMLimit, &v.MaxConcurrency)
	return v, err
}

// TenantUsageStats reports a tenant's usage for the current day and month. It is
// the numerator of the quota share the console shows.
//
// The day and month boundaries are computed here, in UTC, and passed as
// instants. That is deliberate: the limiting side buckets on UTC too — the
// limiter's period keys come from time.Now().UTC() (tenantDayBucket /
// tenantMonthBucket) and a Quota Counter rebuild reads the log over the same UTC
// windows — so the number the panel shows is the window the gate actually
// enforced. Computing the boundary as date_trunc('day', NOW()) inside the query
// would instead follow the database session's TimeZone, and a deployment whose
// Postgres runs in UTC+8 would file the eight hours after local midnight under
// the previous day, making the console's "used" disagree with the counter that
// refused the request.
func (p *Postgres) TenantUsageStats(ctx context.Context, tenantID string) (model.TenantUsageStats, error) {
	var v model.TenantUsageStats
	v.TenantID = tenantID
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	err := p.Pool.QueryRow(ctx, `
SELECT
	COUNT(*) FILTER (WHERE created_at >= $2::timestamptz)::bigint,
	COUNT(*) FILTER (WHERE created_at >= $3::timestamptz)::bigint,
	COALESCE(SUM(total_tokens) FILTER (WHERE created_at >= $2::timestamptz),0)::bigint,
	COALESCE(SUM(total_tokens) FILTER (WHERE created_at >= $3::timestamptz),0)::bigint
FROM gateway_request_log
WHERE tenant_id=$1::uuid
`, tenantID, dayStart, monthStart).Scan(&v.DailyRequests, &v.MonthlyRequests, &v.DailyTokens, &v.MonthlyTokens)
	return v, err
}

func (p *Postgres) ListAPIKeys(ctx context.Context) ([]model.APIKeyAdmin, error) {
	// The usage columns are aggregated from the request log rather than read
	// from a counter on the key. A stored counter would have to be incremented
	// on the hot path and would drift the moment a request was logged but its
	// counter update failed; deriving it means the number is always exactly
	// what the log says, and a revoked key keeps reporting what it served.
	//
	// LEFT JOIN LATERAL, not a plain join: an aggregate subquery always returns
	// one row, so the outer join never drops a key that has served no traffic.
	// It rides idx_gateway_request_log_api_key_created.
	rows, err := p.Pool.Query(ctx, `
SELECT
	k.id::text, k.tenant_id::text, t.code, t.name, k.name, k.key_prefix, k.status,
	k.expire_at, k.last_used_at, k.allowed_models, k.allowed_ips,
	COALESCE(k.route_group,''),
	k.created_at,
	COALESCE(u.requests,0)::bigint, COALESCE(u.tokens,0)::bigint
FROM gateway_api_key k
JOIN gateway_tenant t ON t.id=k.tenant_id
LEFT JOIN LATERAL (
	SELECT COUNT(*) AS requests, COALESCE(SUM(COALESCE(l.total_tokens,0)),0) AS tokens
	FROM gateway_request_log l
	WHERE l.api_key_id = k.id
) u ON true
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
			&v.RouteGroup, &v.CreatedAt,
			&v.UsageRequests, &v.UsageTokens,
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
	allowed_models, allowed_ips, route_group
)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, NULLIF($9,''))
RETURNING id::text, created_at
`,
		v.TenantID, v.Name, v.KeyPrefix, digest, v.Status, v.ExpireAt,
		string(modelsJSON), string(ipsJSON), v.RouteGroup,
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
	route_group=NULLIF($8,''),
	updated_at=NOW()
WHERE id=$1::uuid
RETURNING key_prefix, created_at, last_used_at
`,
		id, v.TenantID, v.Name, v.Status, v.ExpireAt,
		string(modelsJSON), string(ipsJSON), v.RouteGroup,
	).Scan(&v.KeyPrefix, &v.CreatedAt, &v.LastUsedAt)
	if err != nil {
		return v, err
	}
	v.ID = id
	_ = p.Pool.QueryRow(ctx, "SELECT code, name FROM gateway_tenant WHERE id=$1::uuid", v.TenantID).Scan(&v.TenantCode, &v.TenantName)
	return v, nil
}

// RevokeAPIKey disables a key and destroys its credential digest.
//
// Disabling alone would be an edit, not a revocation: the row would keep the
// digest of a key that was already handed out, so re-enabling it — or any
// future code that reads the column — would bring the same credential back to
// life. Overwriting the digest with a fresh one makes the revocation
// irreversible, which is the property an operator is asking for when they
// revoke rather than disable.
//
// The replacement digest is generated by the caller so the HMAC secret stays in
// the layer that already owns it; only the digest is ever persisted, exactly as
// for a newly created key.
//
// The row itself is kept. gateway_request_log references the key by id, and
// deleting the row would leave every historical request pointing at an
// identifier nothing can resolve — the audit trail would survive, but stop
// being readable.
func (p *Postgres) RevokeAPIKey(ctx context.Context, id, digest string) error {
	tag, err := p.Pool.Exec(ctx, `
UPDATE gateway_api_key
SET status=2,
	key_digest=$2,
	updated_at=NOW()
WHERE id=$1::uuid
`, id, digest)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
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
	COALESCE(upstream_model,''),
	COALESCE(route_priority,0),
	COALESCE(attempts,0),
	COALESCE(failover,false),
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
			&v.RequestedModel, &v.ActualModel, &v.Provider, &v.UpstreamModel,
			&v.RoutePriority, &v.Attempts, &v.Failover, &v.Stream,
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
	COALESCE(AVG(latency_ms),0)::float8,
	COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms),0)::float8,
	COUNT(*) FILTER (WHERE COALESCE(failover,false))::bigint
FROM gateway_request_log
WHERE created_at >= date_trunc('day', NOW())
`).Scan(&v.TodayRequests, &v.TodayTokens, &v.TodayErrors, &v.AvgLatencyMS, &v.P95LatencyMS, &v.FailoverCount)
	return v, err
}
