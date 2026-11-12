-- The statements of the chat's knowledge-base specialist and of its undo.
--
-- Every statement names the owner. A folder, a document, an action or a
-- revision of another account is a row these statements cannot reach, so it
-- answers exactly as one that was never made. A document being deleted
-- (status "deleting") is already gone as far as a person can tell, and every
-- read and write here passes over it.

-- ---------------------------------------------------------------------------
-- Reading the tree
-- ---------------------------------------------------------------------------

-- name: AccountFolders :many
-- The account's whole folder tree, flat: what a listing's paths are worked
-- out from in Go. A tree is tens or hundreds of rows; one read of all of it
-- is cheaper than a recursive walk per folder shown.
SELECT id, name, parent_id
FROM folders
WHERE user_id = sqlc.arg(user_id)
ORDER BY position, id;

-- name: DocumentsInFolder :many
-- The documents filed directly in one folder (NULL: in none), the newest
-- edit first, one more than the page so the caller knows there are more.
SELECT id, title, document_type::text AS document_type, (status ->> 'state')::text AS state
FROM documents
WHERE user_id = sqlc.arg(user_id)
  AND status ->> 'state' <> 'deleting'
  AND (folder_id = sqlc.narg(folder_id)::bigint
       OR (sqlc.narg(folder_id)::bigint IS NULL AND folder_id IS NULL))
ORDER BY COALESCE(updated_at, created_at) DESC, id DESC
LIMIT sqlc.arg(page_size)::bigint;

-- name: CountDocumentsInFolder :one
SELECT count(*) FROM documents
WHERE user_id = sqlc.arg(user_id)
  AND status ->> 'state' <> 'deleting'
  AND (folder_id = sqlc.narg(folder_id)::bigint
       OR (sqlc.narg(folder_id)::bigint IS NULL AND folder_id IS NULL));

-- name: FindDocuments :many
-- Documents whose title holds the words as typed (a title is how a person
-- names what they mean), then those whose text matches them in full text.
-- Over one account's documents only, which the owner index narrows first.
--
-- In a notebook's chat (`confined`) a match on the text is a match only
-- among what that chat may read, the notebook's sources (`readable`) and
-- the notes filed in it: that a document says a word is something of its
-- text. A title is matched everywhere, as the source picker lists every
-- title.
SELECT d.id, d.title, d.document_type::text AS document_type, d.folder_id,
       (d.status ->> 'state')::text AS state
FROM documents d
WHERE d.user_id = sqlc.arg(user_id)
  AND d.status ->> 'state' <> 'deleting'
  AND (d.title ILIKE sqlc.arg(title_pattern)::text
       OR ((NOT sqlc.arg(confined)::boolean
            OR d.id = ANY(sqlc.arg(readable)::bigint[])
            OR EXISTS (SELECT 1 FROM notes n WHERE n.document_id = d.id AND n.user_id = d.user_id
                       AND n.notebook_id = sqlc.narg(notebook_id)::bigint))
           AND to_tsvector('english', d.title || ' ' || COALESCE(d.source_markdown, ''))
               @@ websearch_to_tsquery('english', sqlc.arg(query)::text)))
ORDER BY (d.title ILIKE sqlc.arg(title_pattern)::text) DESC,
         COALESCE(d.updated_at, d.created_at) DESC, d.id DESC
LIMIT sqlc.arg(page_size)::bigint;

-- name: FiledInNotebook :one
-- Whether a document is a note filed in this notebook: in the notebook's
-- chat, one of the documents whose text may be read.
SELECT EXISTS (SELECT 1 FROM notes
               WHERE document_id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
                 AND notebook_id = sqlc.arg(notebook_id))::boolean;

-- name: DocumentToRead :one
-- One document with its text: the Markdown a person or the indexer wrote,
-- or, before indexing has written any, the content it arrived with.
SELECT id, title, document_type::text AS document_type, folder_id,
       (status ->> 'state')::text AS state,
       COALESCE(source_markdown, content)::text AS text
FROM documents
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND status ->> 'state' <> 'deleting';

-- ---------------------------------------------------------------------------
-- Changing a document
-- ---------------------------------------------------------------------------

-- name: LockDocument :one
-- A document about to be changed, locked until the change commits, with
-- everything its before-state and its after-state are made of. `is_note`
-- says the document is a note (a notes row beside it): only a note's text
-- is the document, so only a note's text is edited or deleted here.
-- `has_files` is a stored file under it, which no note normally has and
-- which a delete here could not bring back.
SELECT d.id, d.title, d.document_type::text AS document_type, d.folder_id,
       COALESCE(d.source_markdown, '')::text AS markdown,
       md5(COALESCE(d.source_markdown, ''))::text AS text_md5,
       (d.status ->> 'state')::text AS state, d.document_metadata,
       (n.document_id IS NOT NULL)::boolean AS is_note,
       n.notebook_id, n.origin, n.automation_id, n.thread_id, n.message_id, n.pinned_at,
       EXISTS (SELECT 1 FROM document_files f WHERE f.document_id = d.id AND f.user_id = d.user_id)::boolean AS has_files
FROM documents d
LEFT JOIN notes n ON n.document_id = d.id AND n.user_id = d.user_id
WHERE d.id = sqlc.arg(id) AND d.user_id = sqlc.arg(user_id)
  AND d.status ->> 'state' <> 'deleting'
FOR UPDATE OF d;

-- name: InsertNote :one
-- A note the specialist writes. Like a note written by hand, `content` holds
-- the Markdown until the indexer writes what it indexed, and a note with
-- text starts "pending" because its job is queued after the commit.
INSERT INTO documents (user_id, title, document_type, content, content_hash,
                       source_markdown, content_needs_reindexing,
                       document_metadata, folder_id, status, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(title), 'NOTE', sqlc.arg(markdown)::text,
        sqlc.arg(content_hash), sqlc.arg(markdown)::text, sqlc.arg(needs_indexing)::boolean,
        COALESCE(sqlc.narg(document_metadata)::jsonb, '{"NOTE": true}'::jsonb), sqlc.narg(folder_id),
        CASE WHEN sqlc.arg(needs_indexing)::boolean THEN '{"state": "pending"}'::jsonb
             ELSE '{"state": "ready"}'::jsonb END,
        now())
RETURNING id;

-- name: InsertNoteRecord :exec
-- What makes the document a note. A thread, message, notebook or automation
-- that is not the owner's, or no longer exists, is written as NULL: that is
-- what deleting it would have done to the note (ON DELETE SET NULL), so a
-- note brought back is filed as it would be had it never left.
INSERT INTO notes (document_id, user_id, notebook_id, origin, automation_id, thread_id, message_id, pinned_at)
VALUES (sqlc.arg(document_id), sqlc.arg(user_id),
        (SELECT nb.id FROM notebooks nb WHERE nb.id = sqlc.narg(notebook_id)::bigint AND nb.user_id = sqlc.arg(user_id)),
        sqlc.arg(origin),
        (SELECT a.id FROM automations a WHERE a.id = sqlc.narg(automation_id)::bigint AND a.user_id = sqlc.arg(user_id)),
        (SELECT t.id FROM new_chat_threads t WHERE t.id = sqlc.narg(thread_id)::bigint AND t.user_id = sqlc.arg(user_id)),
        (SELECT m.id FROM new_chat_messages m
         JOIN new_chat_threads t ON t.id = m.thread_id AND t.user_id = sqlc.arg(user_id)
         WHERE m.id = sqlc.narg(message_id)::bigint),
        sqlc.narg(pinned_at));

-- name: SetNoteText :exec
-- A note's new text, marked for indexing as the editor's own save marks it.
UPDATE documents
SET source_markdown = sqlc.arg(markdown)::text,
    content_hash = sqlc.arg(content_hash),
    content_needs_reindexing = true,
    status = '{"state": "pending"}'::jsonb,
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: SetDocumentTitle :exec
UPDATE documents SET title = sqlc.arg(title), updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: SetDocumentFolder :exec
-- Where a document is filed. `updated_at` is left alone, as the document
-- browser's own move leaves it: filing a document is not editing it. A
-- folder of another account cannot be named: the composite foreign key
-- (folder_id, user_id) refuses it.
UPDATE documents SET folder_id = sqlc.narg(folder_id)
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: ChunkIDsOfDocument :many
-- A note's passages, read before its row is deleted: each id is also its
-- point's id in the vector store, and once the row is gone nothing names
-- them.
SELECT id FROM chunks WHERE document_id = sqlc.arg(document_id) AND user_id = sqlc.arg(user_id)
ORDER BY id;

-- name: DeleteDocument :exec
-- The row and everything that cascades from it: its passages, its note
-- record, its versions and its notebook switches.
DELETE FROM documents WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: DocumentNotebookSources :many
-- The notebooks a document is switched on in, kept with its deletion so an
-- undo switches them on again.
SELECT notebook_id, enabled FROM notebook_sources
WHERE document_id = sqlc.arg(document_id)::bigint AND user_id = sqlc.arg(user_id)
ORDER BY notebook_id;

-- name: FolderNotebookSources :many
SELECT notebook_id, enabled FROM notebook_sources
WHERE folder_id = sqlc.arg(folder_id)::bigint AND user_id = sqlc.arg(user_id)
ORDER BY notebook_id;

-- name: RestoreDocumentSource :exec
-- One switch brought back, in a notebook that is still the owner's.
INSERT INTO notebook_sources (notebook_id, user_id, document_id, enabled)
SELECT nb.id, nb.user_id, sqlc.arg(document_id)::bigint, sqlc.arg(enabled)::boolean
FROM notebooks nb WHERE nb.id = sqlc.arg(notebook_id) AND nb.user_id = sqlc.arg(user_id)
ON CONFLICT (notebook_id, document_id) DO NOTHING;

-- name: RestoreFolderSource :exec
INSERT INTO notebook_sources (notebook_id, user_id, folder_id, enabled)
SELECT nb.id, nb.user_id, sqlc.arg(folder_id)::bigint, sqlc.arg(enabled)::boolean
FROM notebooks nb WHERE nb.id = sqlc.arg(notebook_id) AND nb.user_id = sqlc.arg(user_id)
ON CONFLICT (notebook_id, folder_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Changing a folder
-- ---------------------------------------------------------------------------

-- name: LockFolder :one
SELECT id, name, parent_id, position
FROM folders
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
FOR UPDATE;

-- name: FolderExists :one
SELECT EXISTS (SELECT 1 FROM folders WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id))::boolean;

-- name: SiblingNamed :one
-- Whether a folder under this parent (NULL: the top level) already has the
-- name, other than the folder itself. The unique constraint would refuse it
-- too; asking first is what lets the refusal be a sentence.
SELECT EXISTS (
    SELECT 1 FROM folders
    WHERE user_id = sqlc.arg(user_id) AND name = sqlc.arg(name)
      AND id <> sqlc.arg(except_id)::bigint
      AND (parent_id = sqlc.narg(parent_id)::bigint
           OR (sqlc.narg(parent_id)::bigint IS NULL AND parent_id IS NULL))
)::boolean;

-- name: LastSiblingPosition :one
-- The last key under a parent, which a new or a moved folder goes after.
SELECT position FROM folders
WHERE user_id = sqlc.arg(user_id)
  AND (parent_id = sqlc.narg(parent_id)::bigint
       OR (sqlc.narg(parent_id)::bigint IS NULL AND parent_id IS NULL))
ORDER BY position DESC, id DESC
LIMIT 1;

-- name: FolderDepth :one
-- 1 for a top-level folder; 0 for one that is not the owner's.
WITH RECURSIVE ancestors AS (
    SELECT f.id, f.parent_id, 1 AS depth
    FROM folders f WHERE f.id = sqlc.arg(id) AND f.user_id = sqlc.arg(user_id)
    UNION ALL
    SELECT f.id, f.parent_id, a.depth + 1
    FROM folders f JOIN ancestors a ON f.id = a.parent_id
    WHERE f.user_id = sqlc.arg(user_id)
)
SELECT COALESCE(MAX(depth), 0)::int FROM ancestors;

-- name: SubtreeDepth :one
-- How many levels hang below a folder: 0 for a leaf.
WITH RECURSIVE descendants AS (
    SELECT f.id, 1 AS depth
    FROM folders f WHERE f.parent_id = sqlc.arg(id)::bigint AND f.user_id = sqlc.arg(user_id)
    UNION ALL
    SELECT f.id, d.depth + 1
    FROM folders f JOIN descendants d ON f.parent_id = d.id
    WHERE f.user_id = sqlc.arg(user_id)
)
SELECT COALESCE(MAX(depth), 0)::int FROM descendants;

-- name: IsWithin :one
-- Whether `id` is `ancestor` or sits anywhere below it: the move that would
-- put a folder inside itself.
WITH RECURSIVE ancestors AS (
    SELECT f.id, f.parent_id FROM folders f
    WHERE f.id = sqlc.arg(id) AND f.user_id = sqlc.arg(user_id)
    UNION ALL
    SELECT f.id, f.parent_id FROM folders f JOIN ancestors a ON f.id = a.parent_id
    WHERE f.user_id = sqlc.arg(user_id)
)
SELECT EXISTS (SELECT 1 FROM ancestors WHERE ancestors.id = sqlc.arg(ancestor)::bigint)::boolean;

-- name: FolderIsEmpty :one
-- No folder under it and no document in it. A document being deleted does
-- not count: it is gone as far as anyone can tell, and deleting the folder
-- only clears that row's folder.
SELECT (NOT EXISTS (SELECT 1 FROM folders c WHERE c.parent_id = sqlc.arg(id)::bigint AND c.user_id = sqlc.arg(user_id))
        AND NOT EXISTS (SELECT 1 FROM documents d WHERE d.folder_id = sqlc.arg(id)::bigint AND d.user_id = sqlc.arg(user_id)
                        AND d.status ->> 'state' <> 'deleting'))::boolean;

-- name: InsertFolder :one
INSERT INTO folders (user_id, name, position, parent_id, created_at, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(name), sqlc.arg(position), sqlc.narg(parent_id), now(), now())
RETURNING id;

-- name: SetFolderName :exec
UPDATE folders SET name = sqlc.arg(name), updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: SetFolderParent :exec
UPDATE folders SET parent_id = sqlc.narg(parent_id), position = sqlc.arg(position), updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: DeleteFolder :exec
DELETE FROM folders WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- ---------------------------------------------------------------------------
-- The record of a change
-- ---------------------------------------------------------------------------

-- name: InsertAction :one
-- One change the assistant made, written in the change's own transaction.
-- `reverse_descriptor` holds the after-state an undo compares with, so an
-- undo can tell whether the item has changed since.
INSERT INTO agent_action_log (thread_id, user_id, tool_call_id, chat_turn_id, tool_name, args,
                              result_id, reversible, reverse_descriptor, reverse_of)
VALUES (sqlc.arg(thread_id), sqlc.arg(user_id), sqlc.narg(tool_call_id), sqlc.narg(chat_turn_id),
        sqlc.arg(tool_name), sqlc.narg(args)::jsonb, sqlc.narg(result_id), sqlc.arg(reversible)::boolean,
        sqlc.narg(reverse_descriptor)::jsonb, sqlc.narg(reverse_of))
RETURNING id, created_at;

-- name: InsertDocumentRevision :exec
INSERT INTO document_revisions (document_id, user_id, content_before, title_before, folder_id_before,
                                metadata_before, created_by_turn_id, agent_action_id)
VALUES (sqlc.narg(document_id), sqlc.arg(user_id), sqlc.narg(content_before), sqlc.narg(title_before),
        sqlc.narg(folder_id_before), sqlc.narg(metadata_before)::jsonb, sqlc.narg(created_by_turn_id),
        sqlc.arg(agent_action_id));

-- name: InsertFolderRevision :exec
INSERT INTO folder_revisions (folder_id, user_id, name_before, parent_id_before, position_before,
                              created_by_turn_id, agent_action_id)
VALUES (sqlc.narg(folder_id), sqlc.arg(user_id), sqlc.narg(name_before), sqlc.narg(parent_id_before),
        sqlc.narg(position_before), sqlc.narg(created_by_turn_id), sqlc.arg(agent_action_id));

-- ---------------------------------------------------------------------------
-- Undo
-- ---------------------------------------------------------------------------

-- name: LockActionToUndo :one
-- The action, if it is the owner's, in the owner's thread: both are named,
-- as on the log's own list. Locked, so two undos of one action wait for
-- each other and the second finds the first's row.
SELECT a.id, a.tool_name, a.reversible, a.reverse_of, a.reverse_descriptor, a.error, a.chat_turn_id,
       COALESCE((SELECT u.id FROM agent_action_log u WHERE u.reverse_of = a.id), 0)::bigint AS undone_by
FROM agent_action_log a
JOIN new_chat_threads t ON t.id = a.thread_id AND t.user_id = sqlc.arg(user_id)
WHERE a.id = sqlc.arg(id) AND a.thread_id = sqlc.arg(thread_id) AND a.user_id = sqlc.arg(user_id)
FOR UPDATE OF a;

-- name: ThreadIsOwned :one
SELECT EXISTS (SELECT 1 FROM new_chat_threads WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id))::boolean;

-- name: TurnActions :many
-- A turn's actions, the newest first: the order a turn is unwound in, so a
-- note written into a folder made in the same turn goes before the folder.
SELECT a.id, a.tool_name
FROM agent_action_log a
JOIN new_chat_threads t ON t.id = a.thread_id AND t.user_id = sqlc.arg(user_id)
WHERE a.thread_id = sqlc.arg(thread_id) AND a.user_id = sqlc.arg(user_id)
  AND a.chat_turn_id = sqlc.arg(chat_turn_id)::text
ORDER BY a.created_at DESC, a.id DESC;

-- name: DocumentRevisionOf :one
SELECT id, document_id, content_before, title_before, folder_id_before, metadata_before
FROM document_revisions
WHERE agent_action_id = sqlc.arg(action_id)::bigint AND user_id = sqlc.arg(user_id)
ORDER BY id DESC
LIMIT 1;

-- name: FolderRevisionOf :one
SELECT id, folder_id, name_before, parent_id_before, position_before
FROM folder_revisions
WHERE agent_action_id = sqlc.arg(action_id)::bigint AND user_id = sqlc.arg(user_id)
ORDER BY id DESC
LIMIT 1;

-- name: PointDocumentRevision :exec
-- A deleted note brought back has a new id; its revision follows it, so the
-- log can still say which document the action was about.
UPDATE document_revisions SET document_id = sqlc.arg(document_id)::bigint
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: PointFolderRevision :exec
UPDATE folder_revisions SET folder_id = sqlc.arg(folder_id)::bigint
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);
