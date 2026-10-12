-- 0007_admin_users.sql
-- Admin Session Authentication v2: local control-plane accounts.
--
-- Replaces the shared ADMIN_TOKEN as the everyday way into the admin UI.
-- Only a password hash is ever stored; the plaintext exists for the duration
-- of a single request and is never written to the database, the logs or the
-- audit trail.
--
-- auth_version is bumped whenever every session of an account must be
-- invalidated at once (password change, account disabled). Access tokens carry
-- the version they were issued against, so a bump takes effect on the very
-- next request instead of at token expiry.
--
-- The script is idempotent and also upgrades a database that already has a
-- gateway_admin_user table from an earlier build. That earlier shape lacked
-- auth_version and matched usernames case-sensitively; both are corrected here
-- without touching any other table.

CREATE TABLE IF NOT EXISTS gateway_admin_user (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username VARCHAR(64) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  display_name VARCHAR(128) NOT NULL DEFAULT '',
  -- Same role vocabulary as ADMIN_AUTH_MODE=trusted-header, so a campus SSO
  -- rollout later does not have to re-map role names.
  roles JSONB NOT NULL DEFAULT '["auditor"]'::jsonb,
  status SMALLINT NOT NULL DEFAULT 1,
  auth_version BIGINT NOT NULL DEFAULT 1,
  last_login_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Upgrade path: add auth_version to a table created before the column existed.
-- Existing rows start at 1, which is the same value a fresh install produces.
ALTER TABLE gateway_admin_user
  ADD COLUMN IF NOT EXISTS auth_version BIGINT NOT NULL DEFAULT 1;

-- Usernames are matched case-insensitively. Without this index "Admin" and
-- "admin" could coexist while a login lookup by lower(username) returns an
-- arbitrary one of them.
--
-- This index is created BEFORE the legacy case-sensitive constraint is dropped
-- so that a database which already contains case-insensitive duplicates fails
-- here, loudly, with the old constraint still in place. The operator resolves
-- the duplicates and re-runs; nothing is destroyed by a failed attempt.
CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_admin_user_username_lower
  ON gateway_admin_user (lower(username));

-- The case-sensitive constraint is now redundant: the lower() index above is
-- strictly stronger. Dropped so every deployment converges on one shape.
ALTER TABLE gateway_admin_user
  DROP CONSTRAINT IF EXISTS gateway_admin_user_username_key;

CREATE INDEX IF NOT EXISTS idx_gateway_admin_user_status
  ON gateway_admin_user(status);
