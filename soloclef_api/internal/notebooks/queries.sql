-- The statements of the notebooks slice. A notebook is its owner's, and so
-- is everything in it: every statement names `user_id`, and that predicate
-- is the whole of the access check. Every list has a total order, a list of
-- ids is `= ANY($n::bigint[])`, and each statement selects only the columns
-- its route reads (`notebooks.focus_suggestions` can be a megabyte).

-- name: NotebookOfOwner :one
-- One of the caller's notebooks. Another account's is not found -- the same
-- answer as one that was never made.
SELECT id, title, icon, created_at, updated_at, chat_goal,
       chat_instructions, response_length, title_source, auto_title,
       featured_at
FROM notebooks
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: NotebooksOfOwner :many
-- The caller's notebooks, most recently used first. The order is total:
-- `id` breaks a tie on `updated_at`.
SELECT id, title, icon, created_at, updated_at, chat_goal,
       chat_instructions, response_length, title_source, auto_title,
       featured_at
FROM notebooks
WHERE user_id = sqlc.arg(user_id)
ORDER BY updated_at DESC, id DESC;

-- name: SourceCounts :many
-- Per notebook, how many sources it has and how many of them are ticked. A
-- notebook with no source has no row here, which the route reads as (0, 0).
-- No ORDER BY: the rows are put in a map keyed by notebook, so their order
-- reaches nothing.
--
-- The ids come from the caller's own notebooks, and the owner is named all
-- the same, so the statement is safe whatever ids it is handed.
SELECT notebook_id,
       count(id) AS total,
       coalesce(sum(CAST(enabled AS integer)), 0)::bigint AS enabled
FROM notebook_sources
WHERE notebook_id = ANY(sqlc.arg(notebook_ids)::bigint[])
  AND user_id = sqlc.arg(user_id)
GROUP BY notebook_id;

-- name: PendingAmong :many
-- Which candidate notebooks have a source that is being indexed, or one that
-- became ready since the instant given for that notebook (naming.go works
-- the instant out, so no text out of jsonb is cast to a timestamp here, and
-- one unreadable value cannot fail a whole list).
--
-- `ready_since` is one instant per candidate, in the candidates' order.
SELECT DISTINCT s.notebook_id
FROM notebook_sources s
JOIN documents d ON d.id = s.document_id AND d.user_id = s.user_id
WHERE s.notebook_id = ANY(sqlc.arg(candidates)::bigint[])
  AND s.user_id = sqlc.arg(user_id)
  AND (((d.status ->> 'state') IN ('pending', 'processing')
        AND s.added_at >= sqlc.arg(indexing_since)::timestamptz)
       OR d.updated_at >= (sqlc.arg(ready_since)::timestamptz[])[
            array_position(sqlc.arg(candidates)::bigint[], s.notebook_id)])
ORDER BY s.notebook_id;

-- name: InsertNotebook :one
-- A new notebook, the caller's. Both timestamps are one reading of the
-- application's clock, passed so that the answer and the row agree; every
-- other column is the table's default. The row is answered by the INSERT
-- itself, so nothing is read back.
INSERT INTO notebooks (user_id, title, icon, title_source, icon_source,
                       updated_at, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(title), sqlc.narg(icon),
        sqlc.arg(title_source), sqlc.arg(icon_source),
        sqlc.arg(written_at), sqlc.arg(written_at))
RETURNING id, title, icon, created_at, updated_at, chat_goal,
          chat_instructions, response_length, title_source, auto_title,
          featured_at;

-- name: DeleteNotebook :execrows
-- Removes the caller's notebook. Its source links, studio outputs, earlier
-- chats and public link go by the database's own CASCADE; its notes and its
-- chat stay, unfiled (ON DELETE SET NULL). One statement: the predicate is
-- the lookup and the access check, and the count says whether there was a
-- notebook to remove.
DELETE FROM notebooks WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: FeatureNotebook :one
-- Pins the caller's notebook to the top of their list. A notebook already
-- pinned keeps the instant it was pinned at, so pinning twice does not
-- reorder the pinned row. `updated_at` is not touched: pinning is not using.
UPDATE notebooks
SET featured_at = COALESCE(featured_at, sqlc.arg(featured_at))
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING id, title, icon, created_at, updated_at, chat_goal,
          chat_instructions, response_length, title_source, auto_title,
          featured_at;

-- name: UnfeatureNotebook :one
-- Removes the pin, and is no complaint when there was none.
UPDATE notebooks
SET featured_at = NULL
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING id, title, icon, created_at, updated_at, chat_goal,
          chat_instructions, response_length, title_source, auto_title,
          featured_at;

-- name: TemplatesOfOwner :many
-- The caller's own edits of the seeded report templates. Ordered, so the
-- statement has one answer; the response's own order is the seeds'.
SELECT template_key, title, description, prompt
FROM notebook_report_templates
WHERE user_id = sqlc.arg(user_id)
ORDER BY id;

-- name: SaveTemplate :exec
-- Saves the caller's edit of one template: a first edit is inserted, and an
-- edit of an edit is written over it, in one statement, so two saves that
-- race cannot both insert (uq_notebook_report_templates_key).
--
-- A save that changes nothing writes nothing -- the WHERE on the update --
-- so `updated_at` moves only when the template actually changed.
INSERT INTO notebook_report_templates AS template
       (user_id, template_key, title, description, prompt, updated_at, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(template_key), sqlc.arg(title),
        sqlc.arg(description), sqlc.arg(prompt),
        sqlc.arg(written_at), sqlc.arg(written_at))
ON CONFLICT (user_id, template_key) DO UPDATE
SET title = EXCLUDED.title,
    description = EXCLUDED.description,
    prompt = EXCLUDED.prompt,
    updated_at = EXCLUDED.updated_at
WHERE (template.title, template.description, template.prompt)
      IS DISTINCT FROM (EXCLUDED.title, EXCLUDED.description, EXCLUDED.prompt);

-- name: ResetTemplate :exec
-- Discards the caller's edit, so the seed shows again. Resetting a template
-- nobody edited removes nothing and is not an error.
DELETE FROM notebook_report_templates
WHERE user_id = sqlc.arg(user_id) AND template_key = sqlc.arg(template_key);

-- name: UpdateNotebook :one
-- The caller's edit of a notebook's name, icon and chat settings. Each field
-- is written only when the request mentioned it (its `set_` flag), in one
-- guarded statement, so two edits of different fields that race both land.
-- Naming it, or choosing its icon, stops the automatic naming for good: the
-- source becomes "user" when the value changes. `updated_at` moves only when
-- something did, because the list is ordered by it.
UPDATE notebooks AS n SET
    title = CASE WHEN sqlc.arg(set_title)::boolean THEN sqlc.arg(title)::text ELSE n.title END,
    title_source = CASE WHEN sqlc.arg(set_title)::boolean AND sqlc.arg(title)::text <> n.title
                        THEN 'user' ELSE n.title_source END,
    icon = CASE WHEN sqlc.arg(set_icon)::boolean THEN sqlc.narg(icon)::text ELSE n.icon END,
    icon_source = CASE WHEN sqlc.arg(set_icon)::boolean AND sqlc.narg(icon)::text IS DISTINCT FROM n.icon
                       THEN 'user' ELSE n.icon_source END,
    chat_goal = CASE WHEN sqlc.arg(set_chat_goal)::boolean THEN sqlc.arg(chat_goal)::text ELSE n.chat_goal END,
    chat_instructions = CASE WHEN sqlc.arg(set_chat_instructions)::boolean
                             THEN sqlc.narg(chat_instructions)::text ELSE n.chat_instructions END,
    response_length = CASE WHEN sqlc.arg(set_response_length)::boolean
                           THEN sqlc.arg(response_length)::text ELSE n.response_length END,
    updated_at = CASE WHEN
        (sqlc.arg(set_title)::boolean AND sqlc.arg(title)::text <> n.title)
        OR (sqlc.arg(set_icon)::boolean AND sqlc.narg(icon)::text IS DISTINCT FROM n.icon)
        OR (sqlc.arg(set_chat_goal)::boolean AND sqlc.arg(chat_goal)::text <> n.chat_goal)
        OR (sqlc.arg(set_chat_instructions)::boolean AND sqlc.narg(chat_instructions)::text IS DISTINCT FROM n.chat_instructions)
        OR (sqlc.arg(set_response_length)::boolean AND sqlc.arg(response_length)::text <> n.response_length)
      THEN sqlc.arg(written_at) ELSE n.updated_at END
WHERE n.id = sqlc.arg(id) AND n.user_id = sqlc.arg(user_id)
RETURNING id, title, icon, created_at, updated_at, chat_goal,
          chat_instructions, response_length, title_source, auto_title,
          featured_at;
