-- name: CallerMembership :one
-- app.utils.rbac.get_user_membership with its role loaded. The pair
-- (user_id, workspace_id) is unique (uq_user_workspace_membership), which is
-- why the Python .first() needs no ordering to be the same row every time.
SELECT membership.is_owner,
       role.name AS role_name,
       COALESCE(role.permissions, '{}')::varchar[] AS role_permissions
FROM workspace_memberships AS membership
LEFT JOIN workspace_roles AS role ON role.id = membership.role_id
WHERE membership.user_id = $1 AND membership.workspace_id = $2;

-- name: WorkspaceGate :one
-- What app.utils.rbac._enforce_api_access_gate needs, plus the name the
-- my-access route reads from the row the gate already fetched.
SELECT name, api_access_enabled FROM workspaces WHERE id = $1;
