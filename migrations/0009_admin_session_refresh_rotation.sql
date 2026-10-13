-- 0009_admin_session_refresh_rotation.sql
-- Replay-safe refresh rotation metadata.
--
-- Two browser tabs that refresh at the same moment present the same refresh
-- token. Only one of them can win the compare-and-set that rotates the hash,
-- but the loser is not an attacker: it is the same user, in the same browser,
-- milliseconds earlier. Without extra state the server cannot tell that apart
-- from a leaked token being replayed.
--
-- previous_refresh_hash parks the hash that was current immediately before the
-- last rotation. previous_refresh_valid_until bounds how long that superseded
-- hash is still accepted as a benign concurrent refresh. Reuse after the window
-- is treated as replay and revokes the session family.
--
-- Both columns are additive and idempotent, so re-running the migration is a
-- no-op and an existing database keeps working unchanged.

ALTER TABLE gateway_admin_session
  ADD COLUMN IF NOT EXISTS previous_refresh_hash VARCHAR(128) NOT NULL DEFAULT '';

ALTER TABLE gateway_admin_session
  ADD COLUMN IF NOT EXISTS previous_refresh_valid_until TIMESTAMPTZ;

-- The replay check looks a session up by sid, which is already the primary key,
-- so no additional index is required.
