-- The statements of the caller's public links. Both name the owner.

-- name: LinksOfOwner :many
-- The caller's links, newest first; `id` breaks a tie on `created_at`, so
-- the order is total and the list never changes its own answer between two
-- requests.
--
-- `snapshot_data` is the output's frozen copy and the list reads one key of
-- it, so only that key is selected. `->` on something that is not an object
-- is NULL, and a title that is not a string is treated as none, so no stored
-- value can fail the list. "None" is the empty string: an empty title is no
-- title either, and one spelling of that is enough.
--
-- The join is on the owner as well as the id. A link is only ever made to
-- the maker's own output, and saying so in the statement means a row that
-- somehow was not would contribute nothing rather than somebody else's
-- title.
SELECT l.id, l.share_token, l.created_at, l.expires_at, l.user_id,
       COALESCE(CASE WHEN jsonb_typeof(l.snapshot_data -> 'title') = 'string'
                     THEN l.snapshot_data ->> 'title' END, '')::text AS stored_title,
       o.notebook_id, o.title AS output_title
FROM output_public_links l
JOIN notebook_outputs o ON o.id = l.notebook_output_id AND o.user_id = l.user_id
WHERE l.user_id = sqlc.arg(user_id)
ORDER BY l.created_at DESC, l.id DESC;

-- name: RevokeLinkOfOwner :execrows
-- Takes one link down, if it is the caller's. Nothing hangs off the row.
DELETE FROM output_public_links
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);
