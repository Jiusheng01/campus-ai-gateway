package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// Model catalog errors. The admin layer maps these to a status and a message
// that names the field; a generic "could not be updated" sends the operator
// looking for a row that is right in front of them.
var (
	ErrModelNotFound       = errors.New("model not found")
	ErrModelAliasImmutable = errors.New("model alias is immutable")
	ErrModelAliasTaken     = errors.New("model alias is already in use")
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
SELECT id::text, alias, display_name, status, created_at, updated_at
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
		if err := rows.Scan(&v.ID, &v.Alias, &v.DisplayName, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CreateModel writes the catalog entry only. A model with no route is a valid
// state: it is visible to the console and invisible to /v1/models until a route
// is added, which is exactly what "catalog first, deployment second" means.
func (p *Postgres) CreateModel(ctx context.Context, v model.ModelConfig) (model.ModelConfig, error) {
	err := p.Pool.QueryRow(ctx, `
INSERT INTO gateway_model(alias, display_name, status)
VALUES ($1, $2, $3)
RETURNING id::text, alias, display_name, status, created_at, updated_at
`, v.Alias, v.DisplayName, v.Status).Scan(
		&v.ID, &v.Alias, &v.DisplayName, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if isUniqueViolation(err) {
		return v, ErrModelAliasTaken
	}
	return v, err
}

// UpdateModel changes catalog metadata and nothing else. In particular it does
// not touch a route: the previous implementation rewrote whichever route sorted
// first, which made "edit the model" an unpredictable route edit.
//
// Alias is the public identifier every client, quota and allowlist refers to, so
// it is immutable. A new name is a new catalog entry; display_name is the
// human-readable label that can change freely.
func (p *Postgres) UpdateModel(ctx context.Context, id string, v model.ModelConfig) (model.ModelConfig, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return v, err
	}
	defer tx.Rollback(ctx)

	var currentAlias string
	err = tx.QueryRow(ctx, `
SELECT alias
FROM gateway_model
WHERE id=$1::uuid
FOR UPDATE
`, id).Scan(&currentAlias)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrModelNotFound
	}
	if err != nil {
		return v, err
	}
	if currentAlias != v.Alias {
		return v, ErrModelAliasImmutable
	}

	err = tx.QueryRow(ctx, `
UPDATE gateway_model
SET display_name=$2, status=$3, updated_at=NOW()
WHERE id=$1::uuid
RETURNING id::text, alias, display_name, status, created_at, updated_at
`, id, v.DisplayName, v.Status).Scan(
		&v.ID, &v.Alias, &v.DisplayName, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		return v, err
	}
	if err := tx.Commit(ctx); err != nil {
		return v, err
	}
	return v, nil
}

// tenantColumns is the projection every tenant read shares — the list, the
// single-row read the usage endpoint needs for its quota denominator, and the
// two writes' RETURNING clauses. Spelling it once means a column added to the
// tenant cannot be selected by three queries and forgotten by the fourth, which
// is exactly how a quota dimension silently reads as zero.
// tenantColumns is the canonical projection of a tenant row. Every query that
// returns a full tenant uses it, and it must stay in sync with scanTenant in
// tenant_hierarchy.go. Spelling it once means a column added to the tenant
// cannot be selected by three queries and forgotten by the fourth, which is
// exactly how a quota dimension silently reads as zero.
//
// The six limit columns are nullable by design (NULL = inherit), so they scan
// into *int64. allowed_models is nullable for the same reason: NULL means
// "inherit", while a present "[]" means "deny every model".
const tenantColumns = `id::text, code, name, status, daily_token_limit, monthly_token_limit, monthly_request_limit, monthly_budget_micros, rpm_limit, tpm_limit, max_concurrency, parent_id::text, path, depth, kind, allowed_models`

func (p *Postgres) ListTenants(ctx context.Context) ([]model.Tenant, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT `+tenantColumns+`
FROM gateway_tenant
ORDER BY path
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Tenant
	for rows.Next() {
		v, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CreateTenant and UpdateTenant live in tenant_hierarchy.go: a tenant row now
// carries a materialized path and a depth, both derived from its parent, so the
// write has to read the parent first. Keeping a second plain-INSERT version here
// would let a caller create a tenant with no path.

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

// APIKeyTenantID reports which tenant owns a key. The scoped admin layer uses
// it to decide whether a key addressed by id in the URL is inside the caller's
// subtree — the check has to happen against the stored row, not against the
// body, because the body is what the caller is trying to change.
//
// A missing key returns pgx.ErrNoRows, which the handlers already translate
// into 404.
func (p *Postgres) APIKeyTenantID(ctx context.Context, id string) (string, error) {
	var tenantID string
	err := p.Pool.QueryRow(ctx, `SELECT tenant_id::text FROM gateway_api_key WHERE id=$1::uuid`, id).Scan(&tenantID)
	return tenantID, err
}

func (p *Postgres) ListAPIKeys(ctx context.Context) ([]model.APIKeyAdmin, error) {
	return p.listAPIKeys(ctx, nil)
}

// ListAPIKeysInSubtree returns the keys of one tenant and its descendants. It
// is the scoped counterpart of ListAPIKeys and exists so a tenant_admin's key
// list is narrowed in the query rather than in the browser: a filter applied
// after the rows are read still ships every other department's keys over the
// wire, and one forgotten filter would show them.
func (p *Postgres) ListAPIKeysInSubtree(ctx context.Context, scopeTenantID string) ([]model.APIKeyAdmin, error) {
	if scopeTenantID == "" {
		return p.listAPIKeys(ctx, nil)
	}
	return p.listAPIKeys(ctx, &scopeTenantID)
}

func (p *Postgres) listAPIKeys(ctx context.Context, scopeTenantID *string) ([]model.APIKeyAdmin, error) {
	// The usage columns are aggregated from the request log rather than read
	// from a counter on the key. A stored counter would have to be incremented
	// on the hot path and would drift the moment a request was logged but its
	// counter update failed; deriving it means the number is always exactly
	// what the log says, and a revoked key keeps reporting what it served.
	//
	// LEFT JOIN LATERAL, not a plain join: an aggregate subquery always returns
	// one row, so the outer join never drops a key that has served no traffic.
	// It rides idx_gateway_request_log_api_key_created.
	//
	// The scope predicate is a prefix comparison on the tenant's materialized
	// path, the same test the tenant list uses, so "inside my subtree" has one
	// definition across the admin API.
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
WHERE $1::uuid IS NULL
   OR t.path LIKE (SELECT path || '%' FROM gateway_tenant WHERE id=$1::uuid)
ORDER BY k.created_at DESC
`, scopeTenantID)
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
