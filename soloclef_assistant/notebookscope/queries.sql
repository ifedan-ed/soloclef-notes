-- A notebook as a turn confined to it reads it: its chat settings, and the
-- documents it may read. Every statement names the owner.

-- name: NotebookChatSettings :one
-- The owner's notebook by id, with its "Configure chat" choices. A notebook
-- of another account is no row.
SELECT title, chat_goal, COALESCE(chat_instructions, '')::text AS chat_instructions, response_length
FROM notebooks
WHERE id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id);

-- name: NotebookSources :many
-- A notebook's enabled sources with the name each is listed under, in the
-- order they were added. A source whose document or folder is not this
-- account's is not a source: the joins name the account, and a row that
-- matched neither is dropped.
SELECT source.document_id, source.folder_id,
       COALESCE(document.title, folder.name || '/', '')::text AS title
FROM notebook_sources AS source
LEFT JOIN documents AS document
       ON document.id = source.document_id AND document.user_id = sqlc.arg(user_id)
LEFT JOIN folders AS folder
       ON folder.id = source.folder_id AND folder.user_id = sqlc.arg(user_id)
WHERE source.notebook_id = sqlc.arg(notebook_id)
  AND source.user_id = sqlc.arg(user_id)
  AND source.enabled
  AND (document.id IS NOT NULL OR folder.id IS NOT NULL)
ORDER BY source.added_at, source.id;

-- name: NotebookDocuments :many
-- The documents a turn confined to a notebook may read: its enabled
-- document sources and the documents directly in its enabled folder
-- sources, all this account's. This list is the hard limit of every search
-- the turn makes. The account is named on the documents and on the sources
-- both, so neither a source row nor a document of another account can
-- widen it.
SELECT document.id
FROM documents AS document
WHERE document.user_id = sqlc.arg(user_id)
  AND (
      document.id IN (
          SELECT source.document_id FROM notebook_sources AS source
          WHERE source.notebook_id = sqlc.arg(notebook_id) AND source.user_id = sqlc.arg(user_id)
            AND source.enabled AND source.document_id IS NOT NULL
      )
      OR document.folder_id IN (
          SELECT source.folder_id FROM notebook_sources AS source
          WHERE source.notebook_id = sqlc.arg(notebook_id) AND source.user_id = sqlc.arg(user_id)
            AND source.enabled AND source.folder_id IS NOT NULL
      )
  )
ORDER BY document.id;
