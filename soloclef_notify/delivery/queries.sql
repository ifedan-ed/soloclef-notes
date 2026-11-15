-- The statements of push delivery: which inbox rows to push and to whom,
-- the tickets Expo answered, and the phones that are gone (by Expo's word or
-- Apple's). Each read takes a whole batch of rows or accounts at once, so
-- none is asked once per row.

-- name: Pushable :many
-- The rows of a delivery, each with what its push says (metadata.push, set
-- by soloclef_notify/notify) and the collapse key of a row that stands for
-- several events ('' for one that does not), which an iPhone collapses the
-- push by. A row read since it was written is not pushed: the person has
-- seen it.
SELECT n.id, n.user_id, n.type, (n.metadata -> 'push')::jsonb AS push,
       coalesce(n.metadata ->> 'collapse_key', '')::text AS collapse_key
FROM notifications n
WHERE n.id = ANY (sqlc.arg(ids)::bigint[])
  AND NOT n.read
  AND jsonb_typeof(n.metadata -> 'push') = 'object'
ORDER BY n.id;

-- name: Preferences :many
-- The accounts' push choices. An account with no row has every kind on and
-- no quiet hours.
SELECT user_id, choices, quiet_start_minute, quiet_end_minute, time_zone
FROM push_preferences
WHERE user_id = ANY (sqlc.arg(user_ids)::uuid[]);

-- name: LiveDevices :many
-- The phones that take the accounts' pushes: those registered under a
-- sign-in that is still live, each with the kind of its token (which sender
-- pushes it) and, for an APNs token, its environment. A phone signed out is
-- removed when it is (internal/signin in the API); this is the belt to that
-- brace, so a token whose sign-in ended some other way is never woken. A
-- registration from before sign-ins were named (no family) keeps the old
-- rule and is pushed until it is removed at its own sign-out.
SELECT d.id, d.user_id, d.token, d.kind, d.apns_environment
FROM push_devices d
WHERE d.user_id = ANY (sqlc.arg(user_ids)::uuid[])
  AND (d.session_family_id IS NULL OR EXISTS (
        SELECT 1 FROM refresh_tokens r
        WHERE r.user_id = d.user_id
          AND r.family_id = d.session_family_id
          AND r.revoked_at IS NULL
          AND r.expires_at > sqlc.arg(now)::timestamptz
          AND (r.absolute_expiry IS NULL OR r.absolute_expiry > sqlc.arg(now)::timestamptz)))
ORDER BY d.user_id, d.id;

-- name: UnreadCounts :many
-- The number on each account's app icon: its unread inbox rows, counted as
-- the inbox counts them.
SELECT n.user_id, count(*)::bigint AS unread
FROM notifications n
WHERE n.user_id = ANY (sqlc.arg(user_ids)::uuid[])
  AND NOT n.read
GROUP BY n.user_id;

-- name: RecordTickets :exec
-- The tickets Expo accepted, to ask for their receipts later. The two
-- arrays are the same length and are read side by side: two set-returning
-- functions in one select list advance together.
INSERT INTO push_tickets (ticket_id, token, created_at)
SELECT unnest(sqlc.arg(ticket_ids)::text[]), unnest(sqlc.arg(tokens)::text[]), sqlc.arg(now)::timestamptz
ON CONFLICT (ticket_id) DO NOTHING;

-- name: ForgetDevices :execrows
-- Phones the push service says are gone for good (Expo's
-- DeviceNotRegistered, Apple's 410 or BadDeviceToken: the app was removed,
-- or its permission withdrawn). Named by token alone: a token is
-- one phone's and is unique, and a dead token is dead whoever registered
-- it last.
DELETE FROM push_devices WHERE token = ANY (sqlc.arg(tokens)::text[]);

-- name: DueTickets :many
-- Tickets old enough that Expo has its receipt (it asks for fifteen
-- minutes), oldest first.
SELECT ticket_id, token
FROM push_tickets
WHERE created_at <= sqlc.arg(due_before)::timestamptz
ORDER BY created_at, id
LIMIT sqlc.arg(page_size);

-- name: ForgetTickets :exec
DELETE FROM push_tickets WHERE ticket_id = ANY (sqlc.arg(ticket_ids)::text[]);

-- name: DropUnanswerableTickets :execrows
-- Tickets older than Expo keeps receipts (a day): asking would answer
-- nothing, so they are dropped rather than asked about for ever.
DELETE FROM push_tickets WHERE created_at < sqlc.arg(before)::timestamptz;

-- name: TicketsLeft :one
-- Whether any ticket is left (not due yet, or due with no receipt yet), so
-- the receipts check knows whether to come back.
SELECT EXISTS (SELECT 1 FROM push_tickets)::boolean AS left;
