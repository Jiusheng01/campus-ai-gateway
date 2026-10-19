-- 0014_request_attempt_shadow_index.sql
-- The shadow report filters the attempt table by outcome and time, which the
-- request_id index does not serve. Replay-safe.

CREATE INDEX IF NOT EXISTS idx_request_attempt_outcome_created
  ON gateway_request_attempt(outcome, created_at DESC);
