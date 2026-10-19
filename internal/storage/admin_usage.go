package storage

import (
	"context"
	"errors"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/jackc/pgx/v5"
)

// GroupRequestLogsByKey aggregates the whole filtered set by API key: how many
// requests and how many tokens each key served within the filter.
//
// The grouping runs on the log alone over the same requestLogWhere as the table
// and the summary, so "by key" cannot describe a different set of rows than
// "by list". Key display fields are joined on afterwards, so the shared
// predicate stays byte-identical (a join that pulled k.tenant_id into scope
// would make the predicate's bare `tenant_id` ambiguous).
//
// Ordering is by tokens, descending, so the heaviest consumers surface first in
// the console; a stable secondary of the key name keeps equal rows in a
// readable order.
func (p *Postgres) GroupRequestLogsByKey(ctx context.Context, f RequestLogFilter) ([]model.APIKeyUsage, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT
	COALESCE(g.api_key_id::text,'') AS api_key_id,
	COALESCE(k.name,''),
	COALESCE(k.key_prefix,''),
	g.requests,
	g.tokens
FROM (
	SELECT api_key_id, COUNT(*)::bigint AS requests,
	       COALESCE(SUM(COALESCE(total_tokens,0)),0)::bigint AS tokens
	FROM gateway_request_log`+requestLogWhere+`
	GROUP BY api_key_id
) g
LEFT JOIN gateway_api_key k ON k.id = g.api_key_id
ORDER BY g.tokens DESC, k.name
`, requestLogArgs(f)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.APIKeyUsage
	for rows.Next() {
		var v model.APIKeyUsage
		if err := rows.Scan(&v.APIKeyID, &v.Name, &v.KeyPrefix, &v.Requests, &v.Tokens); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DistinctRequestTenants lists the tenants that have rows under the filter,
// stopping at two.
//
// The usage endpoint can only quote a single tenant's quota when the filter
// describes one tenant; this is how it tells "exactly one tenant" from "a mix"
// without scanning the whole log. Two is enough to detect the mix — the caller
// only needs to know whether the count is one or not.
func (p *Postgres) DistinctRequestTenants(ctx context.Context, f RequestLogFilter) ([]string, error) {
	rows, err := p.Pool.Query(ctx, `
SELECT DISTINCT COALESCE(tenant_id::text,'')
FROM gateway_request_log`+requestLogWhere+`
LIMIT 2
`, requestLogArgs(f)...)
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

// TenantByID returns one tenant's record (its quota limits in particular),
// or pgx.ErrNoRows when no such tenant exists.
func (p *Postgres) TenantByID(ctx context.Context, id string) (model.Tenant, error) {
	var v model.Tenant
	err := p.Pool.QueryRow(ctx, `
SELECT `+tenantColumns+`
FROM gateway_tenant
WHERE id=$1::uuid
`, id).Scan(&v.ID, &v.Code, &v.Name, &v.Status, &v.DailyTokenLimit, &v.MonthlyTokenLimit, &v.MonthlyRequestLimit, &v.MonthlyBudgetMicros, &v.RPMLimit, &v.TPMLimit, &v.MaxConcurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Tenant{}, pgx.ErrNoRows
	}
	return v, err
}
