-- The runs whose result is to be emailed to their owner, as the sender
-- takes and settles them. `output.email` is the whole record: status
-- pending (the worker's), sending (taken here), then sent or not_sent.
-- Every statement that changes one names the status it expects, so two
-- senders, or a sender and a clock, never both act on a run.

-- name: ClaimPendingMail :many
-- Takes up to a batch of waiting runs for this sender, oldest first, and
-- answers what each one's mail needs. SKIP LOCKED: a run another replica
-- is taking this instant is left to it. The owner's address is read
-- through the run's own automation, never from the run's row.
UPDATE automation_runs AS run
SET output = jsonb_set(run.output, '{email}',
        (run.output -> 'email') || jsonb_build_object('status', 'sending', 'claimed_at', to_jsonb(now())))
FROM automations AS automation, "user" AS owner
WHERE run.id IN (
        SELECT waiting.id FROM automation_runs AS waiting
        WHERE waiting.output -> 'email' ->> 'status' = 'pending'
        ORDER BY waiting.id
        LIMIT sqlc.arg(batch)
        FOR UPDATE SKIP LOCKED)
  AND automation.id = run.automation_id
  AND owner.id = automation.user_id
RETURNING run.id, automation.id AS automation_id, automation.name, owner.email, owner.is_active,
          run.status::text AS status,
          COALESCE(run.output ->> 'final_message', '')::text AS final_message,
          COALESCE(run.error ->> 'message', '')::text AS error_message,
          COALESCE((run.output -> 'email' ->> 'attempts')::int, 0)::int AS attempts;

-- name: SettleMail :execrows
-- Writes what became of a run's mail, over a run this sender took.
UPDATE automation_runs
SET output = jsonb_set(output, '{email}', sqlc.arg(email)::jsonb)
WHERE id = sqlc.arg(id) AND output -> 'email' ->> 'status' = 'sending';

-- name: ReleaseMail :execrows
-- Puts a run back to wait after a send the mail server could not take
-- just now, counting the attempt.
UPDATE automation_runs
SET output = jsonb_set(output, '{email}',
        jsonb_build_object('status', 'pending', 'attempts', sqlc.arg(attempts)::int))
WHERE id = sqlc.arg(id) AND output -> 'email' ->> 'status' = 'sending';

-- name: AbandonStaleMail :execrows
-- A run taken and never settled: the process that took it stopped between
-- taking it and writing the outcome. It may or may not have been sent, so
-- it is not sent again; it is said.
UPDATE automation_runs
SET output = jsonb_set(output, '{email}', sqlc.arg(email)::jsonb)
WHERE output -> 'email' ->> 'status' = 'sending'
  AND (output -> 'email' ->> 'claimed_at')::timestamptz < sqlc.arg(before)::timestamptz;
