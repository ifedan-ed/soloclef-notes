-- Every statement names the automation with its owner. A trigger and a run
-- have no owner column of their own: they are reached through their
-- automation, in the same statement, so another account's automation id
-- reads and writes nothing.

-- name: ListAutomations :many
-- Newest first, with the id as the tie-break so a page never repeats or
-- skips a row made in the same instant as its neighbour.
SELECT id, name, description, status::text AS status, version, created_at, updated_at
FROM automations
WHERE user_id = sqlc.arg(user_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountAutomations :one
SELECT count(*) FROM automations WHERE user_id = sqlc.arg(user_id);

-- name: CreateAutomation :one
INSERT INTO automations (user_id, name, description, definition)
VALUES (sqlc.arg(user_id), sqlc.arg(name), sqlc.narg(description), sqlc.arg(definition))
RETURNING id;

-- name: GetAutomation :one
SELECT id, name, description, status::text AS status, version, definition, created_at, updated_at
FROM automations
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: UpdateAutomation :one
-- A partial update in one statement: each column changes only when its
-- "set" flag is true, and the version moves only with the definition. The
-- old status is returned beside the row so the caller knows whether the
-- automation has just stopped being active.
UPDATE automations AS a
SET name = CASE WHEN sqlc.arg(set_name)::boolean THEN sqlc.arg(name)::varchar ELSE a.name END,
    description = CASE WHEN sqlc.arg(set_description)::boolean THEN sqlc.narg(description)::text ELSE a.description END,
    status = CASE WHEN sqlc.arg(set_status)::boolean THEN sqlc.arg(status)::automation_status ELSE a.status END,
    definition = CASE WHEN sqlc.arg(set_definition)::boolean THEN sqlc.arg(definition)::jsonb ELSE a.definition END,
    version = a.version + CASE WHEN sqlc.arg(set_definition)::boolean THEN 1 ELSE 0 END,
    updated_at = now()
WHERE a.id = sqlc.arg(id) AND a.user_id = sqlc.arg(user_id)
RETURNING a.id;

-- name: OwnedNotebooks :many
-- Which of the ids are the owner's notebooks: what a definition may name.
SELECT id FROM notebooks
WHERE user_id = sqlc.arg(user_id) AND id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY id;

-- name: OwnedConnections :many
-- Which of the ids are the owner's connected accounts (a mail connection,
-- Google Drive, a connected app): what a task may name with @.
SELECT id FROM search_source_connectors
WHERE user_id = sqlc.arg(user_id) AND id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY id;

-- name: DeleteRunThreads :execrows
-- The hidden threads an automation's runs were given to make artifacts
-- under, removed with the automation. Only threads of that kind, of the
-- owner, reached through the owner's automation: a run's thread_id is not
-- trusted to name them by itself. What a run made stays; an artifact's
-- thread simply becomes none.
DELETE FROM new_chat_threads AS thread
USING automation_runs AS run, automations AS automation
WHERE run.thread_id = thread.id
  AND run.automation_id = automation.id
  AND automation.id = sqlc.arg(id) AND automation.user_id = sqlc.arg(user_id)
  AND thread.user_id = sqlc.arg(user_id)
  AND thread.source = 'artifact_api';

-- name: DeleteAutomation :execrows
-- Triggers and runs go with it by their foreign keys. A run in progress
-- loses its row, which its worker sees at its next look and stops.
DELETE FROM automations WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: CancelUnfinishedRuns :execrows
-- Pausing or archiving stops what is in flight: a run that is waiting or
-- running becomes cancelled, and its worker stops within moments.
UPDATE automation_runs AS r
SET status = 'cancelled', finished_at = now(), error = sqlc.arg(error)
FROM automations AS a
WHERE r.automation_id = a.id AND a.id = sqlc.arg(automation_id) AND a.user_id = sqlc.arg(user_id)
  AND r.status IN ('pending', 'running');

-- name: ListTriggers :many
SELECT t.id, t.type::text AS type, t.params, t.static_inputs, t.enabled, t.last_fired_at, t.next_fire_at, t.created_at
FROM automation_triggers AS t
JOIN automations AS a ON a.id = t.automation_id
WHERE a.id = sqlc.arg(automation_id) AND a.user_id = sqlc.arg(user_id)
ORDER BY t.id;

-- name: GetTrigger :one
SELECT t.id, t.type::text AS type, t.params, t.static_inputs, t.enabled, t.last_fired_at, t.next_fire_at, t.created_at
FROM automation_triggers AS t
JOIN automations AS a ON a.id = t.automation_id
WHERE t.id = sqlc.arg(id) AND a.id = sqlc.arg(automation_id) AND a.user_id = sqlc.arg(user_id);

-- name: AddTrigger :one
-- Inserted through the automation's own row, so a trigger cannot be added
-- to an automation the caller does not own: no row to select from, no row
-- inserted.
INSERT INTO automation_triggers (automation_id, type, params, static_inputs, enabled, next_fire_at)
SELECT a.id, sqlc.arg(type)::automation_trigger_type, sqlc.arg(params), sqlc.arg(static_inputs),
       sqlc.arg(enabled), sqlc.narg(next_fire_at)
FROM automations AS a
WHERE a.id = sqlc.arg(automation_id) AND a.user_id = sqlc.arg(user_id)
RETURNING id;

-- name: UpdateTrigger :execrows
UPDATE automation_triggers AS t
SET params = sqlc.arg(params), static_inputs = sqlc.arg(static_inputs), enabled = sqlc.arg(enabled),
    next_fire_at = sqlc.narg(next_fire_at)
FROM automations AS a
WHERE t.id = sqlc.arg(id) AND t.automation_id = a.id
  AND a.id = sqlc.arg(automation_id) AND a.user_id = sqlc.arg(user_id);

-- name: DeleteTrigger :execrows
DELETE FROM automation_triggers AS t
USING automations AS a
WHERE t.id = sqlc.arg(id) AND t.automation_id = a.id
  AND a.id = sqlc.arg(automation_id) AND a.user_id = sqlc.arg(user_id);

-- name: ListRuns :many
SELECT r.id, r.automation_id, r.trigger_id, r.status::text AS status, r.started_at, r.finished_at, r.created_at
FROM automation_runs AS r
JOIN automations AS a ON a.id = r.automation_id
WHERE a.id = sqlc.arg(automation_id) AND a.user_id = sqlc.arg(user_id)
ORDER BY r.created_at DESC, r.id DESC
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountRuns :one
SELECT count(*)
FROM automation_runs AS r
JOIN automations AS a ON a.id = r.automation_id
WHERE a.id = sqlc.arg(automation_id) AND a.user_id = sqlc.arg(user_id);

-- name: GetRun :one
SELECT r.id, r.automation_id, r.trigger_id, r.status::text AS status, r.started_at, r.finished_at, r.created_at,
       r.definition_snapshot, r.inputs, r.step_results, r.output, r.artifacts, r.error
FROM automation_runs AS r
JOIN automations AS a ON a.id = r.automation_id
WHERE r.id = sqlc.arg(id) AND a.id = sqlc.arg(automation_id) AND a.user_id = sqlc.arg(user_id);

-- name: CancelRun :execrows
-- Only a run that has not finished can be cancelled; the status is part of
-- the statement, so a run that finishes as the button is pressed keeps the
-- ending it reached.
UPDATE automation_runs AS r
SET status = 'cancelled', finished_at = now(), error = sqlc.arg(error)
FROM automations AS a
WHERE r.id = sqlc.arg(id) AND r.automation_id = a.id
  AND a.id = sqlc.arg(automation_id) AND a.user_id = sqlc.arg(user_id)
  AND r.status IN ('pending', 'running');
