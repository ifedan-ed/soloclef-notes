-- name: MarkQueueingFailed :execrows
-- A document whose indexing job could not be put on the queue. Only a
-- document still waiting is marked: if anything else has moved it since (a
-- delete, or a job another request queued), that newer state is the truth
-- and this failure is not. The reason is shown in the document list.
-- owner: through the caller. A route queues a document only after it has
-- written or read the row for its own account, and this id is that row's.
UPDATE documents
SET status = jsonb_build_object('state', 'failed', 'reason', sqlc.arg(reason)::text),
    updated_at = now()
WHERE id = sqlc.arg(id) AND status ->> 'state' = 'pending';
