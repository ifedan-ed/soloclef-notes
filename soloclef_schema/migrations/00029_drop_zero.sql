-- +goose Up

-- Rocicorp Zero is gone. The web app and the phone no longer
-- follow Postgres by logical replication: every tier tells a person's open
-- screens what changed over Valkey, and the API streams it to them
-- (soloclef_notify/livechange, soloclef_api/internal/livechanges). Nothing
-- reads the publication any more, so it goes, and with it everything
-- zero-cache made for itself in this database when it started:
--
--   - its DDL event triggers (zero_ddl_start_0, zero_ddl_end_0), which fire
--     on every later migration's every statement and call functions in its
--     own schemas;
--   - its own publication (_zero_metadata_0);
--   - its logical replication slot (zero_0_*). A slot left behind holds
--     WAL forever, and a Postgres started without wal_level=logical (the
--     setting zero-cache needed, removed from the Compose file with this
--     change) refuses to start while one exists. So it must go before that
--     restart: this migration runs before Postgres is restarted without it;
--   - its schemas: zero, zero_0, "zero_0/cdc" and "zero_0/cvr" (its change
--     log and what it had sent each client).
--
-- Each is found by the names zero-cache 1.6 gives them under its default
-- app id ("zero") and shard (0), which is how it was run here, and dropped
-- only if it exists: a database zero-cache never started on (a new one, a
-- test) has the publication and nothing else.
--
-- The event triggers go first, so that the drops after them do not call
-- zero-cache's functions on their way.

-- +goose StatementBegin
DO $$
DECLARE
    found record;
BEGIN
    FOR found IN SELECT evtname FROM pg_event_trigger WHERE evtname ~ '^zero_ddl_(start|end)_[0-9]+$' LOOP
        EXECUTE format('DROP EVENT TRIGGER %I', found.evtname);
    END LOOP;

    DROP PUBLICATION IF EXISTS zero_publication;
    FOR found IN SELECT pubname FROM pg_publication WHERE pubname ~ '^_zero_metadata_[0-9]+$' LOOP
        EXECUTE format('DROP PUBLICATION %I', found.pubname);
    END LOOP;

    -- A slot still in use is a zero-cache still running against this
    -- database (the deploy stops it first). Its connection is ended rather
    -- than the migration failed: the slot serves nothing after this change,
    -- and a zero-cache that reconnects finds no publication to follow.
    FOR found IN
        SELECT slot_name, active_pid FROM pg_replication_slots
        WHERE slot_type = 'logical' AND database = current_database() AND slot_name ~ '^zero_[0-9]+_'
    LOOP
        IF found.active_pid IS NOT NULL THEN
            PERFORM pg_terminate_backend(found.active_pid);
            -- The slot is released when the walsender has gone; wait for it.
            FOR attempt IN 1..50 LOOP
                EXIT WHEN NOT EXISTS (
                    SELECT 1 FROM pg_replication_slots WHERE slot_name = found.slot_name AND active);
                PERFORM pg_sleep(0.1);
            END LOOP;
        END IF;
        PERFORM pg_drop_replication_slot(found.slot_name);
    END LOOP;

    FOR found IN SELECT nspname FROM pg_namespace WHERE nspname ~ '^zero(_[0-9]+(/(cdc|cvr))?)?$' LOOP
        EXECUTE format('DROP SCHEMA %I CASCADE', found.nspname);
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down

-- The publication as 00001 made it, so a rollback to Zero has something to
-- follow. zero-cache makes its own triggers, slot and schemas again when it
-- starts; nothing of its old state is wanted back.
CREATE PUBLICATION zero_publication FOR TABLE
    notifications,
    folders,
    documents (id, user_id, title, document_type, folder_id, status, created_at, updated_at),
    search_source_connectors (id, user_id, name, connector_type, enabled, is_indexable,
        last_indexed_at, periodic_indexing_enabled, indexing_frequency_minutes,
        next_scheduled_at, created_at),
    new_chat_threads (id, user_id),
    new_chat_messages,
    chat_session_state,
    automations (id, user_id),
    automation_runs (id, automation_id, trigger_id, status, step_results, started_at,
        finished_at, created_at),
    podcast_runs (id, user_id, title, status, spec, spec_version, duration_seconds, error,
        artifact_id, thread_id, created_at),
    video_presentation_runs (id, user_id, title, status, error, artifact_id, thread_id, created_at),
    deliverable_jobs (id, user_id, kind, title, status, phase, progress, failure_code,
        artifact_id, thread_id, created_at, updated_at);
