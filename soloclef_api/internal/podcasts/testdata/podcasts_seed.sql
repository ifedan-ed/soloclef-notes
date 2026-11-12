-- The podcast routes' rows, for internal/podcasts' database test.
--
-- Loaded into a throwaway database that already has the schema and the two
-- accounts of internal/testpostgres: Alice (1111...) owns the studio's rows,
-- and Bob (2222...) owns one podcast and one thread, which Alice must never
-- be able to see, move, edit or delete -- and the other way round. The file
-- empties the tables it fills first, so it can be loaded before every case.
-- Every title, name and line of dialogue is invented.
--
-- The podcasts are one row per branch:
--
--   1300 awaiting its brief, with one: the row an approval moves.
--   1301 a finished episode with everything set -- a thread, an artifact, a
--        duration, a transcript -- which is the row a regeneration reopens.
--   1302 drafting a new take of an episode that exists: the row a revert
--        returns to `ready`.
--   1303 pending, the brief column SQL NULL.
--   1304 the brief column holding the jsonb value `null`, which is no brief
--        exactly as a SQL NULL is.
--   1305 the brief column holding `{}`: not NULL, and not a brief.
--   1306 a row from before the lifecycle rework: the brief sized in minutes
--        and the transcript a bare list of turns with the old field names.
--   1307 a brief that cannot be read: it is rendered as null, and an approval
--        is refused before anything is written.
--   1309 Bob's, awaiting its brief, and the newest brief of all: so that a
--        statement which forgot the owner would hand Alice his choices.
--   1310 cancelled, and named by a notebook output: deleting it leaves the
--        output with `podcast_id` NULL, by the foreign key and by nothing else.
--   1311 a finished episode from before briefs existed: ready, with no brief.
--   1312 awaiting_review, which nothing enters any more and a regeneration
--        still leaves.
--   1313 a brief whose keys are in jsonb's own order, with a whole number
--        stored as a decimal, a language in capitals, padded names and a
--        focus that is only spaces.
TRUNCATE podcast_runs, notebook_outputs, notebooks, artifacts, documents,
         new_chat_threads RESTART IDENTITY CASCADE;

INSERT INTO new_chat_threads (id, title, user_id, created_at)
VALUES
 (1300,'Where the episode was asked for','11111111-1111-4111-8111-111111111111','2026-07-01 00:00:00+00'),
 (1301,'A conversation of Bob''s','22222222-2222-4222-8222-222222222222','2026-07-01 00:00:00+00');

-- The delivered episode a finished podcast points at. Its audio is in file
-- storage, which none of these routes reads.
INSERT INTO documents (id, title, document_type, content, content_hash,
                       user_id, status, created_at)
VALUES
 (1300,'The harbour hour','ARTIFACT','episode notes','hash-1300',
  '11111111-1111-4111-8111-111111111111','{"state": "ready"}','2026-07-01 00:00:00+00');

INSERT INTO artifacts (id, document_id, user_id, thread_id,
                       format, generation, metadata, updated_at, created_at)
VALUES
 (1300,1300,'11111111-1111-4111-8111-111111111111',1300,'podcast',1,NULL,
  '2026-07-02 00:00:00+00','2026-07-01 00:00:00+00');

INSERT INTO podcast_runs (id, title, status, source_content, spec, spec_version,
                          podcast_transcript, duration_seconds, error,
                          artifact_id, user_id, thread_id, created_at)
VALUES
 (1300,'The harbour hour','awaiting_brief','Notes on the tide tables.',
  '{"language": "en-US", "style": "conversational", "speakers": [{"slot": 0, "name": "Mara", "role": "host", "voice_id": "kokoro:af_bella", "delivery": null}, {"slot": 1, "name": "Teo", "role": "guest", "voice_id": "kokoro:am_adam", "delivery": "dry, unhurried"}], "duration": {"min_seconds": 300, "max_seconds": 600}, "focus": "what the tide tables leave out"}',
  1,NULL,NULL,NULL,NULL,'11111111-1111-4111-8111-111111111111',NULL,'2026-07-03 00:00:00+00'),
 (1301,'Tides — “spring” <b>&</b> neap','ready','Notes on the tide tables.',
  '{"language": "pt-BR", "style": "interview", "speakers": [{"slot": 0, "name": "Mara", "role": "host", "voice_id": "kokoro:pf_dora", "delivery": null}, {"slot": 1, "name": "Teo", "role": "expert", "voice_id": "kokoro:pm_alex", "delivery": null}], "duration": {"min_seconds": 15, "max_seconds": 86400}, "focus": null}',
  4,
  '{"turns": [{"speaker": 0, "text": "Welcome back to the harbour hour."}, {"speaker": 1, "text": "Glad to be here — the tide is in.", "emotion": "warm"}]}',
  412,NULL,1300,'11111111-1111-4111-8111-111111111111',1300,'2026-07-03 00:00:01.250000+00'),
 (1302,'A second take','drafting','Notes on the tide tables.',
  '{"language": "en", "style": "monologue", "speakers": [{"slot": 0, "name": "Mara", "role": "narrator", "voice_id": "kokoro:af_bella", "delivery": null}], "duration": {"min_seconds": 20, "max_seconds": 30}, "focus": null}',
  2,
  '{"turns": [{"speaker": 0, "text": "The first take, still playable."}]}',
  95,NULL,1300,'11111111-1111-4111-8111-111111111111',NULL,'2026-07-03 00:00:02+00'),
 (1303,'Not yet briefed','pending','Notes.',NULL,1,NULL,NULL,NULL,NULL,'11111111-1111-4111-8111-111111111111',NULL,
  '2026-07-03 00:00:03+00'),
 (1304,'A brief that is json null','awaiting_brief','Notes.','null',1,NULL,NULL,NULL,NULL,
  '11111111-1111-4111-8111-111111111111',NULL,'2026-07-03 00:00:04+00'),
 (1305,'A brief that is an empty object','awaiting_brief','Notes.','{}',1,NULL,NULL,NULL,
  NULL,'11111111-1111-4111-8111-111111111111',NULL,'2026-07-03 00:00:05+00'),
 (1306,'From before the rework','failed',NULL,
  '{"language": "en", "style": "conversational", "speakers": [{"slot": 0, "name": "Mara", "role": "host", "voice_id": "kokoro:af_bella"}], "duration": {"min_minutes": 5, "max_minutes": 10}}',
  1,
  '[{"speaker_id": 0, "dialog": "An older episode, kept as it was written."}, {"speaker_id": 1, "dialog": "And it still reads."}]',
  NULL,'the speech provider answered 503 — “try later”',NULL,'11111111-1111-4111-8111-111111111111',NULL,
  '2026-07-03 00:00:06+00'),
 (1307,'A brief nobody can read','awaiting_brief','Notes.',
  '{"language": "en", "speakers": []}',1,NULL,NULL,NULL,NULL,'11111111-1111-4111-8111-111111111111',NULL,
  '2026-07-03 00:00:07+00'),
 (1309,'A podcast of Bob''s','awaiting_brief','Notes.',
  '{"language": "es", "style": "monologue", "speakers": [{"slot": 0, "name": "Mara", "role": "narrator", "voice_id": "kokoro:ef_dora", "delivery": null}], "duration": {"min_seconds": 20, "max_seconds": 30}, "focus": null}',
  1,NULL,NULL,NULL,NULL,'22222222-2222-4222-8222-222222222222',NULL,'2026-08-01 00:00:00+00'),
 (1310,'Cancelled, and still named by an output','cancelled','Notes.',NULL,1,NULL,NULL,NULL,
  NULL,'11111111-1111-4111-8111-111111111111',NULL,'2026-07-03 00:00:10+00'),
 (1311,'Finished before briefs existed','ready',NULL,NULL,1,
  '[{"speaker_id": 0, "dialog": "No brief was ever written for this one."}]',
  61,NULL,1300,'11111111-1111-4111-8111-111111111111',NULL,'2026-07-03 00:00:11+00'),
 (1312,'Waiting at a gate nothing enters now','awaiting_review','Notes.',
  '{"language": "en", "style": "monologue", "speakers": [{"slot": 0, "name": "Mara", "role": "narrator", "voice_id": "kokoro:af_bella", "delivery": null}], "duration": {"min_seconds": 20, "max_seconds": 30}, "focus": null}',
  1,NULL,NULL,NULL,NULL,'11111111-1111-4111-8111-111111111111',NULL,'2026-07-03 00:00:12+00'),
 (1313,'Numbers as jsonb keeps them','rendering','Notes.',
  '{"focus": "   ", "speakers": [{"voice_id": " kokoro:af_bella ", "slot": 1.0, "role": "host", "name": " Mara "}, {"voice_id": "kokoro:am_adam", "slot": 2, "role": "guest", "name": "Teo", "delivery": ""}], "language": "EN-us", "duration": {"max_seconds": 6.00e2, "min_seconds": 300}}',
  2147483647,NULL,0,'',1300,'11111111-1111-4111-8111-111111111111',NULL,'2026-07-03 00:00:13+00');

INSERT INTO notebooks (id, user_id, title, created_at, updated_at)
VALUES (1300,'11111111-1111-4111-8111-111111111111','A notebook with an audio overview',
        '2026-07-01 00:00:00+00','2026-07-01 00:00:00+00');

INSERT INTO notebook_outputs (id, notebook_id, user_id, kind, title, options,
                              source_document_ids, podcast_id, created_at)
VALUES (1300,1300,'11111111-1111-4111-8111-111111111111','audio_overview',
        'An overview that was cancelled','{}','[]',1310,'2026-07-01 00:00:00+00');

-- Past every id seeded above: a create case must never be handed an id a
-- seeded row holds.
SELECT setval('new_chat_threads_id_seq', 1301, true);
SELECT setval('documents_id_seq', 1300, true);
SELECT setval('artifacts_id_seq', 1300, true);
SELECT setval('podcast_runs_id_seq', 1313, true);
SELECT setval('notebooks_id_seq', 1300, true);
SELECT setval('notebook_outputs_id_seq', 1300, true);
