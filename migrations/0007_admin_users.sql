-- Local control-plane accounts.
--
-- The gateway historically authenticated the admin UI with a single shared
-- ADMIN_TOKEN. That is fine for a developer laptop but unusable for a campus
-- deployment: it cannot be attributed to a person, cannot be revoked, and has
-- to be copied around by hand. This table backs the username/password login
-- flow instead.
--
-- Only the bcrypt hash of a password is ever stored. The plaintext exists only
-- for the duration of the login request and is never written to the database,
-- to logs, or to the audit trail.
CREATE TABLE IF NOT EXISTS gateway_admin_user (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username VARCHAR(64) NOT NULL UNIQUE,
  password_hash VARCHAR(255) NOT NULL,
  display_name VARCHAR(128) NOT NULL DEFAULT '',
  -- Same role vocabulary as ADMIN_AUTH_MODE=trusted-header, so a campus SSO
  -- rollout later does not require re-mapping role names.
  roles JSONB NOT NULL DEFAULT '["auditor"]'::jsonb,
  status SMALLINT NOT NULL DEFAULT 1,
  last_login_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_gateway_admin_user_status ON gateway_admin_user(status);
