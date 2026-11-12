-- The statements of the caller's public links. Both name the owner.

-- name: LinksOfOwner :many
-- The caller's links, newest first; `id` breaks a tie on `created_at`, so
-- the order is total and the list never changes its own answer between two
-- requests (the sharing panel makes several links in one burst).
--
-- `snapshot_data` is a whole frozen conversation and the list reads one key
-- of it, so only that key is selected. `->` on something that is not an
-- object is NULL, and a title that is not a string is treated as none, so no
-- stored value can fail the list. "None" is the empty string: an empty title
-- is no title either, and one spelling of that is enough.
--
-- The two joins are on the owner as well as the id. The rows they reach are
-- always the same account's (a link is made from the maker's own thread or
-- output), and saying so in the statement means a row that somehow was not
-- would contribute nothing rather than somebody else's title.
SELECT s.id, s.share_token, s.created_at, s.expires_at, s.user_id,
       s.thread_id, s.notebook_output_id,
       COALESCE(CASE WHEN jsonb_typeof(s.snapshot_data -> 'title') = 'string'
                     THEN s.snapshot_data ->> 'title' END, '')::text AS stored_title,
       cardinality(s.message_ids)::bigint AS messages,
       t.title AS thread_title,
       o.notebook_id
FROM public_chat_snapshots s
LEFT JOIN new_chat_threads t ON t.id = s.thread_id AND t.user_id = s.user_id
LEFT JOIN notebook_outputs o ON o.id = s.notebook_output_id AND o.user_id = s.user_id
WHERE s.user_id = sqlc.arg(user_id)
ORDER BY s.created_at DESC, s.id DESC;

-- name: RevokeLinkOfOwner :execrows
-- Takes one link down, if it is the caller's. Nothing hangs off the row: a
-- thread that was started from the link keeps going, with its
-- `cloned_from_snapshot_id` set to NULL by its own foreign key.
DELETE FROM public_chat_snapshots
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);
