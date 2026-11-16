-- Every statement names the draft by its id and its owner together, so
-- another account's draft id reads and changes nothing.

-- name: ReadDraft :one
SELECT id, request, status, replaced_by, automation_id, created_at
FROM automation_drafts
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: DiscardDraft :execrows
-- Only a draft still waiting can be discarded: a created one is an
-- automation now, and one replaced by a newer draft is already set aside.
UPDATE automation_drafts
SET status = 'discarded', updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status = 'pending';

-- name: FolderTitles :many
-- The owner's folders among the ids a draft's trigger names, for the card
-- to show by name. Another account's folder is simply not named.
SELECT id, name FROM folders
WHERE user_id = sqlc.arg(user_id) AND id = ANY(sqlc.arg(ids)::bigint[])
ORDER BY id;

-- name: CreatedAutomation :many
-- What a created draft became, as saved (the person may have changed it
-- on the card before Create): its name and its triggers, oldest first.
-- No rows when the automation was deleted since; one row with an empty
-- type when it has no trigger left. The outer cast is what tells sqlc the
-- column is text: it types a bare COALESCE as unknown (interface{}).
SELECT a.name, COALESCE(t.type::text, '')::text AS type, t.params
FROM automations AS a
LEFT JOIN automation_triggers AS t ON t.automation_id = a.id
WHERE a.id = sqlc.arg(automation_id) AND a.user_id = sqlc.arg(user_id)
ORDER BY t.id;
