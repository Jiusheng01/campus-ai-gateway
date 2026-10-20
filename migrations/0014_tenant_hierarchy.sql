-- 0014_tenant_hierarchy.sql
-- M3.1: multi-level tenant hierarchy (school -> dept -> teacher -> student).
--
-- The Glossary defines the Tenant as the single access-control, rate-limit,
-- quota and audit boundary. It is currently flat: one row per tenant, every
-- limit set in place. This migration turns that table into a tree so a school
-- can allocate to a department, a department to its teachers, and a teacher to
-- their students, without each level re-stating the whole budget.
--
-- What this adds:
--   * parent_id / path / depth / kind, making gateway_tenant a tree. `path` is a
--     materialized path of UUID segments ("/<root>/<child>/"), used for O(1)
--     subtree matching (prefix compare) and for resolving the ancestor chain
--     without a recursive query on the request path.
--   * allowed_models, the access axis, which is a different axis from quota:
--         NULL -> inherit (intersect) with ancestors
--           [] -> explicitly deny every model
--       [...]  -> this tenant's own allowlist
--   * The six enforced limit columns become NULLable. This is the important
--     part, and it changes a convention that 0006_quota.sql and
--     0012_rate_limits_on_tenant.sql both established:
--
--         NULL -> inherit from the nearest ancestor that sets a value
--            0 -> explicitly unlimited
--           >0 -> this tenant's own limit
--
--     A two-state column cannot express "same as my parent". Under a hierarchy
--     the only available values would be the parent's number (which silently
--     freezes the child at today's value and stops tracking later changes) or
--     0 (which reads as "unlimited" and lets a child exceed its parent -- a
--     security hole, not a configuration mistake). Hence three states.
--
--     monthly_budget_micros is deliberately NOT converted: it is stored but not
--     enforced (no price data exists), so a third state on it would be a
--     distinction nothing reads.
--
-- Backward compatibility: every existing row keeps the value it already has.
-- A tenant configured with 0 (unlimited) stays unlimited, because 0 is still
-- an explicit value. Every existing tenant becomes a root with path
-- '/<id>/' and depth 0. Existing behaviour is therefore unchanged; hierarchy is
-- purely additive.
--
-- Replay-safe: every statement is idempotent.

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
-- 5) the access axis
-- ---------------------------------------------------------------------------

-- NULL = inherit (intersect), [] = deny all, [...] = own list
ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS allowed_models JSONB;

-- ---------------------------------------------------------------------------
-- 6) the six enforced limit columns become three-state
--
--    The DEFAULT is dropped as well as NOT NULL: a newly inserted tenant must
--    default to NULL (inherit), not to 0 (unlimited). Keeping the default would
--    make every tenant created through a path that omits the field silently
--    unlimited, which is the opposite of what a hierarchy is for.
--
--    This is a no-op for existing rows -- dropping a default never rewrites
--    stored values.
-- ---------------------------------------------------------------------------

ALTER TABLE gateway_tenant ALTER COLUMN daily_token_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN daily_token_limit DROP DEFAULT;

ALTER TABLE gateway_tenant ALTER COLUMN monthly_token_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN monthly_token_limit DROP DEFAULT;

ALTER TABLE gateway_tenant ALTER COLUMN monthly_request_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN monthly_request_limit DROP DEFAULT;

-- The three columns 0012_rate_limits_on_tenant.sql moved up from the API Key.
ALTER TABLE gateway_tenant ALTER COLUMN rpm_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN rpm_limit DROP DEFAULT;

ALTER TABLE gateway_tenant ALTER COLUMN tpm_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN tpm_limit DROP DEFAULT;

ALTER TABLE gateway_tenant ALTER COLUMN max_concurrency DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN max_concurrency DROP DEFAULT;

-- ---------------------------------------------------------------------------
-- 7) a parent must not be able to form a cycle with its own descendant
--
--    The application checks this on write, but a CHECK cannot express "no cycle
--    anywhere in the table"; a trigger would. The write path is the only writer
--    (there is no other insert into this table), so the check lives there and
--    the database enforces the cheaper invariant that a tenant is not its own
--    parent.
-- ---------------------------------------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'chk_gateway_tenant_parent_not_self'
  ) THEN
    ALTER TABLE gateway_tenant
      ADD CONSTRAINT chk_gateway_tenant_parent_not_self
      CHECK (parent_id IS NULL OR parent_id <> id);
  END IF;
END
$$;
