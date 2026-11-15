-- +goose Up

-- Copies of a person's text that outlived the work they were kept for are
-- removed. From this version the code removes each as its work ends; this
-- removes the ones left before it did.

-- A Studio job's stored task (`request -> 'task'`) is a copy of the
-- person's sources and words, kept only while the job may still have to be
-- made (soloclef_api/internal/studiogenerate, stored_task.go). A job that
-- has ended -- cancelled, failed or made -- is never made again from it, but
-- a cancel of a queued job used to leave it in place.
UPDATE deliverable_jobs SET request = request - 'task'
WHERE request ? 'task' AND status IN ('cancelled', 'failed', 'ready');

-- The chat loop's saved conversations (soloclef_agent_checkpoints) are
-- named, not owned: no foreign key takes them with the row they belong to,
-- so each family below is matched by its exact name shape and kept while
-- its owner can still read it. Each delete reads a prefix range first
-- (`~>=~` and `~<~`, which the table's text_pattern_ops index serves;
-- '-' is followed by '.' in byte order, so [x-, x.) is every name that
-- begins "x-"), then the exact shape, so a name of any other kind is never
-- touched.

-- A Studio run's conversation, "studio-job-<job>-attempt-<n>" and the
-- specialists' threads beneath it ("…/<call>"): only the attempt a job is
-- running now is ever read again (studiogenerate, attempt.go). A run that
-- died on its last attempt, a queued job that was cancelled after an
-- earlier attempt was cut off, and a job deleted with its account all left
-- theirs.
DELETE FROM soloclef_agent_checkpoints AS c
WHERE c.thread_id ~>=~ 'studio-job-' AND c.thread_id ~<~ 'studio-job.'
  AND c.thread_id ~ '^studio-job-[0-9]{1,18}-attempt-[0-9]{1,9}(/|$)'
  AND NOT EXISTS (
      SELECT 1 FROM deliverable_jobs AS j
      WHERE j.kind = 'artifact' AND j.status IN ('running', 'cancelling')
        AND substring(c.thread_id FROM '^(studio-job-[0-9]+-attempt-[0-9]+)') = 'studio-job-' || j.id || '-attempt-' || j.attempt_count);

-- A chat's conversation, "chat-<thread>", the specialists' threads beneath
-- it, and the turn it keeps beside it ("chat-<thread>.turn",
-- chat/history.go). Deleting a thread removed the first two and not the
-- third, and erasing an account removed none: its threads went with the
-- account and their conversations, every message in them, stayed.
DELETE FROM soloclef_agent_checkpoints AS c
WHERE c.thread_id ~>=~ 'chat-' AND c.thread_id ~<~ 'chat.'
  AND c.thread_id ~ '^chat-[0-9]{1,18}(/|\.turn$|$)'
  AND NOT EXISTS (
      SELECT 1 FROM new_chat_threads AS t
      WHERE t.id = substring(c.thread_id FROM '^chat-([0-9]+)')::bigint);

-- An automation run's conversation, "automation-run-<run>-<step>-<attempt>"
-- (soloclef_worker/internal/automationrun): its run removes it when the
-- step ends, so one is left only by a worker that died, or by an account
-- erased while a run was going. Kept while its run is pending or running.
DELETE FROM soloclef_agent_checkpoints AS c
WHERE c.thread_id ~>=~ 'automation-run-' AND c.thread_id ~<~ 'automation-run.'
  AND c.thread_id ~ '^automation-run-[0-9]{1,18}-[0-9]{1,9}-[0-9]{1,9}(/|$)'
  AND NOT EXISTS (
      SELECT 1 FROM automation_runs AS r
      WHERE r.status IN ('pending', 'running')
        AND r.id = substring(c.thread_id FROM '^automation-run-([0-9]+)-')::bigint);

-- +goose Down

-- Nothing to put back: what was removed was left over, and is not restored.
