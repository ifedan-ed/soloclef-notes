-- The rules that decide whether the chat's agent may run a tool outright,
-- must ask, or is refused. A rule is its account's: every statement names
-- the account beside the rule, so another account's rule is no row, exactly
-- as a rule that was never written is no row.

-- name: RulesOfOwner :many
-- The account's rules, newest first. `id` breaks ties: two rules written in
-- one instant must come back in one order on every request.
SELECT id, thread_id, permission, pattern, action, created_at
FROM agent_permission_rules
WHERE user_id = sqlc.arg(user_id)
ORDER BY created_at DESC, id DESC;

-- name: ThreadIsOwners :one
-- Whether a thread a rule is to be scoped to is the account's own. A thread
-- of another account and a thread that does not exist are the same "no".
SELECT EXISTS (
    SELECT 1 FROM new_chat_threads
    WHERE id = sqlc.arg(thread_id) AND user_id = sqlc.arg(user_id)
);

-- name: InsertRule :one
-- The database mints created_at. A second identical rule is refused by
-- uq_agent_permission_rules_scope, account-wide rules included: the
-- constraint compares NULL thread ids as equal.
INSERT INTO agent_permission_rules (user_id, thread_id, permission, pattern, action)
VALUES (sqlc.arg(user_id), sqlc.narg(thread_id), sqlc.arg(permission), sqlc.arg(pattern), sqlc.arg(action))
RETURNING id, thread_id, permission, pattern, action, created_at;

-- name: UpdateRule :one
-- Writes the fields the body mentioned and leaves the rest. A CASE per
-- column and not a COALESCE: a column set to itself is what an unmentioned
-- field means. No row answered means the account has no such rule.
UPDATE agent_permission_rules
SET pattern = CASE WHEN sqlc.arg(set_pattern)::boolean THEN sqlc.arg(pattern)::varchar ELSE pattern END,
    action  = CASE WHEN sqlc.arg(set_action)::boolean THEN sqlc.arg(action)::varchar ELSE action END
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING id, thread_id, permission, pattern, action, created_at;

-- name: DeleteRule :execrows
-- One statement finds the rule and removes it, so there is no moment between
-- a check and a delete. Zero rows means the account has no such rule.
DELETE FROM agent_permission_rules
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);
