package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Tenant hierarchy errors. They are returned by the storage layer and mapped to
// HTTP status codes by the handlers.
var (
	// ErrTenantNotFound is returned when a tenant id does not exist.
	ErrTenantNotFound = errors.New("tenant not found")
	// ErrTenantParentNotFound is returned when the requested parent does not
	// exist. Creating a node under a missing parent would produce an orphan with
	// an unreachable quota chain, so it is rejected outright.
	ErrTenantParentNotFound = errors.New("parent tenant not found")
	// ErrTenantCycle is returned when a move would make a tenant its own
	// ancestor.
	ErrTenantCycle = errors.New("tenant move would create a cycle")
)

// rowScanner is implemented by both pgx.Row and pgx.Rows, so scanTenant works
// for single-row and multi-row queries alike.
type rowScanner interface{ Scan(dest ...any) error }

// scanTenant reads one tenant row in the order of tenantColumns (defined in
// management.go, which is the single place that projection is spelled out).
//
// The six limit columns are nullable and scan into *int64 so that NULL survives
// as nil (= inherit). allowed_models needs the same care in the other direction:
// a NULL JSONB means "inherit" while a present "[]" means "deny everything", and
// only a non-nil raw buffer can tell the two apart.
func scanTenant(row rowScanner) (model.Tenant, error) {
	var t model.Tenant
	var rawModels []byte
	if err := row.Scan(
		&t.ID, &t.Code, &t.Name, &t.Status,
		&t.DailyTokenLimit, &t.MonthlyTokenLimit, &t.MonthlyRequestLimit, &t.MonthlyBudgetMicros,
		&t.RPMLimit, &t.TPMLimit, &t.MaxConcurrency,
		&t.ParentID, &t.Path, &t.Depth, &t.Kind, &rawModels,
	); err != nil {
		return model.Tenant{}, err
	}
	if rawModels != nil {
		t.AllowedModels = []string{}
		if err := json.Unmarshal(rawModels, &t.AllowedModels); err != nil {
			return model.Tenant{}, fmt.Errorf("decode allowed_models for tenant %s: %w", t.ID, err)
		}
	}
	return t, nil
}

// marshalModels encodes an allowlist for the JSONB column.
//
//	nil      -> SQL NULL   (inherit from ancestors)
//	[]       -> "[]"       (explicitly deny every model)
//	[a, b]   -> "[a,b]"
func marshalModels(models []string) any {
	if models == nil {
		return nil
	}
	if len(models) == 0 {
		return "[]"
	}
	b, err := json.Marshal(models)
	if err != nil {
		// []string cannot fail to marshal; fall back to deny-all rather than
		// silently widening access.
		return "[]"
	}
	return string(b)
}

// ParseTenantPath splits a materialized path into its id segments, root first.
// The stored form is "/<root-id>/<child-id>/", so the leading and trailing
// separators are dropped and empty segments ignored.
func ParseTenantPath(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// BuildTenantPath returns the materialized path for a tenant created under
// parentPath. A nil parentPath makes the tenant a root.
func BuildTenantPath(parentPath *string, id string) string {
	if parentPath == nil || *parentPath == "" {
		return "/" + id + "/"
	}
	return strings.TrimSuffix(*parentPath, "/") + "/" + id + "/"
}

// TenantSubtreePrefix returns the LIKE pattern that matches a tenant and all of
// its descendants. It is also the exact prefix used for scope checks.
func TenantSubtreePrefix(path string) string {
	return strings.TrimSuffix(path, "/") + "/%"
}

// TenantDepthFromPath derives the depth (root = 0) from a materialized path.
func TenantDepthFromPath(path string) int {
	n := len(ParseTenantPath(path))
	if n == 0 {
		return 0
	}
	return n - 1
}

// ResolveQuotas turns a root-first chain into the limiter's view of it.
//
// Each node keeps its OWN declaration rather than an inherited number, and that
// is deliberate. A Quota Counter is inclusive: a parent's counter already
// contains every child's usage, so the parent's cap constrains the whole
// subtree. Walking the chain and checking only the levels that declare a value
// therefore yields exactly the intended semantics.
//
// Resolving an inherited number at each level instead would (a) check the same
// usage once per level, and (b) turn an explicit 0 -- "this level is unlimited"
// -- into the parent's number, which is the one thing a child must not be able
// to do.
func ResolveQuotas(chain []model.Tenant) []model.TenantQuota {
	out := make([]model.TenantQuota, 0, len(chain))
	for _, t := range chain {
		out = append(out, model.TenantQuota{
			TenantID:            t.ID,
			Name:                t.Name,
			DailyTokenLimit:     t.DailyTokenLimit,
			MonthlyTokenLimit:   t.MonthlyTokenLimit,
			MonthlyRequestLimit: t.MonthlyRequestLimit,
			RPMLimit:            t.RPMLimit,
			TPMLimit:            t.TPMLimit,
			MaxConcurrency:      t.MaxConcurrency,
		})
	}
	return out
}

// ResolveAccess walks a root-first chain and intersects the model allowlists.
//
// Access is an intersection rather than an inheritance: a child can never widen
// what an ancestor allows. The returned restricted flag distinguishes "no
// allowlist anywhere" (unrestricted) from "an allowlist that happens to be
// empty" (deny everything).
func ResolveAccess(chain []model.Tenant) (map[string]struct{}, bool) {
	var allowed map[string]struct{}
	restricted := false

	for _, t := range chain {
		if t.InheritsAllowedModels() {
			continue
		}
		set := StringSet(t.AllowedModels)
		if !restricted {
			allowed = set
			restricted = true
			continue
		}
		allowed = IntersectSets(allowed, set)
	}
	return allowed, restricted
}

// StringSet converts a slice into a set. An empty but non-nil slice yields an
// empty (non-nil) set, which is how "deny everything" is represented.
func StringSet(v []string) map[string]struct{} {
	out := make(map[string]struct{}, len(v))
	for _, s := range v {
		out[s] = struct{}{}
	}
	return out
}

// IntersectSets returns the elements present in both sets. The result is always
// non-nil so that callers can rely on it being a valid (possibly empty) set.
func IntersectSets(a, b map[string]struct{}) map[string]struct{} {
	if len(a) > len(b) {
		a, b = b, a
	}
	out := make(map[string]struct{}, len(a))
	for k := range a {
		if _, ok := b[k]; ok {
			out[k] = struct{}{}
		}
	}
	return out
}

// ComposeAccess combines two allowlist constraints, each expressed as
// (set, restricted). "Unrestricted" means no constraint at all.
func ComposeAccess(
	aSet map[string]struct{}, aRestricted bool,
	bSet map[string]struct{}, bRestricted bool,
) (map[string]struct{}, bool) {
	switch {
	case !aRestricted && !bRestricted:
		return nil, false
	case !aRestricted:
		return bSet, true
	case !bRestricted:
		return aSet, true
	default:
		return IntersectSets(aSet, bSet), true
	}
}

// ListTenants lives in management.go, which owns tenantColumns. It orders by
// materialized path, which is a depth-first walk of the tree: a parent always
// precedes its children and siblings stay grouped, so the admin UI can render
// the hierarchy without re-sorting client side.

// FindTenantChain loads the ancestors of tenantID and returns them root first,
// ending with the tenant itself.
func (p *Postgres) FindTenantChain(ctx context.Context, tenantID string) ([]model.Tenant, error) {
	var path string
	err := p.Pool.QueryRow(ctx, `SELECT path FROM gateway_tenant WHERE id=$1::uuid`, tenantID).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTenantNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load tenant path: %w", err)
	}
	return p.FindTenantChainByPath(ctx, path)
}

// FindTenantChainByPath is FindTenantChain for callers that already hold the
// materialized path -- the API key lookup returns it, which saves a round trip
// on the request hot path.
//
// The chain is driven by the path rather than a recursive CTE: the path already
// lists every ancestor id, so this is a single primary-key lookup.
func (p *Postgres) FindTenantChainByPath(ctx context.Context, path string) ([]model.Tenant, error) {
	ids := ParseTenantPath(path)
	if len(ids) == 0 {
		return nil, fmt.Errorf("tenant path %q has no segments", path)
	}

	rows, err := p.Pool.Query(ctx, `
SELECT `+tenantColumns+`
FROM gateway_tenant
WHERE id = ANY($1::uuid[])
`, ids)
	if err != nil {
		return nil, fmt.Errorf("load tenant chain: %w", err)
	}
	defer rows.Close()

	byID := make(map[string]model.Tenant, len(ids))
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, fmt.Errorf("scan tenant chain: %w", err)
		}
		byID[t.ID] = t
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Restore path order (root first). A missing ancestor would mean a broken
	// path, which ON DELETE RESTRICT is supposed to prevent; fail loudly rather
	// than silently dropping a level of the budget hierarchy.
	chain := make([]model.Tenant, 0, len(ids))
	for _, id := range ids {
		t, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("tenant path %q references missing ancestor %s", path, id)
		}
		chain = append(chain, t)
	}
	return chain, nil
}

// CreateTenantNode inserts a tenant, optionally under parentID, and returns the
// stored row.
//
// The uuid is generated here rather than by the database because the
// materialized path embeds it: knowing the id up front turns "insert, then fix
// up the path" into a single statement.
func (p *Postgres) CreateTenant(ctx context.Context, v model.Tenant) (model.Tenant, error) {
	parentID := model.NormalizeOptionalID(v.ParentID)

	var parentPath *string
	depth := 0
	if parentID != nil {
		var pp string
		err := p.Pool.QueryRow(ctx, `SELECT path FROM gateway_tenant WHERE id=$1::uuid`, *parentID).Scan(&pp)
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Tenant{}, ErrTenantParentNotFound
		}
		if err != nil {
			return model.Tenant{}, fmt.Errorf("load parent tenant: %w", err)
		}
		parentPath = &pp
		depth = TenantDepthFromPath(pp) + 1
	}

	id := uuid.NewString()
	path := BuildTenantPath(parentPath, id)

	kind := strings.TrimSpace(v.Kind)
	if kind == "" {
		kind = model.TenantKindTenant
	}

	row := p.Pool.QueryRow(ctx, `
INSERT INTO gateway_tenant(
	id, code, name, status, kind, parent_id, path, depth,
	daily_token_limit, monthly_token_limit, monthly_request_limit, monthly_budget_micros,
	rpm_limit, tpm_limit, max_concurrency, allowed_models
)
VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $7, $8,
        $9, $10, $11, $12, $13, $14, $15, $16::jsonb)
RETURNING `+tenantColumns,
		id, v.Code, v.Name, v.Status, kind, parentID, path, depth,
		v.DailyTokenLimit, v.MonthlyTokenLimit, v.MonthlyRequestLimit, v.MonthlyBudgetMicros,
		v.RPMLimit, v.TPMLimit, v.MaxConcurrency, marshalModels(v.AllowedModels),
	)
	out, err := scanTenant(row)
	if err != nil {
		return model.Tenant{}, err
	}
	return out, nil
}

// UpdateTenantNode rewrites a tenant's mutable fields. Reparenting is
// deliberately not part of this: it rewrites the whole subtree, so it lives in
// MoveTenantNode where the cost is explicit.
func (p *Postgres) UpdateTenant(ctx context.Context, id string, v model.Tenant) (model.Tenant, error) {
	kind := strings.TrimSpace(v.Kind)
	if kind == "" {
		kind = model.TenantKindTenant
	}

	row := p.Pool.QueryRow(ctx, `
UPDATE gateway_tenant
SET code=$2, name=$3, status=$4, kind=$5,
    daily_token_limit=$6, monthly_token_limit=$7, monthly_request_limit=$8, monthly_budget_micros=$9,
    rpm_limit=$10, tpm_limit=$11, max_concurrency=$12, allowed_models=$13::jsonb,
    updated_at=NOW()
WHERE id=$1::uuid
RETURNING `+tenantColumns,
		id, v.Code, v.Name, v.Status, kind,
		v.DailyTokenLimit, v.MonthlyTokenLimit, v.MonthlyRequestLimit, v.MonthlyBudgetMicros,
		v.RPMLimit, v.TPMLimit, v.MaxConcurrency, marshalModels(v.AllowedModels),
	)
	out, err := scanTenant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Tenant{}, ErrTenantNotFound
	}
	if err != nil {
		return model.Tenant{}, err
	}
	return out, nil
}

// MoveTenantNode reparents a tenant and rewrites the materialized paths of its
// entire subtree in a single statement.
//
// The rewrite is a pure string splice: every descendant path starts with the
// node's old path, so replacing that prefix with the new path moves the whole
// subtree at once. depth is adjusted by the constant delta.
func (p *Postgres) MoveTenant(ctx context.Context, id string, newParentID *string) (model.Tenant, error) {
	parentID := model.NormalizeOptionalID(newParentID)

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return model.Tenant{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var oldPath string
	var oldDepth int
	err = tx.QueryRow(ctx, `SELECT path, depth FROM gateway_tenant WHERE id=$1::uuid FOR UPDATE`, id).Scan(&oldPath, &oldDepth)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Tenant{}, ErrTenantNotFound
	}
	if err != nil {
		return model.Tenant{}, fmt.Errorf("load tenant to move: %w", err)
	}

	var newParentPath *string
	newDepth := 0
	if parentID != nil {
		var pp string
		err := tx.QueryRow(ctx, `SELECT path FROM gateway_tenant WHERE id=$1::uuid FOR UPDATE`, *parentID).Scan(&pp)
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Tenant{}, ErrTenantParentNotFound
		}
		if err != nil {
			return model.Tenant{}, fmt.Errorf("load new parent tenant: %w", err)
		}
		// Moving a node under one of its own descendants would detach the
		// subtree from the tree entirely, so reject it before writing anything.
		if strings.HasPrefix(pp, oldPath) {
			return model.Tenant{}, ErrTenantCycle
		}
		newParentPath = &pp
		newDepth = TenantDepthFromPath(pp) + 1
	}

	newPath := BuildTenantPath(newParentPath, id)

	// substring(path from len(oldPath)+1) yields the tail after the old prefix:
	// "" for the node itself, "X/" for /old/X/. Concatenating it onto newPath
	// therefore moves the node and every descendant in one pass.
	if _, err := tx.Exec(ctx, `
UPDATE gateway_tenant
SET path  = $1 || substring(path FROM char_length($2) + 1),
    depth = depth + $3,
    parent_id = CASE WHEN id = $4::uuid THEN $5::uuid ELSE parent_id END,
    updated_at = CASE WHEN id = $4::uuid THEN NOW() ELSE updated_at END
WHERE path LIKE $6
`, newPath, oldPath, newDepth-oldDepth, id, parentID, TenantSubtreePrefix(oldPath)); err != nil {
		return model.Tenant{}, fmt.Errorf("rewrite subtree paths: %w", err)
	}

	row := tx.QueryRow(ctx, `SELECT `+tenantColumns+` FROM gateway_tenant WHERE id=$1::uuid`, id)
	out, err := scanTenant(row)
	if err != nil {
		return model.Tenant{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Tenant{}, err
	}
	return out, nil
}

// CountTenantChildren reports how many direct children a tenant has. Deletion
// is restricted rather than cascading, so callers use this to explain why a
// delete was refused.
func (p *Postgres) CountTenantChildren(ctx context.Context, id string) (int, error) {
	var n int
	err := p.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM gateway_tenant WHERE parent_id=$1::uuid`, id).Scan(&n)
	return n, err
}

// TenantSubtreeIDs returns the ids of a tenant and all of its descendants. It
// backs the "can this admin act on that tenant?" check: an admin scoped to a
// tenant may only manage nodes inside its own subtree.
func (p *Postgres) TenantSubtreeIDs(ctx context.Context, tenantID string) ([]string, error) {
	// path || '%' is exactly TenantSubtreePrefix: "/a/b/" becomes "/a/b/%",
	// which matches the node itself and every descendant.
	rows, err := p.Pool.Query(ctx, `
SELECT id::text
FROM gateway_tenant
WHERE path LIKE (SELECT path || '%' FROM gateway_tenant WHERE id=$1::uuid)
ORDER BY path
`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListTenantsInSubtree returns a tenant together with its descendants, in
// depth-first order. It is the scoped counterpart of ListTenants: an
// administrator confined to a department sees that department and its
// children, and nothing above or beside it.
func (p *Postgres) ListTenantsInSubtree(ctx context.Context, scopeTenantID string) ([]model.Tenant, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT `+tenantColumns+`
FROM gateway_tenant
WHERE path LIKE (SELECT path || '%' FROM gateway_tenant WHERE id=$1::uuid)
ORDER BY path
`, scopeTenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Tenant
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ResolveEffectiveLimits flattens a root-first chain into the single set of
// limits that actually applies to the last tenant in it.
//
// Both axes are reported: the inherited quota and the intersected access. An
// administrator who allocated 5,000,000 tokens and sees usage stop at
// 1,000,000 needs to be able to read back "the effective limit is 1,000,000,
// set by your parent" without opening a database client.
func ResolveEffectiveLimits(chain []model.Tenant) model.EffectiveLimits {
	if len(chain) == 0 {
		return model.EffectiveLimits{}
	}
	leaf := chain[len(chain)-1]

	access, restricted := ResolveAccess(chain)
	models := make([]string, 0, len(access))
	for m := range access {
		models = append(models, m)
	}
	sort.Strings(models)

	// The effective value is the nearest declaration walking down from the root,
	// i.e. the last non-nil value seen. An explicit 0 is a declaration
	// ("unlimited"), not an absence, so it stops the walk like any other value.
	// ResolveQuotas deliberately returns per-level declarations instead, so the
	// collapse has to happen here rather than being read off the chain.
	var daily, monthly, requests, rpm, tpm, concurrency, budget int64
	for _, t := range chain {
		if t.DailyTokenLimit != nil {
			daily = *t.DailyTokenLimit
		}
		if t.MonthlyTokenLimit != nil {
			monthly = *t.MonthlyTokenLimit
		}
		if t.MonthlyRequestLimit != nil {
			requests = *t.MonthlyRequestLimit
		}
		if t.RPMLimit != nil {
			rpm = *t.RPMLimit
		}
		if t.TPMLimit != nil {
			tpm = *t.TPMLimit
		}
		if t.MaxConcurrency != nil {
			concurrency = *t.MaxConcurrency
		}
		// monthly_budget_micros kept the two-state convention (it is stored but
		// not enforced, so a third state would be a distinction nothing reads).
		// With only 0 and >0 available, 0 has to mean "no budget configured"
		// rather than a declaration, so the nearest non-zero value is the one
		// that applies.
		if t.MonthlyBudgetMicros > 0 {
			budget = t.MonthlyBudgetMicros
		}
	}

	return model.EffectiveLimits{
		TenantID:              leaf.ID,
		DailyTokenLimit:       daily,
		MonthlyTokenLimit:     monthly,
		MonthlyRequestLimit:   requests,
		RPMLimit:              rpm,
		TPMLimit:              tpm,
		MaxConcurrency:        concurrency,
		MonthlyBudgetMicros:   budget,
		ModelAccessRestricted: restricted,
		AllowedModels:         models,
		Depth:                 leaf.Depth,
		Path:                  leaf.Path,
	}
}

// EffectiveTenantLimits resolves a tenant's inherited limits.
func (p *Postgres) EffectiveTenantLimits(ctx context.Context, tenantID string) (model.EffectiveLimits, error) {
	chain, err := p.FindTenantChain(ctx, tenantID)
	if err != nil {
		return model.EffectiveLimits{}, err
	}
	return ResolveEffectiveLimits(chain), nil
}

// AllocateTenantQuota replaces the limits a tenant holds.
//
// Only the allocation columns are written: identity (code, name, kind, parent)
// and the rate limits are left alone, so allocating a budget cannot accidentally
// reparent a tenant or reset its name.
func (p *Postgres) AllocateTenantQuota(ctx context.Context, id string, a model.TenantAllocation) (model.Tenant, error) {
	row := p.Pool.QueryRow(ctx, `
UPDATE gateway_tenant
SET daily_token_limit     = $2,
    monthly_token_limit   = $3,
    monthly_request_limit = $4,
    monthly_budget_micros = $5,
    rpm_limit             = $6,
    tpm_limit             = $7,
    max_concurrency       = $8,
    allowed_models        = $9::jsonb,
    updated_at            = NOW()
WHERE id = $1::uuid
RETURNING `+tenantColumns,
		id,
		a.DailyTokenLimit, a.MonthlyTokenLimit, a.MonthlyRequestLimit,
		a.MonthlyBudgetMicros, a.RPMLimit, a.TPMLimit, a.MaxConcurrency,
		marshalModels(a.AllowedModels),
	)
	out, err := scanTenant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Tenant{}, ErrTenantNotFound
	}
	if err != nil {
		return model.Tenant{}, err
	}
	return out, nil
}

// TenantIsDescendant reports whether target sits inside scope's subtree.
//
// The check is a prefix comparison on the materialized path, which is the whole
// reason the path is stored: it turns "is this tenant under that one?" into an
// indexable string comparison rather than a recursive walk.
//
// includeSelf separates the two questions the admin layer asks. Reads are
// allowed on your own tenant (includeSelf = true); writes are not
// (includeSelf = false), because otherwise a scoped administrator could raise
// their own budget.
func (p *Postgres) TenantIsDescendant(ctx context.Context, scopeTenantID, targetTenantID string, includeSelf bool) (bool, error) {
	if scopeTenantID == "" || targetTenantID == "" {
		return false, nil
	}
	var ok bool
	err := p.Pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM gateway_tenant target
    JOIN gateway_tenant scope ON scope.id = $1::uuid
    WHERE target.id = $2::uuid
      AND target.path LIKE scope.path || '%'
      AND ($3 OR target.id <> scope.id)
)
`, scopeTenantID, targetTenantID, includeSelf).Scan(&ok)
	return ok, err
}
