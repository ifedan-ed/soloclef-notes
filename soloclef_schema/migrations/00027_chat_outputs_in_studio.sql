-- +goose Up

-- What a notebook's chat makes is listed in that notebook's Studio.
--
-- Until now the Studio listed only what its own buttons started: a row of
-- notebook_outputs pointing at the run that made it, a podcast or a
-- deliverable job. An artifact the chat made (a deck, a report, a quiz) has
-- no run of its own, so it lived only in the chat, and a person who asked
-- the chat for a quiz looked for it in the Studio and did not find it. A
-- video or podcast the chat queued has a run, but no output pointed at it.
--
-- An output may now point at the artifact itself, for what a chat made with
-- no run behind it. It points at one of the three or at none, never at two:
-- an output made by a run reaches its artifact through the run.
ALTER TABLE notebook_outputs ADD COLUMN artifact_id bigint;

-- The pair (artifact, owner) is the reference, so an output can only ever
-- name an artifact of its own account: the database refuses anything else.
-- artifacts.id is already unique; the pair is declared unique only so that
-- it can be referred to.
ALTER TABLE artifacts ADD CONSTRAINT uq_artifacts_id_owner UNIQUE (id, user_id);
-- An artifact deleted (from the chat or its document from the library)
-- takes its output with it: an output with nothing to open is not a thing
-- to list.
ALTER TABLE notebook_outputs
    ADD CONSTRAINT notebook_outputs_artifact_owner_fkey FOREIGN KEY (artifact_id, user_id)
        REFERENCES artifacts (id, user_id) ON DELETE CASCADE;

ALTER TABLE notebook_outputs DROP CONSTRAINT ck_notebook_outputs_one_run;
ALTER TABLE notebook_outputs
    ADD CONSTRAINT ck_notebook_outputs_one_run CHECK (num_nonnulls(podcast_id, deliverable_job_id, artifact_id) <= 1);

-- One output per artifact; also the index the foreign key's cascade and the
-- backfill's "already listed" check use.
CREATE UNIQUE INDEX uq_notebook_outputs_artifact ON notebook_outputs (artifact_id) WHERE artifact_id IS NOT NULL;

-- The backfill: what notebook chats made before today, listed now. A chat of
-- a notebook is a thread with notebook_id set that is not one of the hidden
-- threads a Studio generation or an automation's run is made in (source
-- 'artifact_api'): a Studio generation's output is listed already, by its
-- own row. Every join names the owner, so a row can only list what its own
-- account made in its own notebook. Each statement inserts only what is not
-- listed yet, so running the migration again adds nothing.
--
-- The kinds are the Studio's names for the formats, as
-- soloclef_studio/notebookoutput.KindOf maps them on the day this was
-- written; a migration is frozen, so they are spelled out here. A picture
-- drawn in the chat (format 'image') is not one of the Studio's kinds and
-- stays in the chat.

-- Artifacts made in a notebook's chat with no run behind them. An artifact a
-- run made (a video, a podcast's audio) is listed through its run, below.
INSERT INTO notebook_outputs (notebook_id, user_id, kind, title, options, artifact_id, thread_id, created_at)
SELECT t.notebook_id, a.user_id, listed.kind,
       left(COALESCE(NULLIF(btrim(d.title), ''), 'Untitled'), 200),
       jsonb_build_object('kind', listed.kind) || listed.options,
       a.id, t.id, a.created_at
FROM artifacts a
JOIN new_chat_threads t ON t.id = a.thread_id AND t.user_id = a.user_id
JOIN notebooks n ON n.id = t.notebook_id AND n.user_id = a.user_id
JOIN documents d ON d.id = a.document_id AND d.user_id = a.user_id
JOIN (VALUES ('flashcards', 'flashcards', '{}'::jsonb), ('quiz', 'quiz', '{}'), ('mindmap', 'mindmap', '{}'),
             ('infographic', 'infographic', '{}'), ('pptx', 'slides', '{}'), ('xlsx', 'data_table', '{}'),
             ('pdf', 'report', '{"format": "document", "file_type": "pdf"}'),
             ('docx', 'report', '{"format": "document", "file_type": "docx"}'),
             ('html', 'report', '{"format": "interactive"}')) AS listed (format, kind, options)
  ON listed.format = a.format
WHERE t.source <> 'artifact_api'
  AND NOT EXISTS (SELECT 1 FROM notebook_outputs o WHERE o.artifact_id = a.id)
  AND NOT EXISTS (SELECT 1 FROM deliverable_jobs j WHERE j.artifact_id = a.id AND j.user_id = a.user_id)
  AND NOT EXISTS (SELECT 1 FROM podcast_runs p WHERE p.artifact_id = a.id AND p.user_id = a.user_id);

-- Videos queued in a notebook's chat.
INSERT INTO notebook_outputs (notebook_id, user_id, kind, title, options, deliverable_job_id, thread_id, created_at)
SELECT t.notebook_id, j.user_id, 'video', left(COALESCE(NULLIF(btrim(j.title), ''), 'Video'), 200), '{"kind": "video"}', j.id, t.id, j.created_at
FROM deliverable_jobs j
JOIN new_chat_threads t ON t.id = j.thread_id AND t.user_id = j.user_id
JOIN notebooks n ON n.id = t.notebook_id AND n.user_id = j.user_id
WHERE t.source <> 'artifact_api' AND j.kind = 'video'
  AND NOT EXISTS (SELECT 1 FROM notebook_outputs o WHERE o.deliverable_job_id = j.id);

-- Podcasts opened in a notebook's chat.
INSERT INTO notebook_outputs (notebook_id, user_id, kind, title, options, podcast_id, thread_id, created_at)
SELECT t.notebook_id, p.user_id, 'audio', left(COALESCE(NULLIF(btrim(p.title), ''), 'Podcast'), 200), '{"kind": "audio"}', p.id, t.id, p.created_at
FROM podcast_runs p
JOIN new_chat_threads t ON t.id = p.thread_id AND t.user_id = p.user_id
JOIN notebooks n ON n.id = t.notebook_id AND n.user_id = p.user_id
WHERE t.source <> 'artifact_api'
  AND NOT EXISTS (SELECT 1 FROM notebook_outputs o WHERE o.podcast_id = p.id);

-- +goose Down

-- The listings of artifacts go with the column; the artifacts themselves
-- stay, in their chats. A video's or a podcast's listing stays: it is an
-- output the schema before this one can hold.
DELETE FROM notebook_outputs WHERE artifact_id IS NOT NULL;
DROP INDEX uq_notebook_outputs_artifact;
ALTER TABLE notebook_outputs DROP CONSTRAINT ck_notebook_outputs_one_run;
ALTER TABLE notebook_outputs
    ADD CONSTRAINT ck_notebook_outputs_one_run CHECK (num_nonnulls(podcast_id, deliverable_job_id) <= 1);
ALTER TABLE notebook_outputs DROP CONSTRAINT notebook_outputs_artifact_owner_fkey;
ALTER TABLE artifacts DROP CONSTRAINT uq_artifacts_id_owner;
ALTER TABLE notebook_outputs DROP COLUMN artifact_id;
