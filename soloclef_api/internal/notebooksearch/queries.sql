-- The one statement behind the search inside notebooks.

-- name: NotebookDocuments :many
-- Every document the caller's notebooks read, once per notebook, with how
-- it gets there and the notebook's own card. Three ways in, in the order a
-- result prefers them when a document has more than one (`reach`):
--
--   1  a ticked document source: the result opens that source's guide;
--   2  a note filed in the notebook: the result opens the note;
--   3  a document directly inside a ticked folder source (one level, as the
--      notebook's chat reads a folder): the result names the folder.
--
-- Studio outputs are documents of type ARTIFACT and are not sources, so
-- they are left out even when one sits in a source folder; a document being
-- deleted is already gone as far as a person can tell.
--
-- Every arm names the owner on the row it starts from and again on each
-- join, so a source row could never reach another account's document,
-- folder or notebook. DISTINCT ON keeps one way in per notebook and
-- document, and the order makes the statement's answer the same every time.
SELECT DISTINCT ON (way.notebook_id, way.document_id)
       way.notebook_id, way.document_id,
       -- 0 for a note, which has no source row; no row has the id 0.
       COALESCE(way.source_id, 0)::bigint AS source_id,
       way.folder_name, way.reach,
       n.title, n.icon, n.updated_at
FROM (
    SELECT s.notebook_id, d.id AS document_id, s.id AS source_id, NULL::text AS folder_name, 1 AS reach
    FROM notebook_sources s
    JOIN documents d ON d.id = s.document_id AND d.user_id = s.user_id
    WHERE s.user_id = sqlc.arg(user_id) AND s.enabled
      AND d.document_type <> 'ARTIFACT' AND d.status ->> 'state' <> 'deleting'
    UNION ALL
    SELECT note.notebook_id, d.id, NULL::bigint, NULL::text, 2
    FROM notes note
    JOIN documents d ON d.id = note.document_id AND d.user_id = note.user_id
    WHERE note.user_id = sqlc.arg(user_id) AND note.notebook_id IS NOT NULL
      AND d.status ->> 'state' <> 'deleting'
    UNION ALL
    SELECT s.notebook_id, d.id, s.id, f.name::text, 3
    FROM notebook_sources s
    JOIN folders f ON f.id = s.folder_id AND f.user_id = s.user_id
    JOIN documents d ON d.folder_id = f.id AND d.user_id = s.user_id
    WHERE s.user_id = sqlc.arg(user_id) AND s.enabled
      AND d.document_type <> 'ARTIFACT' AND d.status ->> 'state' <> 'deleting'
) AS way
JOIN notebooks n ON n.id = way.notebook_id AND n.user_id = sqlc.arg(user_id)
ORDER BY way.notebook_id, way.document_id, way.reach, way.source_id;
