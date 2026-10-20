-- 0017_model_catalog.sql
-- Separate the public model catalog from its deployments.
--
-- Before this migration gateway_model carried both identities:
--   * the catalog entry the business calls (alias, status), and
--   * a legacy "first binding" (provider_id, upstream_model) that 0003 never
--     retired.
-- Every write path had to keep the two in sync, and editing a model silently
-- rewrote whichever route happened to sort first. This migration removes the
-- second identity: a model is a name, a route is a deployment.
--
-- Order matters: backfill any missing route from the legacy columns first, then
-- retire the columns. 0003 already did this for rows that existed then; the
-- repeat here is what makes this migration safe to apply to a database where a
-- model was inserted by an older binary after 0003 ran.
--
-- The columns are made nullable rather than dropped. 0002 declares an index on
-- provider_id and 0003 backfills from provider_id/upstream_model, and the
-- bootstrap path replays every file when schema_migrations is missing — so on a
-- database already at this schema, DROP COLUMN would make 0002's CREATE INDEX
-- fail before 0017 could run. Keeping them nullable, with every value cleared,
-- leaves the replay executable; the application no longer reads or writes
-- either column and the schema contract no longer expects them.

ALTER TABLE gateway_model
  ADD COLUMN IF NOT EXISTS display_name VARCHAR(128) NOT NULL DEFAULT '';

-- Existing rows keep their old alias as the human-readable name. The API does
-- the same when a create request omits display_name.
UPDATE gateway_model SET display_name = alias WHERE display_name = '';

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'gateway_model' AND column_name = 'provider_id'
  ) THEN
    INSERT INTO gateway_model_route (
      model_id, provider_id, upstream_model, priority, weight, timeout_ms, status
    )
    SELECT m.id, m.provider_id, m.upstream_model, 1, 100, 60000, m.status
    FROM gateway_model m
    WHERE m.provider_id IS NOT NULL
      AND m.upstream_model IS NOT NULL
      AND m.upstream_model <> ''
      AND NOT EXISTS (
        SELECT 1 FROM gateway_model_route r WHERE r.model_id = m.id
      )
    ON CONFLICT (model_id, provider_id, upstream_model) DO NOTHING;
  END IF;
END $$;

ALTER TABLE gateway_model ALTER COLUMN provider_id DROP NOT NULL;
ALTER TABLE gateway_model ALTER COLUMN upstream_model DROP NOT NULL;

-- Clear the retired values so a stale binding cannot be mistaken for current
-- configuration by anyone reading the table directly.
UPDATE gateway_model SET provider_id = NULL, upstream_model = NULL;

-- A quota row now names a catalog entry, not an arbitrary string. The check is
-- NOT VALID so an existing orphan (a quota for a model that was never created)
-- does not block the migration; every new write is checked by both the API and
-- this foreign key. ON UPDATE CASCADE is a backstop for an out-of-band alias
-- rename; the admin API treats the alias as immutable.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'gateway_model_quota_model_alias_fkey'
  ) THEN
    ALTER TABLE gateway_model_quota
      ADD CONSTRAINT gateway_model_quota_model_alias_fkey
      FOREIGN KEY (model_alias) REFERENCES gateway_model(alias)
      ON UPDATE CASCADE ON DELETE RESTRICT
      NOT VALID;
  END IF;
END $$;
