-- Make "all models" an explicit choice.
--
-- The empty allowed_models list meant "no restriction" in the data plane while
-- the console described it as "no models". Neither reading survives: an empty
-- list now denies every model, and the only way to grant everything is the
-- "*" wildcard.
--
-- Existing rows are converted rather than reinterpreted. A key created with
-- nothing ticked has in fact been serving every model, so rewriting it to the
-- wildcard preserves exactly the access it already had and merely makes that
-- access visible in the console. Leaving the rows as [] would revoke live
-- traffic on the strength of a documentation fix, which is the one outcome a
-- security change must not produce unannounced.
--
-- A row holding JSON null is the same case, not a different one. The console
-- always sent an array, but a client that omitted the field left the Go slice
-- nil and the writer marshalled that as null; the old data plane asked
-- len(allowed_models) and read both shapes as "no restriction". Rewriting only
-- [] would strand the null rows on the new reading, where an empty list denies
-- every model, and those keys would stop serving without anyone deciding that.
--
-- The backfill is guarded because it is a data migration, not a schema one. If
-- the column is missing the database has drifted, and the schema contract check
-- is the component that reports drift properly: it names the table, the column
-- and the expected shape. Letting this UPDATE fail first would replace that
-- diagnostic with "column does not exist", which tells an operator less about
-- what went wrong.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'gateway_api_key'
          AND column_name = 'allowed_models'
    ) THEN
        UPDATE gateway_api_key
        SET allowed_models = '["*"]'::jsonb
        WHERE allowed_models = '[]'::jsonb
           OR allowed_models = 'null'::jsonb;
    END IF;
END
$$;
