-- 0016_provider_model_list.sql
-- ADR-0018: each Provider carries the list of Provider Models it offers, so
-- "what can this channel do" has a data answer. Replay-safe.
--
-- The list has three sources. 'preset' is seeded from the server-side supplier
-- catalog when a channel is created, 'fetched' is merged from the provider's
-- own GET /models response, and 'manual' is a name the operator added. The
-- source is kept per row so the console can say where a name came from; a
-- refresh never rewrites an existing row's source and never deletes a manual
-- one.
--
-- (provider_id, upstream_model) is unique because the list is a set: the same
-- upstream name listed twice would give the console two rows for one fact. The
-- foreign key cascades so removing a channel removes its list rather than
-- leaving unreachable rows behind.

CREATE TABLE IF NOT EXISTS gateway_provider_model (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id UUID NOT NULL,
  upstream_model VARCHAR(128) NOT NULL,
  source VARCHAR(16) NOT NULL,
  status SMALLINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT gateway_provider_model_provider_model_key UNIQUE (provider_id, upstream_model)
);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'gateway_provider_model'::regclass
      AND conname = 'gateway_provider_model_provider_id_fkey'
  ) THEN
    ALTER TABLE gateway_provider_model
      ADD CONSTRAINT gateway_provider_model_provider_id_fkey
      FOREIGN KEY (provider_id) REFERENCES gateway_provider(id) ON DELETE CASCADE;
  END IF;
END $$;
