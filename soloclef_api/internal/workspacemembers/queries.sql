-- name: Memberships :many
-- The Python route's first query, with no ORDER BY, because it has none: the
-- array's order is the order Postgres returns rows in for this filter, and
-- asking a different question here would answer in a different order.
SELECT id, user_id, workspace_id, role_id, is_owner, joined_at, created_at
FROM workspace_memberships
WHERE workspace_id = $1;

-- name: RolesByID :many
-- selectinload(WorkspaceMembership.role)'s own query.
SELECT id, name, description, permissions, is_default, is_system_role, workspace_id, created_at
FROM workspace_roles
WHERE id = ANY(sqlc.arg(role_ids)::integer[]);

-- name: AccountsByID :many
-- The account lookup the Python route runs once per membership. One query
-- instead of N: the rows are joined back by id, so their order is not read.
SELECT id, email, display_name, avatar_url, last_login
FROM "user"
WHERE id = ANY(sqlc.arg(account_ids)::uuid[]);
