-- name: DocumentForDeletion :one
-- app/routes/documents_routes.py's delete_document loads the document row and
-- uses two of its columns: the workspace the permission is checked against,
-- and the status its three refusals are decided from.
SELECT workspace_id, status FROM documents WHERE id = $1;

-- name: MarkDocumentDeleting :exec
-- `document.status = {"state": "deleting"}` replaces the column outright, as
-- there: the marker is what keeps a document being deleted out of search.
UPDATE documents SET status = '{"state": "deleting"}'::jsonb WHERE id = $1;

-- name: MarkDocumentReady :exec
-- The revert for an enqueue that did not happen. It writes "ready" whatever
-- the state was before, exactly as the Python route's own revert does: a
-- document left in "deleting" with no task to delete it is stuck forever.
UPDATE documents SET status = '{"state": "ready"}'::jsonb WHERE id = $1;
