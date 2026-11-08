-- name: WorkspaceForDeletion :one
-- app/routes/workspaces_routes.py's delete_workspace loads the workspace and
-- reads its name: the marker it may already carry, and the name the marker is
-- prefixed onto.
SELECT name FROM workspaces WHERE id = $1;

-- name: OtherWorkspaceCount :one
-- app/workspace_membership/last_workspace.py's other_workspace_count:
-- workspaces the caller belongs to besides this one, not already being
-- deleted. Nobody deletes their only workspace.
SELECT count(*) FROM workspace_memberships AS membership
JOIN workspaces AS workspace ON workspace.id = membership.workspace_id
WHERE membership.user_id = $1
  AND membership.workspace_id <> $2
  AND NOT (workspace.name LIKE '[DELETING] %');

-- name: RenameWorkspace :exec
-- The soft-delete marker, and the revert for an enqueue that did not happen.
UPDATE workspaces SET name = $2 WHERE id = $1;
