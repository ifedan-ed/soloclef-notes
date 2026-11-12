-- The site's announcements, as an administrator sends, lists and withdraws
-- them. An announcement belongs to the deployment, not to an account, so
-- these are the statements of this tier that name no owner; who may run
-- them is the first statement, asked of every request.
--
-- A delivered copy is a row of `notifications` of type 'site_announcement'
-- whose metadata names the announcement. The inbox routes own those rows
-- once they are delivered (internal/notifications).

-- name: AccountIsAdministrator :one
-- The account's own flag, which the operator sets (`soloclef-api set-admin`).
SELECT is_superuser FROM "user" WHERE id = sqlc.arg(user_id);

-- name: RecentAnnouncements :many
-- The Sent list: the newest announcements with how many recipients have
-- read each. A recipient who read a copy and then deleted it is in
-- cleared_read_count; one who still holds a read copy is counted here.
-- The copies are counted in one pass for the whole page, not once per
-- announcement.
WITH recent AS (
    SELECT id, title, body, link, created_at, expires_at, withdrawn_at, recipient_count, cleared_read_count
    FROM site_announcements
    ORDER BY created_at DESC, id DESC
    LIMIT sqlc.arg(page_size)
), held AS (
    SELECT n.metadata ->> 'announcement_id' AS announcement, count(*) AS copies
    FROM notifications n
    WHERE n.type = 'site_announcement' AND n.read
      AND n.metadata ->> 'announcement_id' IN (SELECT id::text FROM recent)
    GROUP BY 1
)
SELECT recent.id, recent.title, recent.body, recent.link, recent.created_at, recent.expires_at,
       recent.withdrawn_at, recent.recipient_count,
       (recent.cleared_read_count + COALESCE(held.copies, 0))::bigint AS read_count
FROM recent
LEFT JOIN held ON held.announcement = recent.id::text
ORDER BY recent.created_at DESC, recent.id DESC;

-- name: SendAnnouncement :one
-- The announcement and every active account's unread copy, in one
-- statement: both are there or neither is, and the recipient count is the
-- number of copies because both read the accounts in one snapshot. The
-- copy carries the link and the expiry, which is what the inbox reads to
-- stop showing it.
WITH made AS (
    INSERT INTO site_announcements (title, body, link, created_by_id, expires_at, created_at, recipient_count)
    VALUES (sqlc.arg(title), sqlc.arg(body), sqlc.narg(link), sqlc.arg(sender_id)::uuid,
            sqlc.narg(expires_at)::timestamptz, sqlc.arg(now)::timestamptz,
            (SELECT count(*) FROM "user" WHERE is_active))
    RETURNING id, title, body, link, created_at, expires_at, withdrawn_at, recipient_count
), delivered AS (
    INSERT INTO notifications (user_id, type, title, message, read, metadata, created_at, updated_at)
    SELECT u.id, 'site_announcement', made.title, made.body, false,
           jsonb_build_object('announcement_id', made.id, 'link', made.link,
                              'expires_at', sqlc.narg(expires_text)::text),
           made.created_at, made.created_at
    FROM "user" u CROSS JOIN made
    WHERE u.is_active
)
SELECT id, title, body, link, created_at, expires_at, withdrawn_at, recipient_count FROM made;

-- name: WithdrawAnnouncement :one
-- Takes the announcement out of every inbox and marks it withdrawn; the
-- record stays. A second withdrawal keeps the first one's time. The read
-- copies it removes are added to cleared_read_count in the same statement,
-- so "read by N of M" does not fall to nothing the moment an announcement
-- is withdrawn. No row back means there is no such announcement.
WITH gone AS (
    DELETE FROM notifications n
    WHERE n.type = 'site_announcement'
      AND n.metadata ->> 'announcement_id' = sqlc.arg(id)::bigint::text
    RETURNING n.read
)
UPDATE site_announcements a
SET withdrawn_at = COALESCE(a.withdrawn_at, sqlc.arg(now)::timestamptz),
    cleared_read_count = a.cleared_read_count + (SELECT count(*) FROM gone WHERE read)
WHERE a.id = sqlc.arg(id)::bigint
RETURNING a.id, a.title, a.body, a.link, a.created_at, a.expires_at, a.withdrawn_at, a.recipient_count,
          a.cleared_read_count AS read_count;
