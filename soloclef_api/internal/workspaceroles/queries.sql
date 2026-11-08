-- name: RolesInWorkspace :many
-- app/routes/rbac_routes.py:list_roles — every role of one workspace.
-- Deliberately unordered: the Python route's query has no ORDER BY either, so
-- the order of the array is Postgres' and the two tiers agree only while both
-- ask the same question. Adding an ORDER BY here would be a different answer in
-- a different order (custom/docs/go-api-tier.md, "The array order").
SELECT name,
       description,
       permissions::varchar[] AS permissions,
       is_default,
       id,
       workspace_id,
       is_system_role,
       created_at
FROM workspace_roles
WHERE workspace_id = $1;

-- name: RoleInWorkspace :one
-- app/routes/rbac_routes.py:get_role — one role, scoped to the workspace so a
-- role of another workspace is "Role not found" rather than readable.
SELECT name,
       description,
       permissions::varchar[] AS permissions,
       is_default,
       id,
       workspace_id,
       is_system_role,
       created_at
FROM workspace_roles
WHERE id = $1 AND workspace_id = $2;

-- name: RoleNameTaken :one
-- The 409 check in create_role, which asks only whether a row exists. The
-- comparison is exact, as the Python filter is: a role named "viewer" may be
-- created beside "Viewer", and the unique constraint agrees.
SELECT EXISTS (
    SELECT 1 FROM workspace_roles
    WHERE workspace_id = $1 AND name = $2
) AS taken;

-- name: ClearDefaultRole :exec
-- create_role's "unset any existing default". The Python route selects the
-- current defaults and assigns False to each, which the ORM flushes as one
-- UPDATE per row by primary key; this writes the same rows in one statement.
-- Nothing can observe the difference: the predicate is the same, the rows are
-- the same, and both run inside the one transaction that also inserts the new
-- role.
UPDATE workspace_roles
SET is_default = false
WHERE workspace_id = $1 AND is_default;

-- name: CreateRole :one
-- The insert, returning the row RoleRead renders. created_at is passed in
-- rather than defaulted in SQL because the Python model's default is
-- datetime.now(UTC) evaluated in the application, not a server default.
INSERT INTO workspace_roles (name, description, permissions, is_default,
                             is_system_role, workspace_id, created_at)
VALUES (@name, @description, @permissions::varchar[], @is_default, false, @workspace_id, @created_at)
RETURNING name,
          description,
          permissions::varchar[] AS permissions,
          is_default,
          id,
          workspace_id,
          is_system_role,
          created_at;

-- name: RoleNameTakenElsewhere :one
-- update_role's 409 check. The Python route looks for the name in the
-- workspace without excluding the role being renamed, and only asks at all
-- when the new name differs from the role's current one — so the role itself
-- can never be the row it finds. Mirrored as asked rather than tidied into an
-- "id <> $3", which would be a different question.
SELECT EXISTS (
    SELECT 1 FROM workspace_roles
    WHERE workspace_id = @workspace_id AND name = sqlc.narg(name)
) AS taken;

-- name: UpdateRole :one
-- update_role's write. The Python route assigns only the fields the body
-- mentioned (model_dump(exclude_unset=True)) and the ORM flushes an UPDATE for
-- those columns; each CASE here is one of those decisions, so a field the body
-- did not mention keeps its stored value and an explicit null is written as
-- NULL — which is a NOT NULL violation and a 500 on both tiers, deliberately.
UPDATE workspace_roles
SET name = CASE WHEN @set_name::boolean THEN sqlc.narg(name)::varchar ELSE name END,
    description = CASE WHEN @set_description::boolean THEN sqlc.narg(description)::varchar ELSE description END,
    permissions = CASE WHEN @set_permissions::boolean THEN sqlc.narg(permissions)::varchar[] ELSE permissions END,
    is_default = CASE WHEN @set_is_default::boolean THEN sqlc.narg(is_default)::boolean ELSE is_default END
WHERE id = @id AND workspace_id = @workspace_id
RETURNING name,
          description,
          permissions::varchar[] AS permissions,
          is_default,
          id,
          workspace_id,
          is_system_role,
          created_at;

-- name: DeleteRole :exec
-- delete_role's write, scoped to the workspace exactly as the SELECT that
-- loaded the row was. A role still pointed at by a membership fails the
-- foreign key here, which is the 500 the Python route's flush gives.
DELETE FROM workspace_roles WHERE id = $1 AND workspace_id = $2;

-- name: ClearOtherDefaultRoles :exec
-- update_role's "unset existing default", which it runs only when the body
-- asks for is_default and the role is not already the default. The Python
-- route selects every default role of the workspace and assigns False to each,
-- including the role being updated when it is one — which it cannot be, since
-- the branch is guarded by `not db_role.is_default`.
UPDATE workspace_roles
SET is_default = false
WHERE workspace_id = $1 AND is_default;
