-- 0005_security_events.sql
-- M2.0: separate administrator audit and security event streams.
-- Replay-safe for the current dev migration runner.

CREATE TABLE IF NOT EXISTS gateway_admin_audit_log (
  id BIGSERIAL PRIMARY KEY,
  request_id VARCHAR(64),
  actor VARCHAR(128) NOT NULL DEFAULT '',
  action VARCHAR(64) NOT NULL,
  resource VARCHAR(256) NOT NULL DEFAULT '',
  status_code INT NOT NULL DEFAULT 0,
  client_ip VARCHAR(64),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_gateway_admin_audit_created
  ON gateway_admin_audit_log(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_gateway_admin_audit_actor_created
  ON gateway_admin_audit_log(actor, created_at DESC);

CREATE TABLE IF NOT EXISTS gateway_security_event (
  id BIGSERIAL PRIMARY KEY,
  request_id VARCHAR(64),
  tenant_id UUID,
  api_key_id UUID,
  event_type VARCHAR(64) NOT NULL,
  client_ip VARCHAR(64),
  requested_model VARCHAR(128),
  detail VARCHAR(512),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_gateway_security_event_created
  ON gateway_security_event(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_gateway_security_event_type_created
  ON gateway_security_event(event_type, created_at DESC);
