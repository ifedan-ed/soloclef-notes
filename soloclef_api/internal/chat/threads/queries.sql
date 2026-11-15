-- The statements of a thread's own routes. A thread is its owner's and
-- nobody else's, and every statement here says so itself: each names the
-- account beside the id, so a thread, a message or a link of another
-- account is not found, exactly as one that was never made is not found.
-- Messages and links are owned through their thread, so the statements that
-- read or write them join the thread under its owner.

-- name: Thread :one
SELECT id, title, archived, created_at, updated_at
FROM new_chat_threads
WHERE id = sqlc.arg(thread_id) AND user_id = sqlc.arg(user_id);

-- name: InsertThread :one
-- created_at and updated_at are the same instant, the database's.
INSERT INTO new_chat_threads (title, archived, user_id)
VALUES (sqlc.arg(title), sqlc.arg(archived), sqlc.arg(user_id))
RETURNING id, title, archived, created_at, updated_at;

-- name: ListedThreads :many
-- An account's threads, newest activity first. Threads the artifact API
-- made for itself are not conversations and are left out.
--
-- The limit is applied here: the route this replaces read every thread and
-- cut the list in memory. `id` breaks ties so that two requests give one
-- order. The index ix_new_chat_threads_owner_updated answers it.
SELECT id, title, archived, created_at, updated_at
FROM new_chat_threads
WHERE user_id = sqlc.arg(user_id)
  AND archived = sqlc.arg(archived)
  AND source <> 'artifact_api'
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.narg(max_rows)::bigint;

-- name: SearchThreads :many
-- pattern arrives with LIKE's own characters escaped (store.go), so a
-- search for "50%" finds titles containing "50%" and not every thread.
SELECT id, title, archived, created_at, updated_at
FROM new_chat_threads
WHERE user_id = sqlc.arg(user_id)
  AND title ILIKE '%' || sqlc.arg(pattern)::text || '%' ESCAPE '\'
  AND source <> 'artifact_api'
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(max_rows)::bigint;

-- name: UpdateThread :one
-- A field left out keeps its value. updated_at is not moved: it orders the
-- list by when a thread was last talked in, and renaming or archiving a
-- thread is not talking in it.
UPDATE new_chat_threads
SET title = COALESCE(sqlc.narg(title), title),
    archived = COALESCE(sqlc.narg(archived), archived)
WHERE id = sqlc.arg(thread_id) AND user_id = sqlc.arg(user_id)
RETURNING id, title, archived, created_at, updated_at;

-- name: DeleteThread :execrows
-- The thread's messages, usage rows, public links and session state go with
-- it (ON DELETE CASCADE); its artifacts and podcasts stay and lose the
-- pointer (ON DELETE SET NULL).
DELETE FROM new_chat_threads WHERE id = sqlc.arg(thread_id) AND user_id = sqlc.arg(user_id);

-- name: ThreadMessages :many
-- A thread's messages in the order the page shows them: (created_at, id).
-- A turn writes its question and its empty answer in one transaction, so
-- the two share created_at exactly; ordered by created_at alone, which is
-- what the route this replaces did, the answer could come back above its
-- question.
--
-- token_usage has at most one row per message (uq_token_usage_message_id),
-- so the join cannot multiply a message. The thread is joined under its
-- owner: the messages of another account's thread are no rows.
SELECT m.id, m.thread_id, m.role::text AS role, m.content, m.created_at,
       m.author_id, m.turn_id,
       u.display_name AS author_display_name, u.avatar_url AS author_avatar_url,
       t.prompt_tokens, t.completion_tokens, t.total_tokens, t.cost_micros, t.model_breakdown
FROM new_chat_messages m
JOIN new_chat_threads thread ON thread.id = m.thread_id AND thread.user_id = sqlc.arg(user_id)
LEFT JOIN "user" u ON u.id = m.author_id
LEFT JOIN token_usage t ON t.message_id = m.id
WHERE m.thread_id = sqlc.arg(thread_id)
ORDER BY m.created_at, m.id
LIMIT sqlc.narg(max_rows)::bigint OFFSET sqlc.arg(skip)::bigint;

-- name: InsertMessage :one
-- One message per (thread, turn, role): a second write of the same turn's
-- message inserts nothing and returns no row, and the caller reads the row
-- that is there (MessageOfTurn). The thread is selected under its owner, so
-- nothing can be written into another account's thread.
INSERT INTO new_chat_messages (role, content, thread_id, author_id, turn_id)
SELECT sqlc.arg(role)::newchatmessagerole, sqlc.arg(content), thread.id,
       thread.user_id, sqlc.narg(turn_id)
FROM new_chat_threads AS thread
WHERE thread.id = sqlc.arg(thread_id) AND thread.user_id = sqlc.arg(user_id)
ON CONFLICT (thread_id, turn_id, role) WHERE turn_id IS NOT NULL DO NOTHING
RETURNING id, thread_id, role::text AS role, content, created_at, author_id, turn_id;

-- name: MessageOfTurn :one
SELECT m.id, m.thread_id, m.role::text AS role, m.content, m.created_at, m.author_id, m.turn_id
FROM new_chat_messages m
JOIN new_chat_threads thread ON thread.id = m.thread_id AND thread.user_id = sqlc.arg(user_id)
WHERE m.thread_id = sqlc.arg(thread_id)
  AND m.turn_id = sqlc.arg(turn_id)
  AND m.role = sqlc.arg(role)::newchatmessagerole;

-- name: TouchThread :exec
UPDATE new_chat_threads SET updated_at = now()
WHERE id = sqlc.arg(thread_id) AND user_id = sqlc.arg(user_id);

-- name: TouchOwnedNotebook :execrows
-- The caller's notebook, marked as used; the row count is also how a
-- notebook's chat routes learn the notebook is the caller's.
UPDATE notebooks SET updated_at = sqlc.arg(used_at)
WHERE id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id);

-- name: OwnsNotebook :one
SELECT EXISTS (SELECT 1 FROM notebooks WHERE id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id));

-- name: NotebookThreads :many
-- A notebook's chats, newest activity first; archived chats and the threads
-- the studio made for itself are not listed.
SELECT id, title, archived, created_at, updated_at
FROM new_chat_threads
WHERE notebook_id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id)
  AND NOT archived AND source <> 'artifact_api'
ORDER BY updated_at DESC, id DESC;

-- name: NotebookThreadIDs :many
-- Every chat of a notebook a person can clear: all but the studio's own
-- threads, which the outputs they made point at.
SELECT id FROM new_chat_threads
WHERE notebook_id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id)
  AND source <> 'artifact_api'
ORDER BY id;

-- name: InsertNotebookThread :one
INSERT INTO new_chat_threads (title, user_id, notebook_id, created_at, updated_at)
VALUES (sqlc.arg(title), sqlc.arg(user_id), sqlc.arg(notebook_id), sqlc.arg(made_at), sqlc.arg(made_at))
RETURNING id, title, archived, created_at, updated_at;
