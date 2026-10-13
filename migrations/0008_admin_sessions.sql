-- 0008_admin_sessions.sql
-- Admin Session Authentication v2: server-side administrator sessions.
--
-- A session is the unit of "one logged-in device". Only a hash of the refresh
-- secret is stored, so a database leak does not hand out usable refresh
-- tokens. Access tokens are short lived and never persisted anywhere.
--
-- version is bumped when this one session must be invalidated (device revoked
-- from the session list). user_auth_version mirrors gateway_admin_user
-- .auth_version at issue time, so bumping the account version invalidates
-- every session of that account without touching individual rows.
--
-- Replay-safe for the current dev migration runner.

CREATE TABLE IF NOT EXISTS gateway_admin_session (
  sid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES gateway_admin_user(id) ON DELETE CASCADE,
  version BIGINT NOT NULL DEFAULT 1,
  user_auth_version BIGINT NOT NULL DEFAULT 1,
  status SMALLINT NOT NULL DEFAULT 1,
  refresh_hash VARCHAR(128) NOT NULL,
  login_method VARCHAR(32) NOT NULL DEFAULT 'password',
  ip VARCHAR(64) NOT NULL DEFAULT '',
  user_agent VARCHAR(256) NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_active_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at TIMESTAMPTZ NOT NULL,
  revoked_at TIMESTAMPTZ,
  revoke_reason VARCHAR(64) NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_gateway_admin_session_user
  ON gateway_admin_session(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_gateway_admin_session_expires
  ON gateway_admin_session(expires_at);
