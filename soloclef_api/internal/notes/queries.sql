-- The statements of the notes. A note is a `documents` row of type NOTE (its
-- title and its Markdown, indexed like any document) and a `notes` row beside
-- it (the notebook it is filed in, where it came from, whether it is pinned).
-- Both carry the owner, and every statement names the owner: another
-- account's note is a row this file cannot reach.
--
-- A note being deleted (status "deleting") is already gone as far as a
-- person can tell, so every read and write below passes over it.

-- name: ListNotes :many
-- A page of the owner's notes, with the count of every note the filters
-- match (one window count, so the page and its total come from one
-- statement and cannot disagree).
--
-- Pinned notes come first, the most recently pinned on top. Then, for a
-- search, the best match: a title containing the words outranks a note that
-- only mentions them, and full-text rank orders the rest. Then the chosen
-- sort, then the id, so a page is the same page every time it is asked for.
--
-- The search is two tests ORed: the title as typed (ILIKE, which the
-- trigram index on title serves, and which finds "pharm" while a word is
-- still being typed) and full text over title and text, which finds a word
-- anywhere in a long note with English stemming ("dosing" finds "dose").
-- Full text is computed per row rather than indexed: it runs over one
-- account's notes only, which the owner's indexes have already narrowed.
--
-- The preview is the start of the text, or for a search the passage that
-- matched, so a hit deep inside a long note shows why it was found.
SELECT d.id, d.title, n.origin, n.notebook_id, nb.title AS notebook_title,
       nb.icon AS notebook_icon, n.thread_id, n.message_id, n.pinned_at,
       (d.status ->> 'state')::text AS state, d.created_at,
       COALESCE(d.updated_at, d.created_at)::timestamptz AS edited_at,
       (CASE
            WHEN sqlc.narg(query)::text IS NULL
                THEN left(COALESCE(d.source_markdown, ''), sqlc.arg(preview_length)::int)
            ELSE ts_headline('english', COALESCE(d.source_markdown, ''),
                             websearch_to_tsquery('english', sqlc.narg(query)::text),
                             'MaxFragments=1, MaxWords=40, MinWords=18, StartSel="", StopSel=""')
        END)::text AS preview,
       count(*) OVER () AS total
FROM notes n
JOIN documents d ON d.id = n.document_id AND d.user_id = n.user_id
LEFT JOIN notebooks nb ON nb.id = n.notebook_id AND nb.user_id = n.user_id
WHERE n.user_id = sqlc.arg(user_id)
  AND d.status ->> 'state' <> 'deleting'
  AND (sqlc.narg(notebook_id)::bigint IS NULL OR n.notebook_id = sqlc.narg(notebook_id)::bigint)
  AND (NOT sqlc.arg(unfiled)::boolean OR n.notebook_id IS NULL)
  AND (NOT sqlc.arg(pinned_only)::boolean OR n.pinned_at IS NOT NULL)
  AND (sqlc.narg(origin)::text IS NULL OR n.origin = sqlc.narg(origin)::text)
  AND (sqlc.narg(thread_id)::bigint IS NULL OR n.thread_id = sqlc.narg(thread_id)::bigint)
  AND (sqlc.narg(query)::text IS NULL
       OR d.title ILIKE sqlc.narg(title_pattern)::text
       OR to_tsvector('english', d.title || ' ' || COALESCE(d.source_markdown, ''))
          @@ websearch_to_tsquery('english', sqlc.narg(query)::text))
ORDER BY n.pinned_at DESC NULLS LAST,
         (sqlc.narg(query)::text IS NOT NULL AND d.title ILIKE sqlc.narg(title_pattern)::text) DESC,
         CASE WHEN sqlc.narg(query)::text IS NULL THEN 0
              ELSE ts_rank(to_tsvector('english', d.title || ' ' || COALESCE(d.source_markdown, '')),
                           websearch_to_tsquery('english', sqlc.narg(query)::text))
         END DESC,
         CASE WHEN sqlc.arg(sort)::text = 'title' THEN lower(d.title) END ASC,
         CASE WHEN sqlc.arg(sort)::text = 'created' THEN d.created_at END DESC,
         COALESCE(d.updated_at, d.created_at) DESC,
         d.id DESC
LIMIT sqlc.arg(page_size)::bigint OFFSET sqlc.arg(skip)::bigint;

-- name: NoteOfOwner :one
-- One note with its text, for reading and editing, and the notebook whose
-- chat a kept answer came from (which is where "From a chat" links to: the
-- note may since have been filed elsewhere).
SELECT d.id, d.title, COALESCE(d.source_markdown, '')::text AS markdown,
       n.origin, n.notebook_id, nb.title AS notebook_title, nb.icon AS notebook_icon,
       n.automation_id, n.thread_id, n.message_id, t.notebook_id AS chat_notebook_id,
       n.pinned_at, (d.status ->> 'state')::text AS state, d.created_at,
       COALESCE(d.updated_at, d.created_at)::timestamptz AS edited_at
FROM notes n
JOIN documents d ON d.id = n.document_id AND d.user_id = n.user_id
LEFT JOIN notebooks nb ON nb.id = n.notebook_id AND nb.user_id = n.user_id
LEFT JOIN new_chat_threads t ON t.id = n.thread_id AND t.user_id = n.user_id
WHERE n.document_id = sqlc.arg(id) AND n.user_id = sqlc.arg(user_id)
  AND d.status ->> 'state' <> 'deleting';

-- name: SourcesOfNotes :many
-- The notebooks each of these notes is switched on as a source of. A note is
-- read by a notebook's chat through this row and nothing else: the switch is
-- the row, so nothing is copied and nothing drifts.
SELECT s.document_id, s.notebook_id, nb.title AS notebook_title, nb.icon AS notebook_icon
FROM notebook_sources s
JOIN notebooks nb ON nb.id = s.notebook_id AND nb.user_id = s.user_id
WHERE s.user_id = sqlc.arg(user_id) AND s.document_id = ANY(sqlc.arg(document_ids)::bigint[])
ORDER BY s.document_id, lower(nb.title), s.notebook_id;

-- name: NotebookOfOwner :one
-- Whether the owner has this notebook: a note can be filed in, or switched
-- on as a source of, the owner's own notebooks only.
SELECT id FROM notebooks WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: ThreadOfOwner :one
-- The conversation an answer is kept from, and the notebook it is the chat
-- of (NULL for a plain chat), so that a kept answer is filed where it was
-- asked.
SELECT id, notebook_id FROM new_chat_threads
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: MessageInThread :one
-- Whether the message is one of that thread's. Messages carry no owner of
-- their own; the thread, checked first, is what makes it the caller's.
SELECT id FROM new_chat_messages
WHERE id = sqlc.arg(id) AND thread_id = sqlc.arg(thread_id);

-- name: InsertNoteDocument :one
-- The note's document. `content` holds the same Markdown until the indexer
-- writes what it indexed, so anything reading `content` has the note's text
-- from the first moment. A note with text starts "pending", because a job
-- is queued for it after the commit; an empty note has nothing to index and
-- starts "ready".
INSERT INTO documents (user_id, title, document_type, content, content_hash,
                       source_markdown, content_needs_reindexing,
                       document_metadata, status)
VALUES (sqlc.arg(user_id), sqlc.arg(title), 'NOTE', sqlc.arg(markdown)::text,
        sqlc.arg(content_hash), sqlc.arg(markdown)::text, sqlc.arg(needs_indexing)::boolean,
        '{"NOTE": true}'::jsonb,
        CASE WHEN sqlc.arg(needs_indexing)::boolean THEN '{"state": "pending"}'::jsonb
             ELSE '{"state": "ready"}'::jsonb END)
RETURNING id;

-- name: InsertNoteRecord :exec
-- What makes the document a note, written in the same transaction, so there
-- is never a NOTE document without one.
INSERT INTO notes (document_id, user_id, notebook_id, origin, thread_id, message_id)
VALUES (sqlc.arg(document_id), sqlc.arg(user_id), sqlc.narg(notebook_id),
        sqlc.arg(origin), sqlc.narg(thread_id), sqlc.narg(message_id));

-- name: UpdateNoteText :one
-- A save: the title, the text, or both. The row is locked and its state read
-- in the same statement as the write, so the route knows whether an
-- indexing job is already waiting (state "pending") and need not queue
-- another.
--
-- Only a change of text needs indexing; the indexer reads the Markdown and
-- not the title. A text that changed is marked pending, an emptied one
-- included: the indexer takes an emptied note's passages out of search
-- and marks it ready with none, and without the job they would stay.
WITH old AS (
    SELECT d.id, d.status ->> 'state' AS previous_state,
           COALESCE(d.source_markdown, '') AS previous_markdown
    FROM documents d
    WHERE d.id = sqlc.arg(id) AND d.user_id = sqlc.arg(user_id) AND d.document_type = 'NOTE'
      AND d.status ->> 'state' <> 'deleting'
    FOR UPDATE
),
changed AS (
    SELECT old.id, old.previous_state,
           (sqlc.narg(markdown)::text IS NOT NULL
            AND sqlc.narg(markdown)::text IS DISTINCT FROM old.previous_markdown) AS needs_indexing
    FROM old
)
UPDATE documents d
SET title = COALESCE(sqlc.narg(title)::text, d.title),
    source_markdown = COALESCE(sqlc.narg(markdown)::text, d.source_markdown),
    content_hash = COALESCE(sqlc.narg(content_hash)::text, d.content_hash),
    content_needs_reindexing = d.content_needs_reindexing OR changed.needs_indexing,
    status = CASE WHEN changed.needs_indexing THEN '{"state": "pending"}'::jsonb ELSE d.status END,
    updated_at = now()
FROM changed
WHERE d.id = changed.id
RETURNING d.id, changed.previous_state::text AS previous_state,
          changed.needs_indexing::boolean AS needs_indexing;

-- name: SetNotePinned :execrows
-- Pinning keeps a note at the top of every list it is in. It is not an
-- edit: the note's "last edited" is left alone, and pinning twice keeps
-- the first pin's time.
UPDATE notes n
SET pinned_at = CASE WHEN sqlc.arg(pinned)::boolean THEN COALESCE(n.pinned_at, now()) END
FROM documents d
WHERE n.document_id = sqlc.arg(id) AND n.user_id = sqlc.arg(user_id)
  AND d.id = n.document_id AND d.user_id = n.user_id AND d.status ->> 'state' <> 'deleting';

-- name: FileNote :execrows
-- Files the note in one of the owner's notebooks, or in none (NULL). The
-- composite foreign key refuses another account's notebook as well; the
-- route checks first so that it can answer 404 rather than fail.
UPDATE notes n
SET notebook_id = sqlc.narg(notebook_id)
FROM documents d
WHERE n.document_id = sqlc.arg(id) AND n.user_id = sqlc.arg(user_id)
  AND d.id = n.document_id AND d.user_id = n.user_id AND d.status ->> 'state' <> 'deleting';

-- name: SwitchSourceOn :exec
-- The note becomes a source of the notebook: the notebook's chat reads the
-- note's own passages from now on. Switching on twice is one row.
INSERT INTO notebook_sources (notebook_id, user_id, document_id)
VALUES (sqlc.arg(notebook_id), sqlc.arg(user_id), sqlc.arg(document_id))
ON CONFLICT (notebook_id, document_id) DO NOTHING;

-- name: SwitchSourceOff :exec
-- The notebook's chat stops reading the note. The note itself is untouched.
DELETE FROM notebook_sources
WHERE notebook_id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id)
  AND document_id = sqlc.arg(document_id);

-- name: NoteToRemove :one
-- The delete's lookup. A note already marked "deleting" is found, so a
-- removal an earlier request did not finish is carried on by the next one.
SELECT d.id FROM documents d
JOIN notes n ON n.document_id = d.id AND n.user_id = d.user_id
WHERE d.id = sqlc.arg(id) AND d.user_id = sqlc.arg(user_id);
