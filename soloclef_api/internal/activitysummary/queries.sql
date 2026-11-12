-- name: LogsInWindow :many
-- Every entry of one account's activity log since the cutoff, newest first.
-- The owner and the time are the two columns of ix_logs_owner_created, so
-- this is one index range and no sort.
--
-- The enum columns are cast to text because this tier has no reason to know
-- Postgres' enum oids.
SELECT id,
       status::text AS status,
       level::text AS level,
       message,
       source,
       created_at,
       log_metadata
FROM logs
WHERE user_id = sqlc.arg(user_id) AND created_at >= sqlc.arg(since)
ORDER BY created_at DESC;
