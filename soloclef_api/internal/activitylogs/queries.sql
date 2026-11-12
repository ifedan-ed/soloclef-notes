-- The account's activity log. Every statement names the owner: a log of
-- another account is the same "not found" as a log that was never written, so
-- no route here says whether an id is in use.

-- name: LogOfOwner :one
-- One entry, by primary key and owner.
SELECT created_at,
       id,
       level::text AS level,
       status::text AS status,
       message,
       source,
       log_metadata
FROM logs
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: CreateLog :one
-- created_at is passed in so the route's clock can be set in a test; the
-- column's own default is the same instant otherwise.
INSERT INTO logs (user_id, level, status, message, source, log_metadata, created_at)
VALUES (sqlc.arg(user_id),
        sqlc.arg(level)::loglevel,
        sqlc.arg(status)::logstatus,
        sqlc.arg(message),
        sqlc.narg(source)::varchar,
        sqlc.narg(log_metadata)::jsonb,
        sqlc.arg(created_at))
RETURNING created_at,
          id,
          level::text AS level,
          status::text AS status,
          message,
          source,
          log_metadata;

-- name: UpdateLog :one
-- A partial update in one statement. Each CASE is "was this field mentioned":
-- an unmentioned field keeps its value and a mentioned null is written, which
-- clears source and log_metadata (the route refuses a null for the three NOT
-- NULL columns before it gets here). No row back means the log is not the
-- caller's or is not there.
UPDATE logs
SET level = CASE WHEN sqlc.arg(set_level)::boolean THEN sqlc.narg(level)::loglevel ELSE level END,
    status = CASE WHEN sqlc.arg(set_status)::boolean THEN sqlc.narg(status)::logstatus ELSE status END,
    message = CASE WHEN sqlc.arg(set_message)::boolean THEN sqlc.narg(message)::text ELSE message END,
    source = CASE WHEN sqlc.arg(set_source)::boolean THEN sqlc.narg(source)::varchar ELSE source END,
    log_metadata = CASE WHEN sqlc.arg(set_log_metadata)::boolean THEN sqlc.narg(log_metadata)::jsonb ELSE log_metadata END
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING created_at,
          id,
          level::text AS level,
          status::text AS status,
          message,
          source,
          log_metadata;

-- name: DeleteLog :execrows
-- The count is the answer: 0 is "not found".
DELETE FROM logs WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: LogsOfOwner :many
-- The task log list: the caller's entries, newest first. Each filter is
-- "not asked, or matches", so one statement serves every combination and
-- the planner still walks ix_logs_owner_created (user_id, created_at DESC).
-- The id breaks ties so that two entries written in the same instant do not
-- change places between one page and the next. The source pattern arrives
-- already escaped: what a person typed is matched as text, never as a
-- wildcard.
SELECT created_at,
       id,
       level::text AS level,
       status::text AS status,
       message,
       source,
       log_metadata
FROM logs
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.narg(level)::loglevel IS NULL OR level = sqlc.narg(level)::loglevel)
  AND (sqlc.narg(status)::logstatus IS NULL OR status = sqlc.narg(status)::logstatus)
  AND (sqlc.narg(source_pattern)::text IS NULL OR source ILIKE sqlc.narg(source_pattern)::text)
  AND (sqlc.narg(not_before)::timestamptz IS NULL OR created_at >= sqlc.narg(not_before)::timestamptz)
  AND (sqlc.narg(not_after)::timestamptz IS NULL OR created_at <= sqlc.narg(not_after)::timestamptz)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(skip);
