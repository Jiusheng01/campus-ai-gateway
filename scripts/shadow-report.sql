-- Shadow-mode comparison.
--
-- While the gateway runs DATA_PLANE=shadow, ThinkWatch serves every request and
-- the direct plane records the chain it would have tried
-- (gateway_request_attempt.outcome='shadow'). This compares the two per request.
--
--   psql "$DATABASE_URL" -f scripts/shadow-report.sql
--
-- The window is the last 24 hours; widen it by editing the `win` CTE.
--
-- The comparison is the direct plane's LEADER (attempt_no = 1) against the
-- channel ThinkWatch reported. ThinkWatch's provider is the channel that WON,
-- so requests it failed over on are excluded: they would disagree with the
-- direct plane's first pick even when both planes chose the same first line.
--
-- The two planes name channels independently — the direct plane knows a
-- gateway_provider.name, ThinkWatch reports its own resolved channel name — so a
-- disagreement is evidence to read, not a verdict. Confirm the names refer to
-- the same upstream before treating one as wrong.

WITH win AS (SELECT now() - interval '24 hours' AS since)

SELECT
	(SELECT count(*) FROM gateway_request_attempt a, win
	 WHERE a.outcome='shadow' AND a.attempt_no=1 AND a.created_at >= win.since) AS sampled,
	(SELECT count(*) FROM gateway_request_attempt a
	 JOIN gateway_request_log l ON l.request_id = a.request_id, win
	 WHERE a.outcome='shadow' AND a.attempt_no=1 AND a.created_at >= win.since
	   AND COALESCE(l.attempts,1) <= 1
	   AND COALESCE(a.provider,'') <> COALESCE(l.provider,'')) AS mismatched;

SELECT a.request_id,
       COALESCE(a.provider,'')       AS direct_provider,
       COALESCE(a.upstream_model,'') AS direct_upstream_model,
       COALESCE(l.provider,'')       AS thinkwatch_provider,
       a.created_at
FROM gateway_request_attempt a
JOIN gateway_request_log l ON l.request_id = a.request_id, win
WHERE a.outcome='shadow' AND a.attempt_no=1 AND a.created_at >= win.since
  AND COALESCE(l.attempts,1) <= 1
  AND COALESCE(a.provider,'') <> COALESCE(l.provider,'')
ORDER BY a.created_at DESC
LIMIT 100;
