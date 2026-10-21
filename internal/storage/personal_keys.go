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

	// ErrPersonalKeyNotFound means the id does not name a key owned by the
	// session's account. A key owned by another Account deliberately produces
	// the same value: the endpoint must not reveal that it exists.
	ErrPersonalKeyNotFound = errors.New("personal API key not found")
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
	// Limit is the per-Account ceiling, resolved by the config layer. A
	// non-positive value is rejected rather than defaulted again here.
	Limit int64
}

// PersonalKeyUpdate is the write-side input of a personal edit.
//
// Like PersonalKeyCreate it has no TenantID, no OwnerUserID and no Status.
// Disable/re-enable is a separate operation (SetPersonalAPIKeyStatus), so an
// edit body has nowhere to land a status change even if a client sends one.
type PersonalKeyUpdate struct {
	OwnerUserID   string
	Name          string
	AllowedModels []string
	AllowedIPs    []string
	ExpireAt      *time.Time
}

// ListAPIKeysByOwner returns only the keys the Account owns. The owner is a
// bound parameter, never a filter the caller can widen: a request that names
// another account simply matches nothing.
//
// The projection is the admin list's minus nothing secret — the digest is not
// selected at all, so the list cannot return a full key even if a template
// later tried to.
func (p *Postgres) ListAPIKeysByOwner(ctx context.Context, ownerID string) ([]model.APIKeyAdmin, error) {
	rows, err := p.Pool.Query(ctx, `SELECT `+apiKeyAdminProjection+`
WHERE k.owner_user_id = $1::uuid
ORDER BY k.created_at DESC
`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.APIKeyAdmin, 0, 8)
	for rows.Next() {
		v, err := scanAPIKeyAdmin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CountPersonalAPIKeysByOwner is the cheap count /api/console/me carries as a
// hint. It is owner-scoped in SQL like every personal read and rides the
// owner_user_id index added by migration 0018; no key row is projected.
func (p *Postgres) CountPersonalAPIKeysByOwner(ctx context.Context, ownerID string) (int64, error) {
	var n int64
	err := p.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM gateway_api_key WHERE owner_user_id=$1::uuid`, ownerID).Scan(&n)
	return n, err
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
	// The ceiling is configuration, not a storage constant. The handler passes
	// cfg.PersonalKeyLimit (already defaulted and validated at startup); a
	// non-positive value here means a caller bypassed that path, and silently
	// substituting a number would make the limit disagree with the config the
	// operator set.
	if in.Limit <= 0 {
		return model.APIKeyAdmin{}, fmt.Errorf("personal key limit must be positive, got %d", in.Limit)
	}
	limit := in.Limit

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

// GetPersonalAPIKey returns one key owned by the Account.
//
// The owner is part of the WHERE clause rather than a check after the read, so
// another Account's id is indistinguishable from an id that does not exist. The
// projection is the list's: no digest, and no full credential is selectable.
func (p *Postgres) GetPersonalAPIKey(ctx context.Context, id, ownerID string) (model.APIKeyAdmin, error) {
	v, err := scanAPIKeyAdmin(p.Pool.QueryRow(ctx, `SELECT `+apiKeyAdminProjection+`
WHERE k.id=$1::uuid AND k.owner_user_id=$2::uuid
`, id, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.APIKeyAdmin{}, ErrPersonalKeyNotFound
	}
	if err != nil {
		return model.APIKeyAdmin{}, err
	}
	return v, nil
}

// UpdatePersonalAPIKey replaces the editable part of a key: name, model scope,
// IP allowlist and expiry.
//
// The row is locked through the owner-scoped lookup first. Its tenant_id is read
// from the row, never from the caller, and the new model scope is resolved
// against that Tenant's current allowlist with the same three rules as
// creation. Nothing else on the row is written: status has its own operation,
// and tenant and owner have no input path at all.
func (p *Postgres) UpdatePersonalAPIKey(ctx context.Context, id string, in PersonalKeyUpdate) (model.APIKeyAdmin, error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return model.APIKeyAdmin{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantID string
	err = tx.QueryRow(ctx, `
SELECT tenant_id::text
FROM gateway_api_key
WHERE id=$1::uuid AND owner_user_id=$2::uuid
FOR UPDATE
`, id, in.OwnerUserID).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.APIKeyAdmin{}, ErrPersonalKeyNotFound
	}
	if err != nil {
		return model.APIKeyAdmin{}, err
	}

	var tenantCode, tenantName, tenantModelsJSON string
	err = tx.QueryRow(ctx, `
SELECT code, name, allowed_models::text
FROM gateway_tenant
WHERE id=$1::uuid
`, tenantID).Scan(&tenantCode, &tenantName, &tenantModelsJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		// The key's tenant_id is NOT NULL, so this is only reachable if the
		// Tenant was deleted in the same instant. Report the state the write
		// depends on rather than a generic database error.
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

	allowedIPs := in.AllowedIPs
	if allowedIPs == nil {
		allowedIPs = []string{}
	}
	modelsJSON, _ := json.Marshal(effectiveModels)
	ipsJSON, _ := json.Marshal(allowedIPs)

	var v model.APIKeyAdmin
	err = tx.QueryRow(ctx, `
UPDATE gateway_api_key
SET name=$3,
	expire_at=$4,
	allowed_models=$5::jsonb,
	allowed_ips=$6::jsonb,
	updated_at=NOW()
WHERE id=$1::uuid AND owner_user_id=$2::uuid
RETURNING key_prefix, status, last_used_at, created_at
`, id, in.OwnerUserID, in.Name, in.ExpireAt, string(modelsJSON), string(ipsJSON)).
		Scan(&v.KeyPrefix, &v.Status, &v.LastUsedAt, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.APIKeyAdmin{}, ErrPersonalKeyNotFound
	}
	if err != nil {
		return model.APIKeyAdmin{}, err
	}

	// Usage stays derived from the log, exactly as the list reports it.
	if err := tx.QueryRow(ctx, `
SELECT COUNT(*)::bigint, COALESCE(SUM(COALESCE(total_tokens,0)),0)::bigint
FROM gateway_request_log
WHERE api_key_id=$1::uuid
`, id).Scan(&v.UsageRequests, &v.UsageTokens); err != nil {
		return model.APIKeyAdmin{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return model.APIKeyAdmin{}, err
	}

	v.ID = id
	v.TenantID = tenantID
	v.TenantCode = tenantCode
	v.TenantName = tenantName
	v.Name = in.Name
	v.AllowedModels = effectiveModels
	v.AllowedIPs = allowedIPs
	v.ExpireAt = in.ExpireAt
	return v, nil
}

// SetPersonalAPIKeyStatus enables or disables a key the Account owns.
//
// This is a dedicated operation rather than a field on the edit body: pausing
// an integration and renaming it are different decisions, and keeping them
// apart means neither request can carry the other's change. The credential is
// untouched, so re-enabling restores service with the same key. Only status and
// updated_at are written.
func (p *Postgres) SetPersonalAPIKeyStatus(ctx context.Context, id, ownerID string, status int) error {
	tag, err := p.Pool.Exec(ctx, `
UPDATE gateway_api_key
SET status=$3, updated_at=NOW()
WHERE id=$1::uuid AND owner_user_id=$2::uuid
`, id, ownerID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPersonalKeyNotFound
	}
	return nil
}

// DeletePersonalAPIKey removes the key row outright.
//
// The credential dies with the row. Request Records are not deleted: they keep
// api_key_id (there is no foreign key) and the owner snapshot that migration
// 0018 added, so the owner's history stays attributable after the key is gone.
func (p *Postgres) DeletePersonalAPIKey(ctx context.Context, id, ownerID string) error {
	tag, err := p.Pool.Exec(ctx,
		`DELETE FROM gateway_api_key WHERE id=$1::uuid AND owner_user_id=$2::uuid`, id, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPersonalKeyNotFound
	}
	return nil
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
