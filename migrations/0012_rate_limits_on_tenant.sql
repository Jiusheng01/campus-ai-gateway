-- 0012_rate_limits_on_tenant.sql
-- Rate limits move from the API Key to the Tenant. Replay-safe.
--
-- The Glossary defines the Tenant as the single access-control, rate-limit,
-- quota and audit boundary, and an API Key as a credential that carries only
-- identity, model/IP authorization and audit attribution. Minute-level limits
-- on the key contradicted that: three keys at 60 RPM gave one tenant 180 RPM.
-- The three columns therefore move up one level.
--
-- Zero means unlimited, on both the old and the new column, so a tenant that
-- never configures a limit is not silently throttled.

ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS rpm_limit BIGINT NOT NULL DEFAULT 0;

ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS tpm_limit BIGINT NOT NULL DEFAULT 0;

ALTER TABLE gateway_tenant
  ADD COLUMN IF NOT EXISTS max_concurrency BIGINT NOT NULL DEFAULT 0;

ALTER TABLE gateway_api_key
  DROP COLUMN IF EXISTS rpm_limit;

ALTER TABLE gateway_api_key
  DROP COLUMN IF EXISTS tpm_limit;

ALTER TABLE gateway_api_key
  DROP COLUMN IF EXISTS max_concurrency;
