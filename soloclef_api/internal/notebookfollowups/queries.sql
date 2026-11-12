-- The one statement behind a notebook's follow-up questions.

-- name: NotebookWithSourceTitles :one
-- Whether the notebook is the caller's, and the titles of its ticked
-- sources (a document's title, a folder's name) in the order they were
-- added. One statement: the ownership check and the titles cannot disagree,
-- and the joins name the owner again so a source row could never show
-- another account's title.
SELECT EXISTS (
           SELECT 1 FROM notebooks n
           WHERE n.id = sqlc.arg(notebook_id) AND n.user_id = sqlc.arg(user_id)
       ) AS owned,
       ARRAY(
           SELECT COALESCE(d.title, f.name)::text
           FROM notebook_sources s
           LEFT JOIN documents d ON d.id = s.document_id AND d.user_id = s.user_id
           LEFT JOIN folders f ON f.id = s.folder_id AND f.user_id = s.user_id
           WHERE s.notebook_id = sqlc.arg(notebook_id) AND s.user_id = sqlc.arg(user_id)
             AND s.enabled AND COALESCE(d.title, f.name) IS NOT NULL
           ORDER BY s.added_at, s.id
           LIMIT sqlc.arg(title_limit)
       )::text[] AS titles;
