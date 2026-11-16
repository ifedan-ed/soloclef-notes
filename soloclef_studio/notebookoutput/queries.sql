-- A studio output: the row a notebook's Studio lists. Written here and
-- nowhere else, by the Studio's own buttons (soloclef_api/internal/
-- studiogenerate) and by a notebook's chat (soloclef_studio/artifactsave,
-- soloclef_assistant/deliverables), so the two cannot list an output two
-- different ways.

-- name: CreateOutput :one
-- The output, pointing at the run that makes it, or at the artifact a chat
-- made with no run behind it. The notebook is read under the owner, so an
-- output is never added to another account's notebook (no row is written
-- and none returned), and a notebook used is a notebook moved to the top of
-- the list. `notebook_source_ids` is the notebook sources a Studio dialog
-- chose (NULL when nobody chose, as for what a chat made), which Try again
-- sends back.
WITH touched AS (
    UPDATE notebooks AS n SET updated_at = now()
    WHERE n.id = sqlc.arg(notebook_id) AND n.user_id = sqlc.arg(user_id)
    RETURNING n.id, n.user_id
)
INSERT INTO notebook_outputs (notebook_id, user_id, kind, title, options, source_document_ids,
                              podcast_id, deliverable_job_id, artifact_id, thread_id, generated_by, request_key,
                              notebook_source_ids)
SELECT touched.id, touched.user_id, sqlc.arg(kind), sqlc.arg(title), sqlc.arg(options)::jsonb,
       sqlc.arg(source_document_ids)::jsonb, sqlc.narg(podcast_id), sqlc.narg(deliverable_job_id),
       sqlc.narg(artifact_id), sqlc.narg(thread_id), sqlc.narg(generated_by)::jsonb, sqlc.narg(request_key),
       sqlc.narg(notebook_source_ids)::bigint[]
FROM touched
RETURNING notebook_outputs.id;
