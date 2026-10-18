-- 0010_analytics_indexes.sql
-- M2.0: read paths for the dashboard trend and the request-log filters.
--
-- Both new query shapes slice gateway_request_log along a dimension and then
-- order by time: the tenant dashboard aggregates one tenant's window, and the
-- request-log page can now be narrowed to a tenant or a single API key. The
-- only pre-existing index is on created_at alone, so either filter would have
-- forced the planner to walk the time index and discard almost every row.
--
-- Replay-safe: every statement is IF NOT EXISTS, matching the rest of the
-- migration set (dev.ps1 reapplies all migrations on each start).

CREATE INDEX IF NOT EXISTS idx_gateway_request_log_tenant_created
  ON gateway_request_log(tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_gateway_request_log_api_key_created
  ON gateway_request_log(api_key_id, created_at DESC);
