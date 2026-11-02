-- 0019_admin_password_reset.sql
-- Administrator-initiated password reset.
--
-- A reset replaces the account's password with one the operator hands over.
-- Because that password deliberately leaves the console's pages — it is read
-- aloud, written on a slip of paper, or pasted into a chat — it must not stay
-- the account's password. The flag below records that obligation against the
-- account itself rather than in the operator's memory or the browser's, so it
-- survives a restart, applies to every session and every device, and cannot be
-- cleared by signing out and in again with the handed-over password.
--
-- Additive and replay-safe: the column is NOT NULL with a DEFAULT, so every
-- existing row keeps its meaning ("no reset outstanding") without a backfill.

ALTER TABLE gateway_admin_user
  ADD COLUMN IF NOT EXISTS password_reset_required BOOLEAN NOT NULL DEFAULT FALSE;
