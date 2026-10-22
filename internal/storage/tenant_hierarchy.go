package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation reports whether err is a unique-constraint violation.
//
// The SQLSTATE is the contract; matching on message text would break the moment
// the server runs in another locale. The only unique key a tenant write can hit
// is the code, which is globally unique by design.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

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
	// ErrTenantCodeTaken is returned when a code collides with an existing
	// tenant. The code is globally unique, and the console has to be able to say
	// which field is wrong: reporting the collision as "tenant not found" sends
	// the operator looking for a tenant that is right in front of them.
	ErrTenantCodeTaken = errors.New("tenant code is already in use")
	// ErrTenantHasChildren is returned when a delete would orphan a subtree.
	// Deletion is restricted rather than cascading, so a whole department
	// cannot disappear because someone removed its root.
	ErrTenantHasChildren = errors.New("tenant still has child tenants")
	// ErrTenantInUse is returned when something other than an API Key still
	// references the tenant -- notably an administrator scoped to it. Deleting
	// would either orphan the reference or, if it were a SET NULL, silently
	// promote that account to global reach.
	ErrTenantInUse = errors.New("tenant is still referenced by an administrator")
)

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
		v.RPMLimit, v.TPMLimit, v.MaxConcurrency, allowedModelsJSON(v.AllowedModels),
	)
	out, err := scanTenant(row)
	if isUniqueViolation(err) {
		return model.Tenant{}, ErrTenantCodeTaken
	}
	if err != nil {
		return model.Tenant{}, err
	}
	return out, nil
}

// TenantUpdate is the input to UpdateTenant. It folds the allowlist pointer in
// with the editable fields so there is one source of truth: AllowedModels is
// read, and the embedded Tenant's own same-named field is not.
//
// The pointer distinguishes an omitted field (nil: leave the column alone) from
// an explicit [] (a non-nil empty slice: deny every model).
type TenantUpdate struct {
	model.Tenant
	AllowedModels *[]string
}

// UpdateTenant rewrites a tenant's mutable identity fields. Reparenting is
// deliberately not part of this: it rewrites the whole subtree, so it lives in
// MoveTenant where the cost is explicit.
//
// The quotas are not part of this write either. They are three-state since
// 0019, and a JSON body cannot distinguish "omitted" from "explicitly null" in
// a way that would let a rename avoid resetting a budget. Renaming through this
// statement therefore leaves the allocations exactly as they were; changing one
// is AllocateTenantQuota's job, where a full replacement is the stated
// semantics.
//
// COALESCE keeps the stored allowlist in the omitted case, and doing it in the
// statement rather than a read-then-write means a concurrent allowlist edit
// cannot be reverted by a rename.
func (p *Postgres) UpdateTenant(ctx context.Context, id string, in TenantUpdate) (model.Tenant, error) {
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = model.TenantKindTenant
	}
	var modelsArg any
	if in.AllowedModels != nil {
		modelsArg = string(allowedModelsJSON(*in.AllowedModels))
	}

	row := p.Pool.QueryRow(ctx, `
UPDATE gateway_tenant
SET code=$2, name=$3, status=$4, kind=$5,
    allowed_models=COALESCE($6::jsonb, allowed_models),
    updated_at=NOW()
WHERE id=$1::uuid
RETURNING `+tenantColumns,
		id, in.Code, in.Name, in.Status, kind, modelsArg,
	)
	out, err := scanTenant(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Tenant{}, ErrTenantNotFound
	}
	if isUniqueViolation(err) {
		return model.Tenant{}, ErrTenantCodeTaken
	}
	if err != nil {
		return model.Tenant{}, err
	}
	return out, nil
}

// MoveTenant reparents a tenant and rewrites the materialized paths of its
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
// The effective quota is the TIGHTEST positive cap along the chain, not the
// nearest declaration. Enforcement is inclusive: every level that declares a
// positive cap gets its own counter, so a child's larger number or explicit 0
// cannot lift an ancestor's cap. Reporting the nearest declaration here would
// print a number the limiter never enforces -- the exact discrepancy this
// reporting surface exists to prevent.
func ResolveEffectiveLimits(chain []model.Tenant) model.EffectiveLimits {
	if len(chain) == 0 {
		return model.EffectiveLimits{}
	}
	leaf := chain[len(chain)-1]

	// Each limit is the tightest positive declaration on the chain. nil means
	// inherit and 0 means "this level adds no cap"; neither one can lift an
	// ancestor's cap, so both are skipped. ResolveQuotas deliberately returns
	// per-level declarations instead, so the collapse has to happen here rather
	// than being read off the chain.
	var daily, monthly, requests, rpm, tpm, concurrency, budget int64
	for _, t := range chain {
		daily = tightenCap(daily, t.DailyTokenLimit)
		monthly = tightenCap(monthly, t.MonthlyTokenLimit)
		requests = tightenCap(requests, t.MonthlyRequestLimit)
		rpm = tightenCap(rpm, t.RPMLimit)
		tpm = tightenCap(tpm, t.TPMLimit)
		concurrency = tightenCap(concurrency, t.MaxConcurrency)
		// monthly_budget_micros is stored but not enforced, so it is reported
		// with the same tightest-positive rule for consistency. 0 means "no
		// budget configured" rather than an explicit declaration.
		budget = tightenCap(budget, &t.MonthlyBudgetMicros)
	}

	return model.EffectiveLimits{
		TenantID:            leaf.ID,
		DailyTokenLimit:     daily,
		MonthlyTokenLimit:   monthly,
		MonthlyRequestLimit: requests,
		RPMLimit:            rpm,
		TPMLimit:            tpm,
		MaxConcurrency:      concurrency,
		MonthlyBudgetMicros: budget,
		Depth:               leaf.Depth,
		Path:                leaf.Path,
	}
}

// tightenCap keeps the smallest positive declaration seen so far. nil (inherit)
// and 0 (this level adds no cap) leave the running value alone, so a level can
// tighten a ceiling but never widen one.
func tightenCap(current int64, v *int64) int64 {
	if v == nil || *v <= 0 {
		return current
	}
	if current == 0 || *v < current {
		return *v
	}
	return current
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
// Only the allocation columns are written: identity (code, name, kind, parent),
// allowed_models and status are left alone, so allocating a budget cannot
// accidentally reparent a tenant, rename it, or change what its members may
// write to a personal API Key.
//
// monthly_budget_micros is the one limit that is NOT three-state: the column is
// NOT NULL, because the third state ("inherit") was never given an enforcement
// path. An omitted value therefore means 0 rather than NULL, which COALESCE
// expresses without making the caller know the column's nullability.
func (p *Postgres) AllocateTenantQuota(ctx context.Context, id string, a model.TenantAllocation) (model.Tenant, error) {
	row := p.Pool.QueryRow(ctx, `
UPDATE gateway_tenant
SET daily_token_limit     = $2,
    monthly_token_limit   = $3,
    monthly_request_limit = $4,
    monthly_budget_micros = COALESCE($5, 0),
    rpm_limit             = $6,
    tpm_limit             = $7,
    max_concurrency       = $8,
    updated_at            = NOW()
WHERE id = $1::uuid
RETURNING `+tenantColumns,
		id,
		a.DailyTokenLimit, a.MonthlyTokenLimit, a.MonthlyRequestLimit,
		a.MonthlyBudgetMicros, a.RPMLimit, a.TPMLimit, a.MaxConcurrency,
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

// TenantPath returns the materialized path of one tenant.
//
// It exists for the paths that hold an id from a request body and need to know
// whether that tenant exists at all before deciding anything else — scoping an
// administrator to a tenant, for instance. Answering with ErrTenantNotFound
// keeps the caller from having to distinguish "no such tenant" from "the query
// failed".
func (p *Postgres) TenantPath(ctx context.Context, tenantID string) (string, error) {
	var path string
	err := p.Pool.QueryRow(ctx, `SELECT path FROM gateway_tenant WHERE id=$1::uuid`, tenantID).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTenantNotFound
	}
	if err != nil {
		return "", err
	}
	return path, nil
}

// TenantExists reports whether a tenant id names a row. It is the cheap form of
// TenantPath for callers that only need to reject a bad id before writing a
// foreign key that would fail with an opaque constraint error.
func (p *Postgres) TenantExists(ctx context.Context, tenantID string) (bool, error) {
	if tenantID == "" {
		return false, nil
	}
	var ok bool
	err := p.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM gateway_tenant WHERE id=$1::uuid)`, tenantID).Scan(&ok)
	return ok, err
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
