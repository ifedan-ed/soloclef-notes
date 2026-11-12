-- The rows of a chat turn. The web app, the phone app and Zero read
-- new_chat_threads, new_chat_messages and chat_session_state as they are, so
-- the shapes written here are a contract with them, not a choice.
--
-- A thread is its owner's and nobody else's. ThreadForTurn is the gate: it
-- answers a thread only to the account it belongs to, and every route asks
-- it before anything else. The statements that follow it in a turn name a
-- thread the gate has just answered; the ones that read or write anything
-- beyond the thread's own messages (the turn's context, a notebook's
-- sources, the mentions, the trust rules, the model) name the account
-- again themselves.

-- name: ThreadForTurn :one
-- The thread a turn is asked on, if it is the caller's. Another account's
-- thread is no row, exactly as a thread that was never made is no row.
SELECT id
FROM new_chat_threads
WHERE id = sqlc.arg(thread_id) AND user_id = sqlc.arg(user_id);

-- name: TurnContext :one
-- Everything the system prompt takes from the database for one turn, in one
-- round trip: the account's instructions, whether its answers cite, the
-- asker's name and memory, and the notebook the thread belongs to. The
-- thread is named with its account so that a thread id from elsewhere reads
-- nothing.
--
-- `notebook_id` is the thread's own column and `notebook_title` comes from
-- the join, on purpose: a thread that names a notebook this account does
-- not have (the title is NULL) is a notebook chat with nothing to read, not
-- a plain chat that may search everything. The schema's composite key makes
-- that row impossible to write; the query does not rely on it.
SELECT person.chat_instructions,
       person.citations_enabled,
       thread.notebook_id,
       COALESCE(person.display_name, '')::text AS display_name,
       person.memory_md,
       notebook.title AS notebook_title,
       notebook.chat_goal AS notebook_goal,
       notebook.chat_instructions AS notebook_instructions,
       notebook.response_length AS notebook_length
FROM new_chat_threads AS thread
JOIN "user" AS person ON person.id = thread.user_id
LEFT JOIN notebooks AS notebook
       ON notebook.id = thread.notebook_id AND notebook.user_id = thread.user_id
WHERE thread.id = sqlc.arg(thread_id) AND thread.user_id = sqlc.arg(user_id);

-- name: InsertUserMessage :one
-- The question. The partial unique index on (thread_id, turn_id, role) makes
-- a repeated write of the same turn a no-op that answers no row.
INSERT INTO new_chat_messages (role, content, thread_id, author_id, turn_id)
VALUES ('USER', sqlc.arg(content), sqlc.arg(thread_id), sqlc.arg(author_id), sqlc.arg(turn_id))
ON CONFLICT (thread_id, turn_id, role) WHERE turn_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: InsertAssistantMessage :one
-- The answer's row, written empty before the first token so that its id can
-- be sent to the page, and filled as the turn runs.
INSERT INTO new_chat_messages (role, content, thread_id, author_id, turn_id)
VALUES ('ASSISTANT', sqlc.arg(content), sqlc.arg(thread_id), NULL, sqlc.arg(turn_id))
ON CONFLICT (thread_id, turn_id, role) WHERE turn_id IS NOT NULL DO NOTHING
RETURNING id;

-- name: TouchThread :exec
-- A thread with a new question sorts to the top of the list.
UPDATE new_chat_threads SET updated_at = now()
WHERE id = sqlc.arg(thread_id) AND user_id = sqlc.arg(user_id);

-- name: StoreMessageContent :execrows
UPDATE new_chat_messages SET content = sqlc.arg(content) WHERE id = sqlc.arg(message_id);

-- name: RecordTokenUsage :exec
-- One row per answer; the partial unique index on message_id drops a second.
INSERT INTO token_usage (
    prompt_tokens, completion_tokens, total_tokens, cost_micros,
    model_breakdown, call_details, usage_type,
    thread_id, message_id, user_id
) VALUES (
    sqlc.arg(prompt_tokens), sqlc.arg(completion_tokens), sqlc.arg(total_tokens), 0,
    sqlc.arg(model_breakdown), sqlc.arg(call_details), 'chat',
    sqlc.arg(thread_id), sqlc.arg(message_id), sqlc.arg(user_id)
)
ON CONFLICT (message_id) WHERE message_id IS NOT NULL DO NOTHING;

-- name: MarkResponding :exec
-- Whether the assistant is answering in the thread, which every open tab of
-- its owner shows. The account being answered, or NULL when nobody is.
INSERT INTO chat_session_state (thread_id, ai_responding_to_user_id, updated_at)
VALUES (sqlc.arg(thread_id), sqlc.narg(user_id), now())
ON CONFLICT (thread_id) DO UPDATE
SET ai_responding_to_user_id = EXCLUDED.ai_responding_to_user_id, updated_at = EXCLUDED.updated_at;

-- name: HistoryMessages :many
-- The conversation as the page shows it, oldest first, for a thread the loop
-- holds no transcript of yet. `id` breaks ties: two rows of one millisecond
-- must come back in the order they were written.
SELECT role::text AS role, content
FROM new_chat_messages
WHERE thread_id = sqlc.arg(thread_id) AND role IN ('USER', 'ASSISTANT')
ORDER BY created_at, id;

-- name: AssistantMessageCount :one
SELECT count(*) FROM new_chat_messages
WHERE thread_id = sqlc.arg(thread_id) AND role = 'ASSISTANT';

-- name: LatestAssistantMessage :one
SELECT id FROM new_chat_messages
WHERE thread_id = sqlc.arg(thread_id) AND role = 'ASSISTANT'
ORDER BY id DESC
LIMIT 1;

-- name: SetThreadTitle :exec
UPDATE new_chat_threads SET title = sqlc.arg(title)
WHERE id = sqlc.arg(thread_id) AND user_id = sqlc.arg(user_id);

-- name: ToolIsTrusted :one
-- Whether this account has said "always allow" for this tool. Asked at the
-- moment a call is decided, every time: a rule removed in settings stops
-- applying to the very next call.
SELECT EXISTS (
    SELECT 1 FROM agent_permission_rules
    WHERE user_id = sqlc.arg(user_id)
      AND thread_id IS NULL
      AND permission = sqlc.arg(tool)::text
      AND pattern = '*'
      AND action = 'allow'
);

-- name: TrustTool :exec
-- Remember "always allow". A second click writes nothing: the unique
-- constraint compares the NULL thread as equal to itself (NULLS NOT
-- DISTINCT), so the conflict is found.
INSERT INTO agent_permission_rules (user_id, thread_id, permission, pattern, action)
VALUES (sqlc.arg(user_id), NULL, sqlc.arg(tool)::text, '*', 'allow')
ON CONFLICT ON CONSTRAINT uq_agent_permission_rules_scope DO NOTHING;

-- name: ChatModelOfAccount :one
-- The model an account chose for chat, by the name the model proxy knows it
-- by. No row means it has chosen none.
SELECT model FROM user_models
WHERE user_id = sqlc.arg(user_id) AND purpose = 'chat';

-- name: ChooseChatModel :exec
INSERT INTO user_models (user_id, purpose, model)
VALUES (sqlc.arg(user_id), 'chat', sqlc.arg(model))
ON CONFLICT (user_id, purpose) DO UPDATE
SET model = EXCLUDED.model, updated_at = now();

-- name: ClearChatModel :exec
-- Back to the deployment's default: no row, not a row that says so.
DELETE FROM user_models
WHERE user_id = sqlc.arg(user_id) AND purpose = 'chat';

-- name: MessageContent :one
-- One message's content, by id within its thread: the thread is named so
-- that an id from elsewhere reads nothing.
SELECT content FROM new_chat_messages
WHERE id = sqlc.arg(message_id) AND thread_id = sqlc.arg(thread_id);

-- The statements of "ask again" and "edit and ask again" (regenerate.go).
-- A message's place in a thread is (created_at, id), the order the page
-- shows: `id` breaks the tie of two rows written in one millisecond.

-- name: MessageInThread :one
-- The message the page named, by id within its thread.
SELECT id, role::text AS role, turn_id, created_at, content
FROM new_chat_messages
WHERE id = sqlc.arg(message_id) AND thread_id = sqlc.arg(thread_id);

-- name: LatestQuestion :one
SELECT id, role::text AS role, turn_id, created_at, content
FROM new_chat_messages
WHERE thread_id = sqlc.arg(thread_id) AND role = 'USER'
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: QuestionOfTurn :one
-- The question an answer belongs to, by the turn id the two share.
SELECT id, role::text AS role, turn_id, created_at, content
FROM new_chat_messages
WHERE thread_id = sqlc.arg(thread_id) AND turn_id = sqlc.arg(turn_id)::text AND role = 'USER';

-- name: QuestionBefore :one
-- The question an answer belongs to when the two share no turn id: the
-- newest question at or before it. That is an answer written by a resumed
-- turn (which has a turn id of its own and no question), or a row from
-- before turn ids existed.
SELECT id, role::text AS role, turn_id, created_at, content
FROM new_chat_messages
WHERE thread_id = sqlc.arg(thread_id) AND role = 'USER'
  AND (created_at, id) <= (sqlc.arg(created_at)::timestamptz, sqlc.arg(message_id)::bigint)
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: MessageIDsFrom :many
-- A question and everything after it: what asking it again replaces.
SELECT id FROM new_chat_messages
WHERE thread_id = sqlc.arg(thread_id)
  AND (created_at, id) >= (sqlc.arg(created_at)::timestamptz, sqlc.arg(message_id)::bigint)
ORDER BY created_at, id;

-- name: HistoryBefore :many
-- The conversation before a question, for a thread whose transcript the loop
-- does not hold at that point.
SELECT role::text AS role, content
FROM new_chat_messages
WHERE thread_id = sqlc.arg(thread_id) AND role IN ('USER', 'ASSISTANT')
  AND (created_at, id) < (sqlc.arg(created_at)::timestamptz, sqlc.arg(message_id)::bigint)
ORDER BY created_at, id;

-- name: DeleteMessages :exec
-- The replaced rows. Their token_usage rows stay, with message_id set to
-- NULL by the foreign key: what was spent is still what was spent.
DELETE FROM new_chat_messages
WHERE thread_id = sqlc.arg(thread_id) AND id = ANY (sqlc.arg(message_ids)::bigint[]);

-- name: DeleteSnapshotsOfMessages :exec
-- A public snapshot that shows a message which no longer exists is removed
-- with it: a link must not go on showing an answer its thread has replaced.
DELETE FROM public_chat_snapshots
WHERE thread_id = sqlc.arg(thread_id) AND message_ids && sqlc.arg(message_ids)::bigint[];

-- name: NotebookOfThread :one
-- The notebook a thread belongs to, with what decides whether it still names
-- itself. Thread and notebook are both read under the account, so a thread
-- that is not the caller's, or that names a notebook that is not, has none.
SELECT notebook.id, notebook.title_source, notebook.auto_title
FROM new_chat_threads AS thread
JOIN notebooks AS notebook
  ON notebook.id = thread.notebook_id AND notebook.user_id = thread.user_id
WHERE thread.id = sqlc.arg(thread_id) AND thread.user_id = sqlc.arg(user_id);

-- name: LinkMentionedDocuments :many
-- Makes the mentioned documents sources of the notebook, and answers the ones
-- that were not sources already. Only the account's own documents are
-- linked, into the account's own notebook: an id from elsewhere, or one
-- that names nothing, matches no row, and the composite keys on
-- notebook_sources refuse the pair even if a statement tried. A document
-- that is already a source is left exactly as it is, unticked included:
-- unticking is the owner's choice and a mention does not undo it.
INSERT INTO notebook_sources (notebook_id, user_id, document_id)
SELECT notebook.id, notebook.user_id, document.id
FROM notebooks AS notebook
JOIN documents AS document
  ON document.user_id = notebook.user_id
 AND document.id = ANY(sqlc.arg(document_ids)::bigint[])
WHERE notebook.id = sqlc.arg(notebook_id) AND notebook.user_id = sqlc.arg(user_id)
ORDER BY document.id
ON CONFLICT DO NOTHING
RETURNING document_id::bigint;

-- name: LinkMentionedFolders :many
-- The same for mentioned folders.
INSERT INTO notebook_sources (notebook_id, user_id, folder_id)
SELECT notebook.id, notebook.user_id, folder.id
FROM notebooks AS notebook
JOIN folders AS folder
  ON folder.user_id = notebook.user_id
 AND folder.id = ANY(sqlc.arg(folder_ids)::bigint[])
WHERE notebook.id = sqlc.arg(notebook_id) AND notebook.user_id = sqlc.arg(user_id)
ORDER BY folder.id
ON CONFLICT DO NOTHING
RETURNING folder_id::bigint;

-- name: TouchNotebook :exec
-- A notebook that gained a source rises in the recent list.
UPDATE notebooks SET updated_at = now()
WHERE id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id);

-- name: AnyDocumentReady :one
-- Whether any of the documents has finished indexing: a notebook is named
-- from sources that can be read, not from ones still being processed.
SELECT EXISTS (
    SELECT 1 FROM documents
    WHERE user_id = sqlc.arg(user_id)
      AND id = ANY(sqlc.arg(document_ids)::bigint[]) AND status ->> 'state' = 'ready'
);
