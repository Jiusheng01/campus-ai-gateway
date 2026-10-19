-- 0015_model_quota.sql
-- Per-model token quota: a second quota axis next to the tenant one.
--
-- A tenant currently holds exactly one token budget. A request consumes the
-- whole thing regardless of which model it asked for, so an expensive model and
-- a cheap one are interchangeable in the accounting. This table adds the
-- missing dimension: "how much of model M may this subject spend?".
--
-- The axis is orthogonal to the access allowlist (gateway_tenant.allowed_models
-- and gateway_api_key.allowed_models):
--
--   allowed_models  -> WHICH models may be called    (intersection down the tree)
--   model quota     -> HOW MUCH of a model may be used (inheritance down the tree)
--
-- They must not share a resolution rule. Access narrows; quota inherits. A
-- tenant that is allowed a model but has no quota row for it simply inherits
-- the nearest configured ancestor.
--
-- tenant_id is NULLable and that is load-bearing:
--   tenant_id IS NULL     -> the system-wide pool for that model. It is the ROOT
--                            of the model quota chain, so every tenant's usage of
--                            that model also lands on it. This is the "one pool
--                            per model, shared by the whole campus" requirement.
--   tenant_id IS NOT NULL -> that tenant's own holding of the model.
--
-- The limit columns follow the convention 0013_tenant_hierarchy.sql establishes
-- for the tenant quota columns, so operators only have to learn it once:
--   NULL -> inherit from the nearest ancestor (or from the system pool)
--      0 -> explicitly unlimited
--     >0 -> this subject's own cap
--
-- Deliberately one pool PER MODEL rather than one pool across models. The unit
-- here is tokens, and 1 token of a large model is not 1 token of a small one; a
-- single mixed pool would be a sum of incomparable quantities and could neither
-- describe cost nor guide allocation. A cost-denominated pool (one unit that IS
-- comparable across models) is a separate workstream.
--
-- Replay-safe: every statement is idempotent. Purely additive -- no existing
-- row or behaviour changes. With no rows present the data plane behaves exactly
-- as before, which is what makes this safe to deploy ahead of any configuration.

-- ---------------------------------------------------------------------------
-- 1) the table
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS gateway_model_quota (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),

  -- NULL = the system-wide pool for this model (see the header).
  tenant_id           UUID REFERENCES gateway_tenant(id) ON DELETE CASCADE,

  model_alias         VARCHAR(128) NOT NULL,

  -- NULL = inherit / 0 = unlimited / >0 = cap. See the header.
  daily_token_limit   BIGINT,
  monthly_token_limit BIGINT,

  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- 2) uniqueness
-- ---------------------------------------------------------------------------
--
-- A subject may hold at most one quota per model. Without the constraint a
-- second row would silently shadow the first depending on scan order, which is
-- exactly the kind of non-determinism a quota system cannot afford.
--
-- NULLS NOT DISTINCT (PostgreSQL 15+) is essential, not cosmetic: in a plain
-- unique index two NULLs are considered different, so the system pool row could
-- be inserted any number of times and no constraint would fire. It is also what
-- lets the admin layer's ON CONFLICT upsert target this index.

CREATE UNIQUE INDEX IF NOT EXISTS uq_model_quota_tenant_model
  ON gateway_model_quota (tenant_id, model_alias) NULLS NOT DISTINCT;

-- ---------------------------------------------------------------------------
-- 3) lookup indexes
-- ---------------------------------------------------------------------------
--
-- The data plane resolves one model at a time for a known tenant chain, so it
-- filters by model_alias first and then matches a handful of tenant ids.

CREATE INDEX IF NOT EXISTS idx_model_quota_model
  ON gateway_model_quota (model_alias);

-- ON DELETE CASCADE on tenant_id handles removal, but the index keeps the
-- cascade itself cheap on a large table.
CREATE INDEX IF NOT EXISTS idx_model_quota_tenant
  ON gateway_model_quota (tenant_id);
