-- Shadow-mode comparison.
--
-- While the gateway runs DATA_PLANE=shadow, ThinkWatch serves every request and
-- the direct plane records the channel it would have chosen
-- (gateway_request_attempt.outcome='shadow'). This compares the two per request.
--
--   psql "$DATABASE_URL" -f scripts/shadow-report.sql
--
-- The window is the last 24 hours; widen it by editing the `since` CTE.
--
-- The two planes name channels independently — the direct plane knows a
-- gateway_provider.name, ThinkWatch reports its own resolved channel name — so a
-- disagreement is evidence to read, not a verdict. Confirm the names refer to
-- the same upstream before treating one as wrong.

WITH win AS (SELECT now() - interval '24 hours' AS since)

SELECT count(*) AS sampled,
       count(*) FILTER (WHERE COALESCE(a.provider,'') <> COALESCE(l.provider,'')) AS mismatched
FROM gateway_request_attempt a
LEFT JOIN gateway_request_log l ON l.request_id = a.request_id, win
WHERE a.outcome = 'shadow' AND a.created_at >= win.since;

SELECT a.request_id,
       a.provider      AS direct_provider,
       a.upstream_model,
       l.provider      AS thinkwatch_provider,
       a.created_at
FROM gateway_request_attempt a
JOIN gateway_request_log l ON l.request_id = a.request_id, win
WHERE a.outcome = 'shadow' AND a.created_at >= win.since
  AND COALESCE(a.provider,'') <> COALESCE(l.provider,'')
ORDER BY a.created_at DESC
LIMIT 100;
