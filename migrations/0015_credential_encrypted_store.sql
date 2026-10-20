-- 0015_credential_encrypted_store.sql
-- ADR-0017: a Provider references a first-class Credential instead of naming a
-- gateway environment variable. Replay-safe.
--
-- The secret a channel uses is now either an environment-variable reference
-- (source_type='env', secret_ref=<name>) or an inline value sealed with
-- AES-256-GCM (source_type='inline', ciphertext=<v1:...>). Keeping the two
-- sources in one table is what lets a channel switch between them without
-- changing its identity.
--
-- Expand-contract in one file: create the table, add the reference, backfill one
-- environment-variable Credential per existing channel, then drop the old
-- column. The backfill is guarded by a catalog check rather than run
-- unconditionally because the migration set is replayed in full during a
-- bootstrap against a database that may already be at the end state, where
-- credential_env no longer exists.
--
-- The backfill name is derived from the provider id so it is stable and unique
-- across reruns without depending on the channel name, which an operator can
-- edit.

CREATE TABLE IF NOT EXISTS gateway_credential (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(128) NOT NULL UNIQUE,
  source_type VARCHAR(16) NOT NULL,
  secret_ref VARCHAR(128),
  ciphertext TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE gateway_provider ADD COLUMN IF NOT EXISTS credential_id UUID;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'gateway_provider'
      AND column_name = 'credential_env'
  ) THEN
    -- One env Credential per provider that does not already have one. The
    -- NOT EXISTS makes a replay that finds a half-backfilled database idempotent.
    INSERT INTO gateway_credential (name, source_type, secret_ref)
    SELECT 'channel:' || p.id::text, 'env', p.credential_env
    FROM gateway_provider p
    WHERE p.credential_id IS NULL
      AND NOT EXISTS (
        SELECT 1 FROM gateway_credential c WHERE c.name = 'channel:' || p.id::text
      );

    UPDATE gateway_provider p
    SET credential_id = c.id,
        updated_at = NOW()
    FROM gateway_credential c
    WHERE p.credential_id IS NULL
      AND c.name = 'channel:' || p.id::text;

    EXECUTE 'ALTER TABLE gateway_provider DROP COLUMN credential_env';
  END IF;
END $$;

ALTER TABLE gateway_provider ALTER COLUMN credential_id SET NOT NULL;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'gateway_provider'::regclass
      AND conname = 'gateway_provider_credential_id_fkey'
  ) THEN
    ALTER TABLE gateway_provider
      ADD CONSTRAINT gateway_provider_credential_id_fkey
      FOREIGN KEY (credential_id) REFERENCES gateway_credential(id);
  END IF;
END $$;
