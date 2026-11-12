-- The statements of a source's guide. Both name the owner; the guide table
-- itself carries no owner column, so it is only ever reached through a
-- document that the same statement has already found to be the caller's.

-- name: SourceForGuide :one
-- The document behind one of the caller's notebook's sources, with its
-- guide if the guide is still about this text.
--
-- `cache_key` is what a current guide is stored under: the document's
-- content hash, and after it the language the guide was asked for in
-- (`key_suffix`, empty for "the document's own"), so that a guide in one
-- language is not served to a reader who asked for another. The hash is cut
-- to leave room for the suffix inside the column's 128 characters; a
-- SHA-256 in hex is 64.
--
-- The document's text is read only when there is no current guide: opening
-- a source whose guide is cached must not pull a book out of the database
-- to throw it away. And it is read up to `body_limit` characters: the guide
-- is written from excerpts taken across the text (excerpt.go), and past a
-- couple of million characters the rest is not worth the memory.
--
-- A folder source has no document and finds no row, as does a source of
-- another notebook or another account, and a document being deleted.
SELECT d.id AS document_id, d.title,
       (left(d.content_hash, 90) || sqlc.arg(key_suffix)::text)::text AS cache_key,
       COALESCE(g.summary, '')::text AS summary,
       (g.document_id IS NOT NULL)::boolean AS cached,
       CASE WHEN g.document_id IS NULL
            THEN left(btrim(COALESCE(NULLIF(d.source_markdown, ''), d.content)), sqlc.arg(body_limit)::int)
            ELSE '' END::text AS body
FROM notebook_sources s
JOIN documents d ON d.id = s.document_id AND d.user_id = s.user_id
LEFT JOIN document_source_guides g
       ON g.document_id = d.id
      AND g.content_hash = left(d.content_hash, 90) || sqlc.arg(key_suffix)::text
WHERE s.id = sqlc.arg(source_id) AND s.notebook_id = sqlc.arg(notebook_id)
  AND s.user_id = sqlc.arg(user_id)
  AND d.status ->> 'state' <> 'deleting';

-- name: SaveGuide :execrows
-- Keeps a guide, replacing the document's earlier one. It is stored under
-- the key read with the text it summarises, not under the document's hash
-- as it is now: if the document was saved while the model was writing, this
-- guide is about the old text, is stored as such, and the next open writes
-- a new one. The row is written only while the document is still the
-- caller's.
INSERT INTO document_source_guides (document_id, content_hash, summary)
SELECT d.id, sqlc.arg(cache_key)::text, sqlc.arg(summary)::text
FROM documents d
WHERE d.id = sqlc.arg(document_id) AND d.user_id = sqlc.arg(user_id)
ON CONFLICT (document_id) DO UPDATE
SET content_hash = EXCLUDED.content_hash, summary = EXCLUDED.summary, updated_at = now();
