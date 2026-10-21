-- 0018_ownership_and_snapshot.sql
-- Personal Console ownership model: the storage the member workspace needs.
--
-- Additive and replay-safe. Every new column is nullable or has a default, so
-- existing rows keep their current meaning:
--   * an Account may belong to no Tenant,
--   * an API Key may have no owning Account and keeps authenticating,
--   * an existing Tenant allows every model,
--   * an existing Request Record stays ownerless.
--
-- The Request Record's owner is a snapshot, not a reference: it must outlive
-- both the key and the Account, so it deliberately carries no foreign key.
--
-- No HMAC or credential-encryption key is touched, and no table is rewritten or
-- dropped.

ALTER TABLE gateway_admin_user
  ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES gateway_tenant(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_gateway_admin_user_tenant
  ON gateway_admin_user(tenant_id);

ALTER TABLE gateway_api_key
  ADD COLUMN IF NOT EXISTS owner_user_id UUID REFERENCES gateway_admin_user(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_gateway_api_key_owner
  ON gateway_api_key(owner_user_id);

ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS allowed_models JSONB NOT NULL DEFAULT '["*"]'::jsonb;

ALTER TABLE gateway_request_log
  ADD COLUMN IF NOT EXISTS owner_user_id UUID;

CREATE INDEX IF NOT EXISTS idx_gateway_request_log_owner_created
  ON gateway_request_log(owner_user_id, created_at DESC);
