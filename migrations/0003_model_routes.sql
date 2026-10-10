-- 0003_model_routes.sql
-- M1.5-01: multi-provider routing data structure.
--
-- Evolution:
--   before: gateway_model (alias -> provider_id -> upstream_model)  -- single binding
--   after:  gateway_model (alias -> 1..N gateway_model_route)       -- multi binding
--
-- This migration is purely additive and idempotent:
--   * it does not touch / rewrite any data created by 0001 or 0002
--   * it backfills exactly one priority=1 route per existing model
--   * re-running it is safe (the dev scripts re-apply every migration on startup)
--
-- Scope of M1.5-01: schema + data backfill only.
--   No DeepSeek, no Route API, no admin Route UI, no ThinkWatch changes.
--   gateway_model.provider_id / upstream_model are intentionally kept for now so
--   the existing control-plane code keeps working; they will be retired later.

CREATE TABLE IF NOT EXISTS gateway_model_route (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  model_id       UUID NOT NULL REFERENCES gateway_model(id) ON DELETE CASCADE,
  provider_id    UUID NOT NULL REFERENCES gateway_provider(id),
  upstream_model VARCHAR(128) NOT NULL,
  priority       INT NOT NULL DEFAULT 1,
  weight         INT NOT NULL DEFAULT 100,
  timeout_ms     INT NOT NULL DEFAULT 60000,
  status         SMALLINT NOT NULL DEFAULT 1,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT gateway_model_route_priority_chk CHECK (priority >= 1),
  CONSTRAINT gateway_model_route_weight_chk   CHECK (weight >= 0),
  CONSTRAINT gateway_model_route_timeout_chk  CHECK (timeout_ms > 0)
);

-- One route per (model, provider, upstream_model) binding.
-- Also serves as the ON CONFLICT target that keeps the backfill idempotent.
CREATE UNIQUE INDEX IF NOT EXISTS uq_gateway_model_route_binding
  ON gateway_model_route(model_id, provider_id, upstream_model);

-- Route selection: pick enabled routes of a model ordered by priority.
CREATE INDEX IF NOT EXISTS idx_gateway_model_route_model
  ON gateway_model_route(model_id, priority, status);

CREATE INDEX IF NOT EXISTS idx_gateway_model_route_provider
  ON gateway_model_route(provider_id);

-- Backfill: turn each existing single binding into its primary (priority=1) route.
-- The current campus-smart -> qwen -> qwen3.7-flash-* binding therefore becomes the
-- priority=1 first route of campus-smart.
-- A model that already owns at least one route is skipped, so repeated runs never
-- add duplicate routes even after routes are managed through the Route API.
INSERT INTO gateway_model_route (
  model_id, provider_id, upstream_model, priority, weight, timeout_ms, status
)
SELECT
  m.id, m.provider_id, m.upstream_model, 1, 100, 60000, m.status
FROM gateway_model m
WHERE m.provider_id IS NOT NULL
  AND m.upstream_model IS NOT NULL
  AND m.upstream_model <> ''
  AND NOT EXISTS (
    SELECT 1 FROM gateway_model_route r WHERE r.model_id = m.id
  )
ON CONFLICT (model_id, provider_id, upstream_model) DO NOTHING;
