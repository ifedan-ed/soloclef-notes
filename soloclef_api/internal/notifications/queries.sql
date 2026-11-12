-- The statements of an account's inbox. Every one names the owner.
--
-- An inbox *view* is the caller's rows narrowed by the filters a tab sends.
-- The list, the count and the clear each spell the same predicate, so that
-- "Clear all" on a tab deletes exactly the rows that tab lists. A filter left
-- out is its zero value: '' for a text, an empty array for the types.
--
-- An announcement past its expiry is in no view. The expiry is text inside
-- the metadata, so it is cast only when it reads as a timestamp: one row
-- with a malformed value must not fail the whole inbox, and CASE is the one
-- construct that promises the cast is not evaluated for it.

-- name: ListView :many
-- One page of a view, newest first, with the size of the whole view beside
-- each row so the page and its total are one statement. `before` pages
-- backwards from the oldest row a client holds.
SELECT n.id, n.user_id, n.type, n.title, n.message, n.read, n.metadata, n.created_at, n.updated_at,
       count(*) OVER () AS total
FROM notifications n
WHERE n.user_id = sqlc.arg(user_id)
  AND (n.type <> 'site_announcement' OR CASE
        WHEN (n.metadata ->> 'expires_at') ~ '^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}'
        THEN (n.metadata ->> 'expires_at')::timestamptz > sqlc.arg(now)::timestamptz
        ELSE true END)
  AND (cardinality(sqlc.arg(types)::text[]) = 0 OR n.type = ANY (sqlc.arg(types)::text[]))
  AND (sqlc.arg(source_key)::text = '' OR n.metadata ->> sqlc.arg(source_key)::text = sqlc.arg(source_value)::text)
  AND (NOT sqlc.arg(unread_only)::boolean OR NOT n.read)
  AND (NOT sqlc.arg(errors_only)::boolean OR n.metadata ->> 'status' = 'failed')
  AND (sqlc.arg(search_pattern)::text = ''
       OR n.title ILIKE sqlc.arg(search_pattern)::text OR n.message ILIKE sqlc.arg(search_pattern)::text)
  AND (sqlc.narg(before)::timestamptz IS NULL OR n.created_at < sqlc.narg(before)::timestamptz)
ORDER BY n.created_at DESC, n.id DESC
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountView :one
-- The size of a view. Asked only when a page past the first comes back
-- empty, which is the one case ListView cannot answer the total itself.
SELECT count(*)
FROM notifications n
WHERE n.user_id = sqlc.arg(user_id)
  AND (n.type <> 'site_announcement' OR CASE
        WHEN (n.metadata ->> 'expires_at') ~ '^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}'
        THEN (n.metadata ->> 'expires_at')::timestamptz > sqlc.arg(now)::timestamptz
        ELSE true END)
  AND (cardinality(sqlc.arg(types)::text[]) = 0 OR n.type = ANY (sqlc.arg(types)::text[]))
  AND (sqlc.arg(source_key)::text = '' OR n.metadata ->> sqlc.arg(source_key)::text = sqlc.arg(source_value)::text)
  AND (NOT sqlc.arg(unread_only)::boolean OR NOT n.read)
  AND (NOT sqlc.arg(errors_only)::boolean OR n.metadata ->> 'status' = 'failed')
  AND (sqlc.arg(search_pattern)::text = ''
       OR n.title ILIKE sqlc.arg(search_pattern)::text OR n.message ILIKE sqlc.arg(search_pattern)::text)
  AND (sqlc.narg(before)::timestamptz IS NULL OR n.created_at < sqlc.narg(before)::timestamptz);

-- name: ClearView :one
-- Deletes a view, but never a running job's row: the job still updates it,
-- and an update of a deleted row would fail the job. A read copy of an
-- announcement that is deleted still counts as read on the announcement
-- ("read by N of M"), so the same statement moves that count; the delete and
-- the count cannot come apart.
WITH gone AS (
    DELETE FROM notifications n
    WHERE n.user_id = sqlc.arg(user_id)
      AND (n.type <> 'site_announcement' OR CASE
            WHEN (n.metadata ->> 'expires_at') ~ '^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}'
            THEN (n.metadata ->> 'expires_at')::timestamptz > sqlc.arg(now)::timestamptz
            ELSE true END)
      AND (cardinality(sqlc.arg(types)::text[]) = 0 OR n.type = ANY (sqlc.arg(types)::text[]))
      AND (sqlc.arg(source_key)::text = '' OR n.metadata ->> sqlc.arg(source_key)::text = sqlc.arg(source_value)::text)
      AND (NOT sqlc.arg(unread_only)::boolean OR NOT n.read)
      AND (NOT sqlc.arg(errors_only)::boolean OR n.metadata ->> 'status' = 'failed')
      AND (sqlc.arg(search_pattern)::text = ''
           OR n.title ILIKE sqlc.arg(search_pattern)::text OR n.message ILIKE sqlc.arg(search_pattern)::text)
      -- 0 is every row of the view; an id is that one row of it.
      AND (sqlc.arg(only_id)::bigint = 0 OR n.id = sqlc.arg(only_id)::bigint)
      AND COALESCE(n.metadata ->> 'status', '') <> 'in_progress'
    RETURNING n.type, n.read, n.metadata ->> 'announcement_id' AS announcement
), counted AS (
    UPDATE site_announcements a
    SET cleared_read_count = a.cleared_read_count + r.copies
    FROM (
        SELECT announcement::bigint AS id, count(*) AS copies
        FROM gone
        WHERE type = 'site_announcement' AND read AND announcement ~ '^\d{1,18}$'
        GROUP BY 1
    ) r
    WHERE a.id = r.id
)
SELECT count(*) FROM gone;

-- name: IsMine :one
-- Asked only after a delete removed nothing, to tell a running job's row
-- (a 409) from one that is not the caller's or not there (a 404).
SELECT EXISTS (SELECT 1 FROM notifications WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id));

-- name: MarkRead :one
-- One guarded statement that also answers whether the row was read already.
-- The row is locked by the subquery, so two marks that race agree.
UPDATE notifications n
SET read = true,
    updated_at = CASE WHEN was.read THEN n.updated_at ELSE sqlc.arg(now)::timestamptz END
FROM (
    SELECT o.id, o.read FROM notifications o
    WHERE o.id = sqlc.arg(id) AND o.user_id = sqlc.arg(user_id)
    FOR UPDATE
) was
WHERE n.id = was.id
RETURNING was.read AS was_read;

-- name: MarkAllRead :execrows
UPDATE notifications SET read = true, updated_at = sqlc.arg(now)::timestamptz
WHERE user_id = sqlc.arg(user_id) AND NOT read;

-- name: UnreadCounts :one
-- Every unread count the inbox shows, in one pass over the caller's unread
-- rows: each tab's total, and how many of those are newer than the live
-- sync's window. `only_type` narrows all of them to one type ('' is any).
SELECT
    count(*) FILTER (WHERE n.type = ANY (sqlc.arg(status_types)::text[])) AS status_total,
    count(*) FILTER (WHERE n.type = ANY (sqlc.arg(status_types)::text[]) AND n.created_at > sqlc.arg(recent_after)::timestamptz) AS status_recent,
    count(*) FILTER (WHERE n.type = 'site_announcement') AS announcements_total,
    count(*) FILTER (WHERE n.type = 'site_announcement' AND n.created_at > sqlc.arg(recent_after)::timestamptz) AS announcements_recent,
    count(*) AS all_total,
    count(*) FILTER (WHERE n.created_at > sqlc.arg(recent_after)::timestamptz) AS all_recent
FROM notifications n
WHERE n.user_id = sqlc.arg(user_id) AND NOT n.read
  AND (sqlc.arg(only_type)::text = '' OR n.type = sqlc.arg(only_type)::text)
  AND (n.type <> 'site_announcement' OR CASE
        WHEN (n.metadata ->> 'expires_at') ~ '^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}'
        THEN (n.metadata ->> 'expires_at')::timestamptz > sqlc.arg(now)::timestamptz
        ELSE true END);

-- name: SourceTypes :many
-- The connector and document types the caller's status rows name, with how
-- many rows name each: what the Status tab's filter offers. One statement
-- and a stated order, where there were two statements and none.
SELECT (CASE WHEN n.type = 'document_processing' THEN 'document' ELSE 'connector' END)::text AS category,
       (CASE WHEN n.type = 'document_processing' THEN n.metadata ->> 'document_type'
             ELSE n.metadata ->> 'connector_type' END)::text AS source_type,
       count(*) AS rows
FROM notifications n
WHERE n.user_id = sqlc.arg(user_id)
  AND n.type IN ('connector_indexing', 'connector_deletion', 'document_processing')
  AND COALESCE(CASE WHEN n.type = 'document_processing' THEN n.metadata ->> 'document_type'
                    ELSE n.metadata ->> 'connector_type' END, '') <> ''
GROUP BY 1, 2
ORDER BY 1, 2;
