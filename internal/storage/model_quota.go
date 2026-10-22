package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// This file is the storage half of the per-model quota axis. Read the header of
// migrations/0019_tenant_hierarchy_and_model_quota.sql first: the three states of
// a limit column (NULL = inherit, 0 = unlimited, >0 = cap) and the meaning of a
// NULL tenant_id (the system-wide pool) are defined there and relied on
// everywhere below.

// SystemPoolLabel is how the system-wide pool is named in a rejection. It is
// exported because the admin layer reuses it when it reports where an
// inherited value came from.
const SystemPoolLabel = "全校"

// modelQuotaColumns is the canonical projection of a quota row. It must stay in
// sync with scanModelQuota.
const modelQuotaColumns = `q.tenant_id::text, COALESCE(t.code, ''), COALESCE(t.name, ''),
       q.model_alias, q.daily_token_limit, q.monthly_token_limit`

func scanModelQuota(row rowScanner) (model.ModelQuota, error) {
	var q model.ModelQuota
	if err := row.Scan(&q.TenantID, &q.TenantCode, &q.TenantName, &q.ModelAlias,
		&q.DailyTokenLimit, &q.MonthlyTokenLimit); err != nil {
		return model.ModelQuota{}, err
	}
	return q, nil
}

// rowScanner (defined in personal_request_logs.go) is implemented by both
// pgx.Row and pgx.Rows, so scanModelQuota works for single-row and multi-row
// queries alike.

// normalizeAlias trims a model alias and rejects the empty string. An alias is
// the whole identity of a quota row, so an empty one would create a pool that
// no request can ever match -- silently configured and silently inert.
func normalizeAlias(alias string) (string, error) {
	trimmed := strings.TrimSpace(alias)
	if trimmed == "" {
		return "", fmt.Errorf("model alias is required")
	}
	if len(trimmed) > 128 {
		return "", fmt.Errorf("model alias is too long (max 128 characters)")
	}
	return trimmed, nil
}

// ListModelQuotas returns every quota row, system pools first, then grouped by
// tenant in hierarchy order and by model. The admin listing renders in this
// order, so no client-side sorting is needed.
//
// A non-nil scopeTenantID restricts the result to the system pools (which a
// scoped administrator cannot edit but must be able to see, since they are the
// ceiling) plus the rows of tenants inside that subtree.
func (p *Postgres) ListModelQuotas(ctx context.Context, scopeTenantID *string) ([]model.ModelQuota, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT `+modelQuotaColumns+`
FROM gateway_model_quota q
LEFT JOIN gateway_tenant t ON t.id = q.tenant_id
WHERE $1::uuid IS NULL
   OR q.tenant_id IS NULL
   OR t.path LIKE (SELECT path || '%' FROM gateway_tenant WHERE id = $1::uuid)
ORDER BY (q.tenant_id IS NULL) DESC, COALESCE(t.path, ''), q.model_alias
`, scopeTenantID)
	if err != nil {
		return nil, fmt.Errorf("list model quotas: %w", err)
	}
	defer rows.Close()

	out := make([]model.ModelQuota, 0, 16)
	for rows.Next() {
		q, err := scanModelQuota(rows)
		if err != nil {
			return nil, fmt.Errorf("scan model quota: %w", err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// ModelAliasExists reports whether the catalog contains this public model name.
// A quota row is keyed by alias, so a typo would otherwise create a cap that no
// request can ever match.
func (p *Postgres) ModelAliasExists(ctx context.Context, alias string) (bool, error) {
	var exists bool
	if err := p.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM gateway_model WHERE alias = $1)`, alias).Scan(&exists); err != nil {
		return false, fmt.Errorf("check model alias: %w", err)
	}
	return exists, nil
}

// UpsertModelQuota writes one (subject, model) pair.
//
// The update and the insert are separate statements rather than one ON CONFLICT
// because the conflict target would have to match a NULLS NOT DISTINCT index,
// and the comparison of a NULL tenant_id is exactly the part that is easy to
// get subtly wrong. An explicit UPDATE ... WHERE tenant_id IS NOT DISTINCT FROM
// says what it means. The unique index still backstops the rare concurrent
// insert, which surfaces as an error rather than a duplicate row.
func (p *Postgres) UpsertModelQuota(ctx context.Context, a model.ModelQuotaAllocation) (model.ModelQuota, error) {
	alias, err := normalizeAlias(a.ModelAlias)
	if err != nil {
		return model.ModelQuota{}, err
	}
	// A nil tenant id means the system pool, so getting this normalisation wrong
	// would silently turn "this tenant" into "everyone".
	tenantID := model.NormalizeOptionalID(a.TenantID)

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return model.ModelQuota{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// A non-NULL tenant must exist. Without this the insert would fail on the
	// foreign key with an opaque message; naming the problem is worth one
	// extra lookup on an admin path.
	if tenantID != nil {
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM gateway_tenant WHERE id = $1::uuid)`, *tenantID).Scan(&exists); err != nil {
			return model.ModelQuota{}, fmt.Errorf("check tenant: %w", err)
		}
		if !exists {
			return model.ModelQuota{}, ErrTenantNotFound
		}
	}

	var updated int
	err = tx.QueryRow(ctx, `
UPDATE gateway_model_quota
SET daily_token_limit = $3,
    monthly_token_limit = $4,
    updated_at = NOW()
WHERE tenant_id IS NOT DISTINCT FROM $1::uuid
  AND model_alias = $2
RETURNING 1
`, tenantID, alias, a.DailyTokenLimit, a.MonthlyTokenLimit).Scan(&updated)
	switch {
	case err == nil:
		// updated; fall through to the read-back below
	case !isNoRows(err):
		return model.ModelQuota{}, fmt.Errorf("update model quota: %w", err)
	default:
		if _, err := tx.Exec(ctx, `
INSERT INTO gateway_model_quota(tenant_id, model_alias, daily_token_limit, monthly_token_limit)
VALUES ($1::uuid, $2, $3, $4)
`, tenantID, alias, a.DailyTokenLimit, a.MonthlyTokenLimit); err != nil {
			return model.ModelQuota{}, fmt.Errorf("insert model quota: %w", err)
		}
	}

	row := tx.QueryRow(ctx, `
SELECT `+modelQuotaColumns+`
FROM gateway_model_quota q
LEFT JOIN gateway_tenant t ON t.id = q.tenant_id
WHERE q.tenant_id IS NOT DISTINCT FROM $1::uuid
  AND q.model_alias = $2
`, tenantID, alias)
	out, err := scanModelQuota(row)
	if err != nil {
		return model.ModelQuota{}, fmt.Errorf("read back model quota: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.ModelQuota{}, err
	}
	return out, nil
}

// DeleteModelQuota removes one (subject, model) pair. Deleting a row is how a
// subject goes back to inheriting, so a missing row is not an error worth
// distinguishing -- but it is reported, so the UI can say "there was nothing to
// remove" instead of claiming success.
func (p *Postgres) DeleteModelQuota(ctx context.Context, tenantID *string, alias string) (bool, error) {
	trimmed, err := normalizeAlias(alias)
	if err != nil {
		return false, err
	}
	tag, err := p.Pool.Exec(ctx, `
DELETE FROM gateway_model_quota
WHERE tenant_id IS NOT DISTINCT FROM $1::uuid
  AND model_alias = $2
`, model.NormalizeOptionalID(tenantID), trimmed)
	if err != nil {
		return false, fmt.Errorf("delete model quota: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// modelQuotaRow is one raw row with its nullable limits preserved. The pointers
// are what keep "inherit" distinct from "unlimited" during the walk.
type modelQuotaRow struct {
	daily   *int64
	monthly *int64
}

// loadModelQuotaRows fetches the quota rows that can affect one model for one
// tenant chain: the system pool plus any row held by a tenant on the chain.
// Rows belonging to anyone else are irrelevant, so they are filtered out in the
// database rather than in Go.
func (p *Postgres) loadModelQuotaRows(ctx context.Context, chain []model.TenantQuota, alias string) (modelQuotaRow, bool, map[string]modelQuotaRow, error) {
	var system modelQuotaRow
	var hasSystem bool
	byTenant := make(map[string]modelQuotaRow, len(chain))

	ids := make([]string, 0, len(chain))
	for _, t := range chain {
		ids = append(ids, t.TenantID)
	}

	rows, err := p.Pool.Query(ctx, `
SELECT tenant_id::text, daily_token_limit, monthly_token_limit
FROM gateway_model_quota
WHERE model_alias = $1
  AND (tenant_id IS NULL OR tenant_id = ANY($2::uuid[]))
`, alias, ids)
	if err != nil {
		return system, false, nil, fmt.Errorf("load model quota rows: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tenantID *string
		var r modelQuotaRow
		if err := rows.Scan(&tenantID, &r.daily, &r.monthly); err != nil {
			return system, false, nil, fmt.Errorf("scan model quota row: %w", err)
		}
		if tenantID == nil {
			system, hasSystem = r, true
			continue
		}
		byTenant[*tenantID] = r
	}
	if err := rows.Err(); err != nil {
		return system, false, nil, err
	}
	return system, hasSystem, byTenant, nil
}

// ResolveModelQuotaLevels builds the enforcing levels of the model quota chain
// for one model, with inheritance already applied. The chain is walked root
// first: the system pool, then the tenant chain.
//
// The tenant chain may be empty. That is the case when the caller only wants the
// campus-wide pool -- notably when resolving the ceiling for a root tenant,
// which has no ancestor to defer to but is still bounded by the shared pool.
// Returning nothing there would silently let a root tenant allocate itself an
// arbitrary amount of a model.
//
// A level is emitted only when the subject actually holds a row for this model.
// That differs from the tenant quota chain, where every level with an effective
// cap gets a counter, and the difference is deliberate. On the tenant axis the
// per-level counters are read back by the usage panel, so one per tenant is
// useful. On the model axis they are enforcement only, and a tenant that
// inherits the system pool's value is already bounded by the system pool's own
// counter -- emitting a redundant counter per tenant would multiply Redis keys
// by the number of models with no constraint added.
//
// The consequence to be aware of: a tenant cannot escape the system pool by
// setting 0 ("unlimited") for a model. 0 removes that tenant's own cap; the
// campus-wide cap still applies, because it is a separate level. That is the
// inclusive semantics the tenant axis already uses.
func (p *Postgres) ResolveModelQuotaLevels(ctx context.Context, chain []model.TenantQuota, alias string) ([]model.ModelQuotaLevel, error) {
	alias, err := normalizeAlias(alias)
	if err != nil {
		return nil, nil // an unparseable alias cannot match a pool; nothing to enforce
	}

	system, hasSystem, byTenant, err := p.loadModelQuotaRows(ctx, chain, alias)
	if err != nil {
		return nil, err
	}
	return resolveModelQuotaLevels(chain, alias, system, hasSystem, byTenant), nil
}

// resolveModelQuotaLevels collects the levels that actually enforce a cap.
//
// Each level contributes its OWN declaration, not a running inherited number. A
// model quota counter is inclusive: the system pool already counts every
// tenant's usage, and a parent already counts its children's. Handing a level
// its parent's number would charge the same usage once per level; handing it
// only what it declares means the gate checks exactly the caps someone set.
//
// A level that declares nothing -- no row, or a NULL limit -- is skipped, and so
// is an explicit 0, which means "unlimited at this level". Neither can widen a
// parent's cap, because the parent's own level is still in the list.
func resolveModelQuotaLevels(chain []model.TenantQuota, alias string, system modelQuotaRow, hasSystem bool, byTenant map[string]modelQuotaRow) []model.ModelQuotaLevel {
	out := make([]model.ModelQuotaLevel, 0, len(chain)+1)

	if hasSystem && (declaresCap(system.daily) || declaresCap(system.monthly)) {
		out = append(out, model.ModelQuotaLevel{
			Label:             fmt.Sprintf("模型:%s(%s)", alias, SystemPoolLabel),
			ModelAlias:        alias,
			DailyTokenLimit:   system.daily,
			MonthlyTokenLimit: system.monthly,
		})
	}

	for _, t := range chain {
		r, ok := byTenant[t.TenantID]
		if !ok {
			continue
		}
		if !declaresCap(r.daily) && !declaresCap(r.monthly) {
			continue
		}
		out = append(out, model.ModelQuotaLevel{
			Label:             fmt.Sprintf("租户:%s/模型:%s", t.Name, alias),
			TenantID:          t.TenantID,
			ModelAlias:        alias,
			DailyTokenLimit:   r.daily,
			MonthlyTokenLimit: r.monthly,
		})
	}
	return out
}

// declaresCap reports whether a three-state limit is an actual cap. nil
// (inherit) and 0 (explicitly unlimited) both mean "this level does not reject".
func declaresCap(v *int64) bool { return v != nil && *v > 0 }

// EffectiveModelQuotas resolves, for every model that any level of the chain
// configures, what the last tenant in the chain actually gets and where the
// value came from.
//
// The effective value follows the same rule as enforcement: every positive cap
// on the chain applies, so the tightest one wins. A level's 0 (unlimited) or
// NULL (inherit) never lifts a ceiling declared above it.
//
// It exists so the admin UI can answer "I allocated 300, why does it stop at
// 100?" without a database client -- the same reason EffectiveLimits exists for
// the tenant axis.
func (p *Postgres) EffectiveModelQuotas(ctx context.Context, chain []model.TenantQuota) ([]model.EffectiveModelQuota, error) {
	if len(chain) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(chain))
	for _, t := range chain {
		ids = append(ids, t.TenantID)
	}

	rows, err := p.Pool.Query(ctx, `
SELECT q.model_alias, q.tenant_id::text, q.daily_token_limit, q.monthly_token_limit
FROM gateway_model_quota q
WHERE q.tenant_id IS NULL OR q.tenant_id = ANY($1::uuid[])
`, ids)
	if err != nil {
		return nil, fmt.Errorf("load effective model quotas: %w", err)
	}
	defer rows.Close()

	type cell struct {
		daily   *int64
		monthly *int64
	}
	// alias -> tenantID ("" for the system pool) -> raw row
	byAlias := make(map[string]map[string]cell)
	for rows.Next() {
		var alias string
		var tenantID *string
		var r cell
		if err := rows.Scan(&alias, &tenantID, &r.daily, &r.monthly); err != nil {
			return nil, fmt.Errorf("scan effective model quota: %w", err)
		}
		key := ""
		if tenantID != nil {
			key = *tenantID
		}
		if byAlias[alias] == nil {
			byAlias[alias] = make(map[string]cell)
		}
		byAlias[alias][key] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	aliases := make([]string, 0, len(byAlias))
	for alias := range byAlias {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	leafID := chain[len(chain)-1].TenantID
	out := make([]model.EffectiveModelQuota, 0, len(aliases))
	for _, alias := range aliases {
		cells := byAlias[alias]

		// Each limit keeps both the running tightest value and the level that
		// declared it, because "300, set by 计算机学院" is the answer the
		// operator needs. A nil or zero declaration never replaces a positive
		// one: it adds no cap, it does not remove one.
		type capSource struct {
			value    int64
			label    string
			tenantID string
		}
		var daily, monthly capSource
		tighten := func(cur capSource, v *int64, label, tenantID string) capSource {
			if v == nil || *v <= 0 {
				return cur
			}
			if cur.value == 0 || *v < cur.value {
				return capSource{value: *v, label: label, tenantID: tenantID}
			}
			return cur
		}

		if r, ok := cells[""]; ok {
			daily = tighten(daily, r.daily, SystemPoolLabel, "")
			monthly = tighten(monthly, r.monthly, SystemPoolLabel, "")
		}
		for _, t := range chain {
			r, ok := cells[t.TenantID]
			if !ok {
				continue
			}
			daily = tighten(daily, r.daily, t.Name, t.TenantID)
			monthly = tighten(monthly, r.monthly, t.Name, t.TenantID)
		}
		if daily.value == 0 && monthly.value == 0 {
			continue // rows that cap nothing are configuration noise, not results
		}

		// SetBy names the source of the daily cap, or the monthly one when daily
		// is uncapped. When the two limits come from different levels this is
		// the daily source; the per-limit numbers are still exact.
		setBy, sourceTenant := daily.label, daily.tenantID
		if daily.value == 0 {
			setBy, sourceTenant = monthly.label, monthly.tenantID
		}

		out = append(out, model.EffectiveModelQuota{
			ModelAlias:        alias,
			DailyTokenLimit:   daily.value,
			MonthlyTokenLimit: monthly.value,
			SetBy:             setBy,
			Own:               sourceTenant != "" && sourceTenant == leafID,
		})
	}
	return out, nil
}

// EffectiveModelQuotasForTenants resolves several subjects for one admin page.
//
// It deliberately reuses EffectiveModelQuotas rather than reimplementing the
// tightest-positive rule: two copies of that rule is how the console and the
// limiter drifted apart once already. The cost is one quota query per distinct
// subject, bounded by the configured rows on screen rather than by request
// volume, and the tenant paths are read only for the subjects asked for.
func (p *Postgres) EffectiveModelQuotasForTenants(ctx context.Context, tenantIDs []string) (map[string][]model.EffectiveModelQuota, error) {
	out := make(map[string][]model.EffectiveModelQuota, len(tenantIDs))

	ids := make([]string, 0, len(tenantIDs))
	seen := make(map[string]struct{}, len(tenantIDs))
	for _, id := range tenantIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return out, nil
	}

	paths := make(map[string]string, len(ids))
	ancestors := make(map[string]struct{}, len(ids))
	rows, err := p.Pool.Query(ctx, `
SELECT id::text, path
FROM gateway_tenant
WHERE id = ANY($1::uuid[])
`, ids)
	if err != nil {
		return nil, fmt.Errorf("load tenant paths for effective model quotas: %w", err)
	}
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan tenant path: %w", err)
		}
		paths[id] = path
		for _, ancestor := range ParseTenantPath(path) {
			ancestors[ancestor] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	all := make([]string, 0, len(ancestors))
	for id := range ancestors {
		all = append(all, id)
	}
	names := make(map[string]string, len(all))
	nameRows, err := p.Pool.Query(ctx, `
SELECT id::text, name
FROM gateway_tenant
WHERE id = ANY($1::uuid[])
`, all)
	if err != nil {
		return nil, fmt.Errorf("load tenant names for effective model quotas: %w", err)
	}
	for nameRows.Next() {
		var id, name string
		if err := nameRows.Scan(&id, &name); err != nil {
			nameRows.Close()
			return nil, fmt.Errorf("scan tenant name: %w", err)
		}
		names[id] = name
	}
	if err := nameRows.Err(); err != nil {
		nameRows.Close()
		return nil, err
	}
	nameRows.Close()

	for _, id := range ids {
		path, ok := paths[id]
		if !ok {
			continue
		}
		chain := make([]model.TenantQuota, 0, len(ParseTenantPath(path)))
		for _, ancestor := range ParseTenantPath(path) {
			chain = append(chain, model.TenantQuota{TenantID: ancestor, Name: names[ancestor]})
		}
		effective, err := p.EffectiveModelQuotas(ctx, chain)
		if err != nil {
			return nil, err
		}
		out[id] = effective
	}
	return out, nil
}

// isNoRows reports whether an error is pgx's "no rows" sentinel. It is a named
// helper so the upsert reads as a three-way branch (updated / missing / failed)
// rather than burying errors.Is in the middle of the expression.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
