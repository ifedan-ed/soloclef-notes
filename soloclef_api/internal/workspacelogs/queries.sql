-- name: LogForMember :one
-- read_log, update_log and delete_log each resolve the row through
-- app/routes/logs_routes.py's _authorised_log: the caller's membership of the
-- workspace the row names is part of the predicate rather than a check run
-- after the row has been loaded.
--
-- That ordering is the whole point. Loading the row first made all three routes
-- an existence oracle over every workspace of the deployment — 403 for a log
-- that exists, 404 for one that does not — so an outsider could count another
-- workspace's logs by asking for ids. One answer now: a log in a workspace the
-- caller is not in is a log that was never written. The specific permission
-- (logs:read, logs:update, logs:delete) is still checked afterwards, and a
-- member who lacks it is still told 403.
--
-- (user_id, workspace_id) is unique on workspace_memberships
-- (uq_user_workspace_membership), so the join cannot multiply the row.
SELECT logs.created_at,
       logs.id,
       logs.level::text AS level,
       logs.status::text AS status,
       logs.message,
       logs.source,
       logs.log_metadata,
       logs.workspace_id
FROM logs
JOIN workspace_memberships AS membership
  ON membership.workspace_id = logs.workspace_id
WHERE logs.id = $1 AND membership.user_id = $2;

-- name: CreateLog :one
-- create_log's insert. created_at is passed in rather than defaulted in SQL
-- because the Python model's default is datetime.now(UTC) evaluated in the
-- application, not a server default. log_metadata is passed as the text
-- SQLAlchemy's JSON type would have written (internal/pythonjson.MarshalColumn).
INSERT INTO logs (level, status, message, source, log_metadata, workspace_id, created_at)
VALUES (@level::loglevel,
        @status::logstatus,
        @message,
        sqlc.narg(source)::varchar,
        sqlc.narg(log_metadata)::json,
        @workspace_id,
        @created_at)
RETURNING created_at,
          id,
          level::text AS level,
          status::text AS status,
          message,
          source,
          log_metadata,
          workspace_id;

-- name: UpdateLog :one
-- update_log's write. The Python route assigns only the fields the body
-- mentioned (model_dump(exclude_unset=True)); each CASE here is one of those
-- decisions, so an unmentioned field keeps its stored value and an explicit
-- null is written as NULL — a NOT NULL violation and a 500 on both tiers for
-- level, status and message, and an ordinary write for the other two.
UPDATE logs
SET level = CASE WHEN @set_level::boolean THEN sqlc.narg(level)::loglevel ELSE level END,
    status = CASE WHEN @set_status::boolean THEN sqlc.narg(status)::logstatus ELSE status END,
    message = CASE WHEN @set_message::boolean THEN sqlc.narg(message)::text ELSE message END,
    source = CASE WHEN @set_source::boolean THEN sqlc.narg(source)::varchar ELSE source END,
    log_metadata = CASE WHEN @set_log_metadata::boolean THEN sqlc.narg(log_metadata)::json ELSE log_metadata END
WHERE id = @id
RETURNING created_at,
          id,
          level::text AS level,
          status::text AS status,
          message,
          source,
          log_metadata,
          workspace_id;

-- name: DeleteLog :exec
-- delete_log's write.
DELETE FROM logs WHERE id = $1;
