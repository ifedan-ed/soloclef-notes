-- The statements of the end-to-end test routes (internal/e2eseed). They run
-- only in a stack whose operator switched the routes on and only for a
-- request that carries the stack's seed secret. None takes a caller: each
-- finds the owner from the row it is given (a notebook, an address) and
-- writes every new row under that owner, so what is seeded is shaped
-- exactly as the product's own rows are.

-- name: AccountByAddress :one
SELECT id FROM "user" WHERE lower(email) = lower(sqlc.arg(email)::text);

-- name: SeedNotification :one
INSERT INTO notifications (user_id, type, title, message, read, metadata, created_at, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(type), sqlc.arg(title), sqlc.arg(message), sqlc.arg(read),
        sqlc.arg(metadata)::jsonb, sqlc.arg(created_at)::timestamptz, sqlc.arg(created_at)::timestamptz)
RETURNING id;

-- name: ExpireSnapshotLink :execrows
UPDATE public_chat_snapshots SET expires_at = sqlc.arg(expired_at)::timestamptz WHERE id = sqlc.arg(id);

-- name: ExpireNotebookLink :execrows
UPDATE notebook_public_links SET expires_at = sqlc.arg(expired_at)::timestamptz WHERE id = sqlc.arg(id);

-- name: NotebookOwner :one
SELECT user_id FROM notebooks WHERE id = sqlc.arg(id);

-- name: NotebookSourceDocuments :one
-- The documents a notebook reads, as the JSON list an output records.
SELECT COALESCE(jsonb_agg(document_id ORDER BY document_id), '[]'::jsonb)::jsonb AS document_ids
FROM notebook_sources
WHERE notebook_id = sqlc.arg(notebook_id) AND user_id = sqlc.arg(user_id) AND document_id IS NOT NULL;

-- name: SeedArtifactDocument :one
-- The document an artifact stands on. It is "ready" although nothing
-- indexed it: the viewers read the row, and no test searches a seeded
-- artifact.
INSERT INTO documents (user_id, title, document_type, document_metadata, content, content_hash, source_markdown, status, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(title), 'ARTIFACT', '{}'::jsonb, sqlc.arg(markdown), sqlc.arg(content_hash),
        sqlc.arg(markdown), '{"state": "ready"}'::jsonb, now())
RETURNING id;

-- name: SeedArtifact :one
INSERT INTO artifacts (document_id, user_id, format, generation, created_by_tool_call_id, updated_by_tool_call_id, metadata)
VALUES (sqlc.arg(document_id), sqlc.arg(user_id), sqlc.arg(format), 1, sqlc.arg(tool_call_id)::text, sqlc.arg(tool_call_id)::text,
        sqlc.narg(metadata)::jsonb)
RETURNING id;

-- name: LinkDocumentToArtifact :exec
UPDATE documents SET document_metadata = jsonb_build_object('artifact_id', sqlc.arg(artifact_id)::bigint)
WHERE id = sqlc.arg(document_id) AND user_id = sqlc.arg(user_id);

-- name: SeedArtifactFile :exec
INSERT INTO artifact_files (artifact_id, role, storage_backend, storage_key, original_filename, mime_type, size_bytes, checksum_sha256)
VALUES (sqlc.arg(artifact_id), sqlc.arg(role)::artifact_file_role, sqlc.arg(storage_backend), sqlc.arg(storage_key),
        sqlc.arg(original_filename), sqlc.arg(mime_type), sqlc.arg(size_bytes), sqlc.arg(checksum));

-- name: SeedPodcast :one
INSERT INTO podcast_runs (user_id, title, status, artifact_id, duration_seconds, error)
VALUES (sqlc.arg(user_id), sqlc.arg(title), sqlc.arg(status)::podcast_status, sqlc.narg(artifact_id),
        sqlc.narg(duration_seconds), sqlc.narg(error))
RETURNING id;

-- name: SeedJob :one
-- A running job is given a heartbeat a day ahead: the studio's sweep fails
-- a running job that has gone silent, and a seeded one has no process to
-- beat for it while a test looks at it.
INSERT INTO deliverable_jobs (user_id, kind, title, tool_call_id, request, status, phase, progress,
                              artifact_id, failure_code, finished_at, heartbeat_at)
VALUES (sqlc.arg(user_id), sqlc.arg(kind), sqlc.arg(title), sqlc.arg(tool_call_id)::text, sqlc.arg(request)::jsonb,
        sqlc.arg(status)::deliverable_job_status, sqlc.narg(phase), sqlc.arg(progress), sqlc.narg(artifact_id),
        sqlc.narg(failure_code), sqlc.narg(finished_at)::timestamptz, sqlc.narg(heartbeat_at)::timestamptz)
RETURNING id;

-- name: SeedOutput :one
INSERT INTO notebook_outputs (notebook_id, user_id, kind, title, options, source_document_ids,
                              podcast_id, deliverable_job_id, generated_by)
VALUES (sqlc.arg(notebook_id), sqlc.arg(user_id), sqlc.arg(kind), sqlc.arg(title), sqlc.arg(options)::jsonb,
        sqlc.arg(source_document_ids)::jsonb, sqlc.narg(podcast_id), sqlc.narg(deliverable_job_id),
        sqlc.narg(generated_by)::jsonb)
RETURNING id;

-- name: QueuedOutput :one
-- An output the app itself started and has not finished, with its job.
SELECT o.user_id, o.kind, o.title, o.deliverable_job_id
FROM notebook_outputs o
WHERE o.id = sqlc.arg(output_id) AND o.notebook_id = sqlc.arg(notebook_id) AND o.deliverable_job_id IS NOT NULL;

-- name: FinishJob :exec
UPDATE deliverable_jobs
SET status = 'ready', phase = 'done', progress = 100, artifact_id = sqlc.arg(artifact_id),
    finished_at = sqlc.arg(now)::timestamptz, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: SeedSourceDocument :one
INSERT INTO documents (user_id, title, document_type, content, content_hash, source_markdown, status, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(title), 'FILE', sqlc.arg(content), sqlc.arg(content_hash), sqlc.arg(content),
        '{"state": "ready"}'::jsonb, now())
RETURNING id;

-- name: SeedOriginalFile :exec
INSERT INTO document_files (document_id, user_id, kind, storage_backend, storage_key, original_filename,
                            mime_type, size_bytes, checksum_sha256)
VALUES (sqlc.arg(document_id), sqlc.arg(user_id), 'ORIGINAL', sqlc.arg(storage_backend), sqlc.arg(storage_key),
        sqlc.arg(original_filename), sqlc.arg(mime_type), sqlc.arg(size_bytes), sqlc.arg(checksum));

-- name: SeedChunk :one
INSERT INTO chunks (document_id, user_id, content, "position", page_start, page_end)
VALUES (sqlc.arg(document_id), sqlc.arg(user_id), sqlc.arg(content), 0, sqlc.arg(page), sqlc.arg(page))
RETURNING id;

-- name: SeedNotebookSource :exec
INSERT INTO notebook_sources (notebook_id, user_id, document_id)
VALUES (sqlc.arg(notebook_id), sqlc.arg(user_id), sqlc.arg(document_id));
