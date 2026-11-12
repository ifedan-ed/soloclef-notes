-- The studio's viewer frames. Every statement names the owner, joins
-- included: an output, an artifact, a document or a run of another account
-- is a row that is not there.

-- name: ReadyOutput :one
-- A finished output of the caller's notebook with the artifact its run made
-- and that artifact's text. The joins to the runs carry `ready`, so an
-- output still being made, failed or cancelled matches nothing: there is
-- nothing to view yet. `primary_type` is the media type of the artifact's
-- main file ('' when it has none), which is how an interactive report is
-- told from a written one.
SELECT o.id, o.kind, o.title, o.options, o.source_document_ids,
       a.id AS artifact_id, a.generation, a.metadata,
       COALESCE(NULLIF(d.source_markdown, ''), d.content) AS markdown,
       COALESCE((SELECT f.mime_type FROM artifact_files f
                 WHERE f.artifact_id = a.id AND f.role = 'primary'), '')::text AS primary_type
FROM notebook_outputs o
LEFT JOIN podcast_runs p ON p.id = o.podcast_id AND p.user_id = o.user_id AND p.status = 'ready'
LEFT JOIN deliverable_jobs j ON j.id = o.deliverable_job_id AND j.user_id = o.user_id AND j.status = 'ready'
JOIN artifacts a ON a.id = COALESCE(p.artifact_id, j.artifact_id) AND a.user_id = o.user_id
JOIN documents d ON d.id = a.document_id AND d.user_id = a.user_id
WHERE o.id = sqlc.arg(output_id) AND o.notebook_id = sqlc.arg(notebook_id) AND o.user_id = sqlc.arg(user_id);

-- name: SourcesOf :many
-- The documents an output was made from that the caller still has, by
-- title. One that was deleted since is simply not listed.
SELECT id, title, document_type::text AS document_type FROM documents
WHERE user_id = sqlc.arg(user_id) AND id = ANY(sqlc.arg(document_ids)::bigint[])
ORDER BY title, id;

-- name: LinkedOutputs :many
-- The outputs suggestions point at, of this notebook, each with the state
-- of the run making it ('' when the run is gone). An id that is not listed
-- is an output the person deleted from the Studio.
SELECT o.id, COALESCE(p.status::text, j.status::text, '')::text AS run_status
FROM notebook_outputs o
LEFT JOIN podcast_runs p ON p.id = o.podcast_id AND p.user_id = o.user_id
LEFT JOIN deliverable_jobs j ON j.id = o.deliverable_job_id AND j.user_id = o.user_id
WHERE o.notebook_id = sqlc.arg(notebook_id) AND o.user_id = sqlc.arg(user_id)
  AND o.id = ANY(sqlc.arg(output_ids)::bigint[]);

-- name: LockArtifactMetadata :one
-- The report's artifact, locked for the length of one change to its
-- reading-view entry, so two tabs cannot each write over the other's.
SELECT generation, metadata FROM artifacts
WHERE id = sqlc.arg(artifact_id) AND user_id = sqlc.arg(user_id)
FOR UPDATE;

-- name: StoreReportView :execrows
-- The reading view's entry, written into the artifact's metadata beside
-- whatever else is kept there. `updated_at` is deliberately not moved: it
-- means "the report's content changed", and a dismissed suggestion is not
-- that.
UPDATE artifacts SET
    metadata = jsonb_set(CASE WHEN jsonb_typeof(metadata) = 'object' THEN metadata ELSE '{}'::jsonb END,
                         '{report_view}', sqlc.arg(entry)::jsonb)
WHERE id = sqlc.arg(artifact_id) AND user_id = sqlc.arg(user_id);
