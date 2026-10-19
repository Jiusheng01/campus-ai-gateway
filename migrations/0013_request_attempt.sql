-- 0013_request_attempt.sql
-- Per-attempt telemetry: one row per Routing Attempt under a Request Record.
-- Replay-safe.
--
-- A Request Record already carries the winning channel and an attempt count,
-- which cannot answer "what did the gateway try, in what order, and why did it
-- move on". Each attempt gets its own row so the failover chain is queryable
-- without parsing a log line. This is Request Record data (analysis, ADR-0004),
-- so it shares that durability: a small loss window is acceptable, and no
-- upstream response content is stored (ADR-0009).

CREATE TABLE IF NOT EXISTS gateway_request_attempt (
  id BIGSERIAL PRIMARY KEY,
  request_id VARCHAR(64) NOT NULL,
  attempt_no INT NOT NULL,
  provider VARCHAR(128),
  upstream_model VARCHAR(128),
  outcome VARCHAR(16) NOT NULL,
  status_code INT,
  latency_ms BIGINT NOT NULL DEFAULT 0,
  error_kind VARCHAR(32),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_request_attempt_request
  ON gateway_request_attempt(request_id);
