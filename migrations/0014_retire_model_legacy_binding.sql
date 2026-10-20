-- 0014_retire_model_legacy_binding.sql
-- ADR-0005 contract: Route is the sole model routing authority. Replay-safe.
--
-- gateway_model.provider_id / upstream_model were the single-binding model that
-- gateway_model_route replaced in 0003. That migration kept them on purpose so
-- the control plane of the day kept working, with a note that they "will be
-- retired later" — this is that step. Until now they were a second writable
-- routing source: every model write also reverse-synced the best route back into
-- these columns, so a Route edit and a model edit could disagree about where a
-- model points.
--
-- With the columns gone, a model is only its client-facing identity and its
-- lifecycle status; its channel and upstream model come from its routes. The
-- backfill in 0003 guarantees every existing model already owns at least one
-- route before the columns disappear, and DeleteModelRoute refuses to remove the
-- last one, so no model is left unroutable.
--
-- The index is dropped explicitly so the intent is visible in the history;
-- dropping the column would remove it anyway.

-- Before the columns go, make sure every existing model still owns a Route.
-- 0003 backfilled one per model and DeleteModelRoute refuses to remove the
-- last one, but a model that lost its only Route some other way must not be
-- left unroutable by this migration. This is 0003's backfill repeated, and it
-- is idempotent for the same reasons: the NOT EXISTS skips every model that
-- already has a Route, and ON CONFLICT covers an existing binding.
INSERT INTO gateway_model_route (model_id, provider_id, upstream_model, priority, weight, timeout_ms, status)
SELECT m.id, m.provider_id, m.upstream_model, 1, 100, 60000, m.status
FROM gateway_model m
WHERE m.provider_id IS NOT NULL
  AND m.upstream_model IS NOT NULL
  AND m.upstream_model <> ''
  AND NOT EXISTS (
    SELECT 1 FROM gateway_model_route r WHERE r.model_id = m.id
  )
ON CONFLICT (model_id, provider_id, upstream_model) DO NOTHING;

DROP INDEX IF EXISTS idx_gateway_model_provider;

ALTER TABLE gateway_model DROP COLUMN IF EXISTS provider_id;

ALTER TABLE gateway_model DROP COLUMN IF EXISTS upstream_model;
