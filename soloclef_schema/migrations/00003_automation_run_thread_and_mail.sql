-- +goose Up

-- A run of an automation can make artifacts (a report, flashcards, a
-- podcast), and an artifact is made under a thread: its sandbox and its
-- attribution are keyed by one. So a run is given a thread of its own when
-- it starts, hidden from every list of conversations the way a studio
-- generation's is (source 'artifact_api'), and the run remembers it here.
-- Deleting the thread leaves the run and what it made.
ALTER TABLE automation_runs
    ADD COLUMN thread_id bigint REFERENCES new_chat_threads (id) ON DELETE SET NULL;
CREATE INDEX ix_automation_runs_thread_id ON automation_runs (thread_id);

-- The runs whose result is waiting to be emailed, or is being sent. The
-- worker ends a run whose automation asked for that with
-- output.email.status = 'pending'; the API tier, which holds the SMTP
-- client, looks for them every few seconds, marks one 'sending' as it
-- takes it, and writes what became of it. The index holds only those
-- rows, so the look costs the same however many runs there have been.
CREATE INDEX ix_automation_runs_email_waiting ON automation_runs (id)
    WHERE output -> 'email' ->> 'status' IN ('pending', 'sending');

-- +goose Down

DROP INDEX ix_automation_runs_email_waiting;
DROP INDEX ix_automation_runs_thread_id;
ALTER TABLE automation_runs DROP COLUMN thread_id;
