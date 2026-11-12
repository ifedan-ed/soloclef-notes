-- The one statement of the notebook copy.

-- name: CopyNotebook :one
-- A copy of one of the caller's notebooks, with its source links, made and
-- described by one statement, so a copy is never left half-made and nothing
-- is read back. `original` is what confines it to the caller: another
-- account's notebook gives it no row, so nothing is written and no row is
-- answered.
--
-- The copy's title is the one asked for, or the original's with " (copy)"
-- when none was; either is cut to the column's 200 characters. A person
-- asked for the copy, so the title and icon are theirs ('user') and the
-- copy never renames itself. The sources keep the order they were added in
-- (`added_at` is carried over) and whether each was ticked.
WITH original AS (
    SELECT o.title, o.icon, o.chat_goal, o.chat_instructions, o.response_length
    FROM notebooks o
    WHERE o.id = sqlc.arg(notebook_id) AND o.user_id = sqlc.arg(user_id)
), made AS (
    INSERT INTO notebooks (user_id, title, icon, title_source, icon_source,
                           chat_goal, chat_instructions, response_length, created_at, updated_at)
    SELECT sqlc.arg(user_id),
           left(CASE WHEN sqlc.arg(title)::text = '' THEN original.title || ' (copy)'
                     ELSE sqlc.arg(title)::text END, 200),
           original.icon, 'user', 'user',
           original.chat_goal, original.chat_instructions, original.response_length,
           sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz
    FROM original
    RETURNING id, title, icon, created_at, updated_at, chat_goal, chat_instructions, response_length
), linked AS (
    INSERT INTO notebook_sources (notebook_id, user_id, document_id, folder_id, enabled, added_at)
    SELECT made.id, s.user_id, s.document_id, s.folder_id, s.enabled, s.added_at
    FROM notebook_sources s, made
    WHERE s.notebook_id = sqlc.arg(notebook_id) AND s.user_id = sqlc.arg(user_id)
    RETURNING enabled
)
SELECT made.id, made.title, made.icon, made.created_at, made.updated_at,
       made.chat_goal, made.chat_instructions, made.response_length,
       (SELECT count(*) FROM linked)::bigint AS source_count,
       (SELECT count(*) FROM linked WHERE linked.enabled)::bigint AS enabled_source_count
FROM made;
