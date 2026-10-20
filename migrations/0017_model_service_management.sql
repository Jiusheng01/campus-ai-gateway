-- 0017_model_service_management.sql
-- MS1: management metadata for upstream channels and external models, plus the
-- indexes used by model/channel runtime summaries. Additive and replay-safe.
--
-- No historical migration is rewritten. Every column has a default or is
-- nullable, so existing rows and an older application that selects only its old
-- columns keep working. The migration history itself is one-way: see the MS1
-- operations notes for the rollback path.

ALTER TABLE gateway_provider
  ADD COLUMN IF NOT EXISTS provider_type VARCHAR(32) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS description VARCHAR(512) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS last_test_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS last_test_ok BOOLEAN,
  ADD COLUMN IF NOT EXISTS last_test_status_code INT,
  ADD COLUMN IF NOT EXISTS last_test_latency_ms BIGINT,
  ADD COLUMN IF NOT EXISTS last_test_upstream_model VARCHAR(128),
  ADD COLUMN IF NOT EXISTS last_test_error_kind VARCHAR(64);

ALTER TABLE gateway_model
  ADD COLUMN IF NOT EXISTS display_name VARCHAR(128) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS description VARCHAR(1024) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS config_version BIGINT NOT NULL DEFAULT 1;

-- A model-scoped runtime summary filters by requested_model and time. The
-- provider/time index already exists (idx_gateway_request_log_provider_created).
CREATE INDEX IF NOT EXISTS idx_gateway_request_log_model_created
  ON gateway_request_log(requested_model, created_at DESC);

-- A channel-scoped runtime summary reads attempt outcomes by provider.
CREATE INDEX IF NOT EXISTS idx_request_attempt_provider_created
  ON gateway_request_attempt(provider, created_at DESC);
