-- 0019_tenant_hierarchy_and_model_quota.sql
-- Tenant tree, inherited quotas, scoped tenant administrators, per-model pools.
--
-- Ported from the 0014/0015/0016 migration set of the earlier branch and
-- re-based onto 0018. Three capabilities arrive together because they share one
-- resolution model:
--
--   * gateway_tenant becomes a tree (school -> dept -> teacher -> student).
--     `path` is a materialized path of UUID segments ("/<root>/<child>/"), which
--     turns "is this tenant inside that subtree?" into an indexable prefix
--     compare and gives the request path its ancestor chain without a recursive
--     query.
--
--   * The six enforced limit columns become three-state:
--
--         NULL -> inherit from the nearest ancestor that sets a value
--            0 -> explicitly unlimited at this level
--           >0 -> this tenant's own cap
--
--     A flat two-state column cannot express "same as my parent": the child
--     would either freeze at today's number or write 0, which reads as
--     unlimited and lets it exceed its parent. monthly_budget_micros is
--     deliberately NOT converted -- it is stored but not enforced, so a third
--     state on it would be a distinction nothing reads.
--
--   * gateway_model_quota adds a second axis: "how much of model M may this
--     subject spend?". A NULL tenant_id is the campus-wide pool for that model
--     and is the root of the model quota chain. One pool per model, because one
--     token of a large model is not one token of a small one.
--
--   * gateway_admin_user.scope_tenant_id confines a tenant_admin to one subtree.
--     NULL means global reach and is what every existing account keeps.
--
-- tenant.allowed_models is untouched. Since 0018 it bounds what a member may
-- request when creating a personal API Key; it is not a hierarchical data-plane
-- access rule, and this migration does not give it one.
--
-- Backward compatibility: every existing tenant becomes a root with
-- path '/<id>/' and depth 0, and keeps the numeric limit it already has. 0 was
-- already "unlimited", which is still an explicit value, so no existing
-- behaviour changes.
--
-- Replay-safe: every statement is idempotent. Purely additive apart from
-- dropping NOT NULL/DEFAULT on the six quota columns.

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
-- 5) the six enforced limit columns become three-state
--
--    Dropping the DEFAULT is as load-bearing as dropping NOT NULL: a tenant
--    created through a path that omits the field must default to NULL
--    (inherit), not to 0 (unlimited). Dropping a default never rewrites stored
--    values.
-- ---------------------------------------------------------------------------

ALTER TABLE gateway_tenant ALTER COLUMN daily_token_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN daily_token_limit DROP DEFAULT;

ALTER TABLE gateway_tenant ALTER COLUMN monthly_token_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN monthly_token_limit DROP DEFAULT;

ALTER TABLE gateway_tenant ALTER COLUMN monthly_request_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN monthly_request_limit DROP DEFAULT;

ALTER TABLE gateway_tenant ALTER COLUMN rpm_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN rpm_limit DROP DEFAULT;

ALTER TABLE gateway_tenant ALTER COLUMN tpm_limit DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN tpm_limit DROP DEFAULT;

ALTER TABLE gateway_tenant ALTER COLUMN max_concurrency DROP NOT NULL;
ALTER TABLE gateway_tenant ALTER COLUMN max_concurrency DROP DEFAULT;

-- ---------------------------------------------------------------------------
-- 6) a parent must not be able to form a cycle with its own descendant
--
--    The application rejects a move into one's own subtree; a CHECK cannot
--    express "no cycle anywhere in the table". The database still enforces the
--    cheap invariant that a tenant is not its own parent.
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

-- ---------------------------------------------------------------------------
-- 7) scoped administrator
--
--    NULL keeps global reach. ON DELETE RESTRICT, not SET NULL: a NULL scope
--    means "everything", so nulling it when a tenant disappears would silently
--    promote the account. Refusing the delete is loud and reversible.
-- ---------------------------------------------------------------------------

ALTER TABLE gateway_admin_user
  ADD COLUMN IF NOT EXISTS scope_tenant_id UUID REFERENCES gateway_tenant(id) ON DELETE RESTRICT;

CREATE INDEX IF NOT EXISTS idx_gateway_admin_user_scope
  ON gateway_admin_user(scope_tenant_id);

-- ---------------------------------------------------------------------------
-- 8) per-model quota
--
--    tenant_id NULL = the system-wide pool for this model. The limit columns
--    follow the same three states as the tenant quota columns.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS gateway_model_quota (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           UUID REFERENCES gateway_tenant(id) ON DELETE CASCADE,
  model_alias         VARCHAR(128) NOT NULL,
  daily_token_limit   BIGINT,
  monthly_token_limit BIGINT,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- NULLS NOT DISTINCT (PostgreSQL 15+) is essential: in a plain unique index two
-- NULLs are different, so the campus pool row could be inserted repeatedly.
CREATE UNIQUE INDEX IF NOT EXISTS uq_model_quota_tenant_model
  ON gateway_model_quota (tenant_id, model_alias) NULLS NOT DISTINCT;

CREATE INDEX IF NOT EXISTS idx_model_quota_model
  ON gateway_model_quota (model_alias);

CREATE INDEX IF NOT EXISTS idx_model_quota_tenant
  ON gateway_model_quota (tenant_id);
