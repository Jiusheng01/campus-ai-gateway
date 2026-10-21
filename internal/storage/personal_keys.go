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

// The personal-create refusals are values the handler maps onto stable error
// codes. They live here rather than in the handler because the state that
// justifies them (no Tenant, an empty allowlist, the key count) is read inside
// the create transaction: deciding outside it would be a guess that a
// concurrent write could invalidate.
var (
	// ErrPersonalTenantUnassigned means the Account has no Tenant Membership,
	// so there is no budget or allowlist to issue a personal key against.
	ErrPersonalTenantUnassigned = errors.New("account has no tenant")

	// ErrPersonalTenantModelsEmpty means the Tenant's allowlist is []: it has
	// deliberately granted its members no models.
	ErrPersonalTenantModelsEmpty = errors.New("tenant allows no models")

	// ErrPersonalAccountNotFound means the session named an account that no
	// longer exists. It cannot normally happen while a session is valid; it is
	// kept distinct so the handler does not turn it into a generic 500.
	ErrPersonalAccountNotFound = errors.New("account not found")
)

// PersonalKeyLimitError reports that the Account already owns the maximum
// number of keys. Limit is carried so the refusal can say what the ceiling is
// rather than only that one was reached.
type PersonalKeyLimitError struct {
	Limit int64
}

func (e *PersonalKeyLimitError) Error() string {
	return fmt.Sprintf("account already owns %d API keys, the configured limit", e.Limit)
}

// PersonalModelNotAllowedError reports a requested model outside the Tenant's
// allowlist. Model is the first offending entry, so the console can point at
// the selection that has to change.
type PersonalModelNotAllowedError struct {
	Model string
}

func (e *PersonalModelNotAllowedError) Error() string {
	return fmt.Sprintf("model %q is not allowed by the tenant", e.Model)
}

// PersonalKeyCreate is the write-side input of a personal key.
//
// It deliberately has no TenantID and no OwnerUserID: both are derived inside
// CreatePersonalAPIKey from the account row the caller names. Keeping them off
// this struct makes it impossible for an HTTP body to redirect either one.
type PersonalKeyCreate struct {
	OwnerUserID string
	Name        string
	// KeyPrefix and KeyDigest come from security.NewAPIKey in the handler. They
	// are not request fields and are never derived from one.
	KeyPrefix string
	KeyDigest string
	// AllowedModels is already normalised by the caller (sorted, deduplicated,
	// wildcard alone). The storage layer still resolves it against the Tenant's
	// current allowlist inside the transaction.
	AllowedModels []string
	AllowedIPs    []string
	ExpireAt      *time.Time
	// Limit is the per-Account ceiling. A non-positive value is treated as the
	// handler's default, so a hand-built Config cannot disable creation.
	Limit int64
}

// ListAPIKeysByOwner returns only the keys the Account owns. The owner is a
// bound parameter, never a filter the caller can widen: a request that names
// another account simply matches nothing.
//
// The projection is the admin list's minus nothing secret — the digest is not
// selected at all, so the list cannot return a full key even if a template
// later tried to.
func (p *Postgres) ListAPIKeysByOwner(ctx context.Context, ownerID string) ([]model.APIKeyAdmin, error) {
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
WHERE k.owner_user_id = $1::uuid
ORDER BY k.created_at DESC
`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.APIKeyAdmin, 0, 8)
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

// CreatePersonalAPIKey writes one key owned by the Account and scoped to its
// Tenant's allowlist.
//
// Everything that decides whether the write may happen is read inside one
// transaction that first locks the account row with FOR UPDATE. That lock is
// the same one AssignAdminUserTenant takes: assignment counts the account's
// keys under it, so a create and an assignment cannot interleave and leave a
// key pointing at a Tenant its owner has just left. It is also what makes the
// per-Account limit a hard ceiling — concurrent creates queue on the row and
// each sees the count left by the one before it.
func (p *Postgres) CreatePersonalAPIKey(ctx context.Context, in PersonalKeyCreate) (model.APIKeyAdmin, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return model.APIKeyAdmin{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The lock comes before every read that informs the decision. NULL tenant_id
	// is the unassigned state migration 0018 introduced, not an error value.
	var tenantID *string
	err = tx.QueryRow(ctx, `
SELECT tenant_id::text
FROM gateway_admin_user
WHERE id=$1::uuid
FOR UPDATE
`, in.OwnerUserID).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.APIKeyAdmin{}, ErrPersonalAccountNotFound
	}
	if err != nil {
		return model.APIKeyAdmin{}, err
	}
	if tenantID == nil || strings.TrimSpace(*tenantID) == "" {
		return model.APIKeyAdmin{}, ErrPersonalTenantUnassigned
	}

	var tenantCode, tenantName, tenantModelsJSON string
	err = tx.QueryRow(ctx, `
SELECT code, name, allowed_models::text
FROM gateway_tenant
WHERE id=$1::uuid
`, *tenantID).Scan(&tenantCode, &tenantName, &tenantModelsJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		// The Account's foreign key is ON DELETE SET NULL, so this is only
		// reachable in the moment between a Tenant delete and the account row's
		// update becoming visible. Report the state the caller can act on.
		return model.APIKeyAdmin{}, ErrPersonalTenantUnassigned
	}
	if err != nil {
		return model.APIKeyAdmin{}, err
	}
	var tenantModels []string
	if err := json.Unmarshal([]byte(tenantModelsJSON), &tenantModels); err != nil {
		return model.APIKeyAdmin{}, fmt.Errorf("decode tenant allowed_models: %w", err)
	}
	if tenantModels == nil {
		tenantModels = []string{model.AllModelsWildcard}
	}

	effectiveModels, err := resolvePersonalKeyModels(tenantModels, in.AllowedModels)
	if err != nil {
		return model.APIKeyAdmin{}, err
	}

	var owned int64
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM gateway_api_key WHERE owner_user_id=$1::uuid`, in.OwnerUserID).Scan(&owned); err != nil {
		return model.APIKeyAdmin{}, err
	}
	if owned >= limit {
		return model.APIKeyAdmin{}, &PersonalKeyLimitError{Limit: limit}
	}

	modelsJSON, _ := json.Marshal(effectiveModels)
	allowedIPs := in.AllowedIPs
	if allowedIPs == nil {
		// The column is NOT NULL. "No restriction" is an empty list, not JSON
		// null, so the stored value reads the same to every consumer.
		allowedIPs = []string{}
	}
	ipsJSON, _ := json.Marshal(allowedIPs)
	var v model.APIKeyAdmin
	err = tx.QueryRow(ctx, `
INSERT INTO gateway_api_key(
	tenant_id, owner_user_id, name, key_prefix, key_digest, status, expire_at,
	allowed_models, allowed_ips
)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, 1, $6, $7::jsonb, $8::jsonb)
RETURNING id::text, created_at
`,
		*tenantID, in.OwnerUserID, in.Name, in.KeyPrefix, in.KeyDigest, in.ExpireAt,
		string(modelsJSON), string(ipsJSON),
	).Scan(&v.ID, &v.CreatedAt)
	if err != nil {
		return model.APIKeyAdmin{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return model.APIKeyAdmin{}, err
	}

	v.TenantID = *tenantID
	v.TenantCode = tenantCode
	v.TenantName = tenantName
	v.Name = in.Name
	v.KeyPrefix = in.KeyPrefix
	v.Status = 1
	v.AllowedModels = effectiveModels
	v.AllowedIPs = allowedIPs
	v.ExpireAt = in.ExpireAt
	return v, nil
}

// resolvePersonalKeyModels applies the Tenant allowlist to a requested scope.
//
// The three cases are the spec's, and they are not interchangeable:
//   - Tenant ["*"]: any requested explicit list is a subset, and a requested
//     wildcard stays "*" because the Tenant itself is unrestricted.
//   - Tenant explicit: a requested wildcard expands to the Tenant's current
//     list, so a key issued today cannot silently acquire a model added
//     tomorrow; an explicit request must be a subset.
//   - Tenant []: no personal key may be written at all, including with an
//     empty scope, because the Tenant has denied every model.
func resolvePersonalKeyModels(tenantModels, requested []string) ([]string, error) {
	if len(tenantModels) == 0 {
		return nil, ErrPersonalTenantModelsEmpty
	}

	tenantWildcard := containsModel(tenantModels, model.AllModelsWildcard)
	requestedWildcard := containsModel(requested, model.AllModelsWildcard)

	if requestedWildcard {
		if tenantWildcard {
			return []string{model.AllModelsWildcard}, nil
		}
		out := append([]string(nil), tenantModels...)
		sort.Strings(out)
		return dedupeStrings(out), nil
	}

	if tenantWildcard {
		out := append([]string{}, requested...)
		sort.Strings(out)
		return dedupeStrings(out), nil
	}

	allowed := make(map[string]struct{}, len(tenantModels))
	for _, name := range tenantModels {
		allowed[name] = struct{}{}
	}
	for _, name := range requested {
		if _, ok := allowed[name]; !ok {
			return nil, &PersonalModelNotAllowedError{Model: name}
		}
	}
	out := append([]string{}, requested...)
	sort.Strings(out)
	return dedupeStrings(out), nil
}

func containsModel(list []string, want string) bool {
	for _, name := range list {
		if name == want {
			return true
		}
	}
	return false
}

func dedupeStrings(list []string) []string {
	if len(list) < 2 {
		return list
	}
	out := list[:1]
	for _, name := range list[1:] {
		if name != out[len(out)-1] {
			out = append(out, name)
		}
	}
	return out
}
