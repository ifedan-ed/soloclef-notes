-- name: InsertDraft :one
-- A draft that passed the create route's own checks, waiting on its card.
INSERT INTO automation_drafts (user_id, request)
VALUES (sqlc.arg(user_id), sqlc.arg(request))
RETURNING id;

-- name: SupersedeDraft :execrows
-- The draft a newer one replaces, as its owner's and only while it still
-- waits: a created draft is an automation and stays so.
UPDATE automation_drafts
SET status = 'superseded', replaced_by = sqlc.arg(replaced_by), updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status = 'pending';

-- name: NotebookChoices :many
-- The owner's notebooks a draft may name, newest first (the order the
-- notebooks home shows them in), bounded so one account's list cannot
-- fill a tool result.
SELECT id, title FROM notebooks
WHERE user_id = sqlc.arg(user_id)
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(max_rows);

-- name: FolderChoices :many
-- The owner's folders an event trigger may wait on, by name, bounded for
-- the same reason.
SELECT id, name FROM folders
WHERE user_id = sqlc.arg(user_id)
ORDER BY name, id
LIMIT sqlc.arg(max_rows);
