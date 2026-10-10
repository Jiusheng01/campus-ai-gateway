-- 0004_route_audit.sql
-- M1.6: route observability fields. Replay-safe because dev.ps1 reapplies all migrations.

ALTER TABLE gateway_request_log
  ADD COLUMN IF NOT EXISTS upstream_model VARCHAR(128);

ALTER TABLE gateway_request_log
  ADD COLUMN IF NOT EXISTS route_priority INT;

ALTER TABLE gateway_request_log
  ADD COLUMN IF NOT EXISTS attempts INT;

ALTER TABLE gateway_request_log
  ADD COLUMN IF NOT EXISTS failover BOOLEAN;

CREATE INDEX IF NOT EXISTS idx_gateway_request_log_provider_created
  ON gateway_request_log(provider, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_gateway_request_log_failover_created
  ON gateway_request_log(failover, created_at DESC);
