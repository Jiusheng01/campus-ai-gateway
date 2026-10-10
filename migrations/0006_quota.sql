-- 0006_quota.sql
-- M2.2: tenant quota configuration. Replay-safe.

ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS daily_token_limit BIGINT NOT NULL DEFAULT 0;

ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS monthly_token_limit BIGINT NOT NULL DEFAULT 0;

ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS monthly_request_limit BIGINT NOT NULL DEFAULT 0;

ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS monthly_budget_micros BIGINT NOT NULL DEFAULT 0;
