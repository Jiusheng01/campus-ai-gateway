-- 0010_tenant_hierarchy.sql
-- M3.1: multi-level tenant hierarchy (school -> dept -> teacher -> student).
-- Replay-safe: every statement is idempotent, so the dev scripts can re-apply it.
--
-- Semantics introduced here:
--   * gateway_tenant gains parent_id / path / depth / kind, turning the flat
--     table into a tree. `path` is a materialized path of UUID segments
--     ("/<root>/<child>/") used for subtree lookup and ancestor resolution.
--   * The quota columns become NULLable. This is the important part:
--         NULL -> inherit from the nearest ancestor that sets a value
--            0 -> explicitly unlimited
--           >0 -> this tenant's own limit
--   * allowed_models JSONB (access, a separate axis from quota):
--         NULL -> inherit (intersect) with ancestors
--           [] -> explicitly deny every model
--       [...]  -> this tenant's own allowlist
--
-- Backward compatibility: every existing row keeps the value it already has.
-- A tenant that was configured with 0 (unlimited) stays unlimited, and every
-- existing tenant becomes a root with path = '/<id>/'. Existing behaviour is
-- therefore unchanged; hierarchy is purely additive.

-- ---------------------------------------------------------------------------
-- 1) hierarchy columns
-- ---------------------------------------------------------------------------

ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS parent_id UUID REFERENCES gateway_tenant(id) ON DELETE RESTRICT;

ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS path TEXT;

ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS depth INT NOT NULL DEFAULT 0;

-- school | dept | teacher | student | tenant (default, for legacy rows)
ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'tenant';

-- ---------------------------------------------------------------------------
-- 2) backfill: every pre-existing tenant becomes a root of its own tree
-- ---------------------------------------------------------------------------

UPDATE gateway_tenant
   SET path = '/' || id::text || '/', depth = 0
 WHERE path IS NULL;

-- ---------------------------------------------------------------------------
-- 3) path is mandatory from now on (the application always writes it)
-- ---------------------------------------------------------------------------

ALTER TABLE gateway_tenant ALTER COLUMN path SET NOT NULL;

-- ---------------------------------------------------------------------------
-- 4) indexes: parent lookup + subtree prefix scan
--    text_pattern_ops is required for LIKE 'prefix%' to use the index.
-- ---------------------------------------------------------------------------

CREATE INDEX IF NOT EXISTS idx_gateway_tenant_parent ON gateway_tenant(parent_id);
CREATE INDEX IF NOT EXISTS idx_gateway_tenant_path   ON gateway_tenant(path text_pattern_ops);

-- ---------------------------------------------------------------------------
-- 5) quota columns become NULLable so NULL can mean "inherit"
--    The DEFAULT is dropped as well: a newly inserted tenant must default to
--    NULL (inherit), not to 0 (unlimited).
-- ---------------------------------------------------------------------------

ALTER TABLE gateway_tenant ALTER COLUMN daily_token_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN daily_token_limit DROP DEFAULT;

ALTER TABLE gateway_tenant ALTER COLUMN monthly_token_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN monthly_token_limit DROP DEFAULT;

ALTER TABLE gateway_tenant ALTER COLUMN monthly_request_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN monthly_request_limit DROP DEFAULT;

ALTER TABLE gateway_tenant ALTER COLUMN monthly_budget_micros DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN monthly_budget_micros DROP DEFAULT;

-- ---------------------------------------------------------------------------
-- 6) access axis (§12.1) + rate axis (§12.4) + soft budget (§12.2)
-- ---------------------------------------------------------------------------

-- NULL = inherit from ancestors (intersection), [] = deny all, [...] = own list
ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS allowed_models JSONB;

-- Rate limits per tenant. 0 = unlimited (unchanged convention).
ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS rpm_limit INT NOT NULL DEFAULT 0;

ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS tpm_limit BIGINT NOT NULL DEFAULT 0;

-- Soft budget: alert when crossed, but do not block. NULL = no soft alert.
ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS soft_token_limit BIGINT;
