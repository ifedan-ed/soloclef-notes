-- name: LogsInWindow :many
-- The Python route's own query: every log of one workspace since the cutoff,
-- newest first and with no limit.
--
-- The enum columns are cast to text because this tier has no reason to know
-- Postgres' enum oids. log_metadata is read as the json column's own bytes, so
-- its key order survives into the response, which is what the Python route's
-- round trip through json.loads and json.dumps preserves.
SELECT id,
       status::text AS status,
       level::text AS level,
       message,
       source,
       created_at,
       log_metadata
FROM logs
WHERE workspace_id = $1 AND created_at >= $2
ORDER BY created_at DESC;
