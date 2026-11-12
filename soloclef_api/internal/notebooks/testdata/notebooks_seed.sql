-- The rows the notebooks database test is asked about, loaded into a
-- throwaway database that already has the schema and the two accounts of
-- internal/testpostgres: Alice (1111...) and Bob (2222...). The file empties
-- the tables it fills first, so it is loaded before every case.
--
-- Every name and title is invented: this repository has a public copy.
--
-- "The moment" below is 2026-06-15 12:00:00+00, the instant the test's clock
-- stands at (routes_test.go). A notebook is "about to name itself" relative
-- to that instant, so the rows that decide `title_pending` are dated against
-- it rather than against the wall clock.
TRUNCATE notebooks, documents, notebook_report_templates, new_chat_threads
         RESTART IDENTITY CASCADE;

-- Alice's notebooks.
--   1100 named by her, pinned, with chat settings and three sources (two
--        ticked): the row the list's shape is checked against.
--   1101 and 1102 share an `updated_at`: the id decides their order.
--   1110-1120 one per branch of `title_pending` (below).
-- Bob's notebook, 1190, is the newest of all and pinned: a statement that
-- forgot the owner would put it at the top of Alice's list.
INSERT INTO notebooks (id, user_id, title, icon, title_source, icon_source, auto_title,
                       chat_goal, chat_instructions, response_length, featured_at,
                       created_at, updated_at)
VALUES
 (1100,'11111111-1111-4111-8111-111111111111','Harbour study','📓','user','user',NULL,
  'learning_guide','Answer briefly.','longer','2026-06-03 00:00:00+00',
  '2026-06-02 08:00:00+00','2026-06-10 09:30:00.250000+00'),
 (1101,'11111111-1111-4111-8111-111111111111','Tide tables',NULL,'user','user',NULL,
  'default',NULL,'default',NULL,'2026-06-02 08:00:00+00','2026-06-09 00:00:00+00'),
 (1102,'11111111-1111-4111-8111-111111111111','Lighthouse notes',NULL,'user','user',NULL,
  'default',NULL,'default',NULL,'2026-06-02 08:00:00+00','2026-06-09 00:00:00+00'),
 -- title_pending, one notebook per branch:
 --   1110 never named, a source pending and added ten minutes before   -> pending
 --   1111 never named, a source pending but added 31 minutes before    -> not
 --   1112 never named, a source ready one minute before                -> pending
 --   1113 never named, a source ready three minutes before             -> not
 --   1115 named once ten minutes before, a source ready since          -> pending
 --   1116 named once ten minutes before, its source ready before that  -> not
 --   1118 named once 31 minutes before, a source ready a minute before -> not
 --   1119 named twice, a source ready a minute before                  -> not
 --   1120 named by its user, with a pending source                     -> not asked
 --   1121 named once with a time nobody can read, a pending source     -> not, and
 --        the list is still answered
 (1110,'11111111-1111-4111-8111-111111111111','Untitled notebook',NULL,'auto','auto',NULL,
  'default',NULL,'default',NULL,'2026-06-01 00:00:00+00','2026-06-01 00:00:10+00'),
 (1111,'11111111-1111-4111-8111-111111111111','Untitled notebook',NULL,'auto','auto',NULL,
  'default',NULL,'default',NULL,'2026-06-01 00:00:00+00','2026-06-01 00:00:11+00'),
 (1112,'11111111-1111-4111-8111-111111111111','Untitled notebook',NULL,'auto','auto',NULL,
  'default',NULL,'default',NULL,'2026-06-01 00:00:00+00','2026-06-01 00:00:12+00'),
 (1113,'11111111-1111-4111-8111-111111111111','Untitled notebook',NULL,'auto','auto',NULL,
  'default',NULL,'default',NULL,'2026-06-01 00:00:00+00','2026-06-01 00:00:13+00'),
 (1115,'11111111-1111-4111-8111-111111111111','A first name',NULL,'auto','auto',
  '{"runs": 1, "at": "2026-06-15T11:50:00+00:00"}',
  'default',NULL,'default',NULL,'2026-06-01 00:00:00+00','2026-06-01 00:00:15+00'),
 (1116,'11111111-1111-4111-8111-111111111111','A first name',NULL,'auto','auto',
  '{"runs": 1, "at": "2026-06-15T11:50:00+00:00"}',
  'default',NULL,'default',NULL,'2026-06-01 00:00:00+00','2026-06-01 00:00:16+00'),
 (1118,'11111111-1111-4111-8111-111111111111','A first name',NULL,'auto','auto',
  '{"runs": 1, "at": "2026-06-15T11:29:00+00:00"}',
  'default',NULL,'default',NULL,'2026-06-01 00:00:00+00','2026-06-01 00:00:18+00'),
 (1119,'11111111-1111-4111-8111-111111111111','A second name',NULL,'auto','auto',
  '{"runs": 2, "at": "2026-06-15T11:55:00+00:00"}',
  'default',NULL,'default',NULL,'2026-06-01 00:00:00+00','2026-06-01 00:00:19+00'),
 (1120,'11111111-1111-4111-8111-111111111111','Named by hand',NULL,'user','user',NULL,
  'default',NULL,'default',NULL,'2026-06-01 00:00:00+00','2026-06-01 00:00:20+00'),
 (1121,'11111111-1111-4111-8111-111111111111','A first name',NULL,'auto','auto',
  '{"runs": 1, "at": "last Tuesday"}',
  'default',NULL,'default',NULL,'2026-06-01 00:00:00+00','2026-06-01 00:00:21+00'),
 (1190,'22222222-2222-4222-8222-222222222222','Bob''s notebook','🧭','user','user',NULL,
  'default',NULL,'default','2026-06-14 00:00:00+00','2026-06-14 00:00:00+00','2026-06-14 00:00:00+00');

-- The documents the notebooks read from. `updated_at` is when a document
-- became ready, which is what "ready since" compares.
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status,
                       created_at, updated_at)
VALUES
 (1100,'11111111-1111-4111-8111-111111111111','Harbour chart','FILE','','nb-hash-1100','{"state": "ready"}','2026-06-01 00:00:00+00','2026-06-01 00:00:00+00'),
 (1101,'11111111-1111-4111-8111-111111111111','Harbour survey','FILE','','nb-hash-1101','{"state": "ready"}','2026-06-01 00:00:01+00','2026-06-01 00:00:01+00'),
 (1102,'11111111-1111-4111-8111-111111111111','Harbour log','FILE','','nb-hash-1102','{"state": "ready"}','2026-06-01 00:00:02+00','2026-06-01 00:00:02+00'),
 (1110,'11111111-1111-4111-8111-111111111111','Still indexing','FILE','','nb-hash-1110','{"state": "pending"}','2026-06-15 11:50:00+00','2026-06-15 11:50:00+00'),
 (1111,'11111111-1111-4111-8111-111111111111','Stuck indexing','FILE','','nb-hash-1111','{"state": "processing"}','2026-06-15 11:29:00+00','2026-06-15 11:29:00+00'),
 (1112,'11111111-1111-4111-8111-111111111111','Ready a minute ago','FILE','','nb-hash-1112','{"state": "ready"}','2026-06-15 11:00:00+00','2026-06-15 11:59:00+00'),
 (1113,'11111111-1111-4111-8111-111111111111','Ready three minutes ago','FILE','','nb-hash-1113','{"state": "ready"}','2026-06-15 11:00:00+00','2026-06-15 11:57:00+00'),
 (1116,'11111111-1111-4111-8111-111111111111','Ready before the name','FILE','','nb-hash-1116','{"state": "ready"}','2026-06-15 11:00:00+00','2026-06-15 11:45:00+00'),
 (1190,'22222222-2222-4222-8222-222222222222','Bob''s chart','FILE','','nb-hash-1190','{"state": "pending"}','2026-06-15 11:59:00+00','2026-06-15 11:59:00+00');

INSERT INTO notebook_sources (id, notebook_id, user_id, document_id, enabled, added_at)
VALUES
 (1100,1100,'11111111-1111-4111-8111-111111111111',1100,true, '2026-06-02 08:00:00+00'),
 (1101,1100,'11111111-1111-4111-8111-111111111111',1101,true, '2026-06-02 08:00:01+00'),
 (1102,1100,'11111111-1111-4111-8111-111111111111',1102,false,'2026-06-02 08:00:02+00'),
 (1110,1110,'11111111-1111-4111-8111-111111111111',1110,true, '2026-06-15 11:50:00+00'),
 (1111,1111,'11111111-1111-4111-8111-111111111111',1111,true, '2026-06-15 11:29:00+00'),
 (1112,1112,'11111111-1111-4111-8111-111111111111',1112,true, '2026-06-15 11:00:00+00'),
 (1113,1113,'11111111-1111-4111-8111-111111111111',1113,true, '2026-06-15 11:00:00+00'),
 (1115,1115,'11111111-1111-4111-8111-111111111111',1112,true, '2026-06-15 11:00:00+00'),
 (1116,1116,'11111111-1111-4111-8111-111111111111',1116,true, '2026-06-15 11:00:00+00'),
 (1118,1118,'11111111-1111-4111-8111-111111111111',1112,true, '2026-06-15 11:00:00+00'),
 (1119,1119,'11111111-1111-4111-8111-111111111111',1112,true, '2026-06-15 11:00:00+00'),
 (1120,1120,'11111111-1111-4111-8111-111111111111',1110,true, '2026-06-15 11:50:00+00'),
 (1121,1121,'11111111-1111-4111-8111-111111111111',1110,true, '2026-06-15 11:50:00+00'),
 (1190,1190,'22222222-2222-4222-8222-222222222222',1190,true, '2026-06-15 11:59:00+00');

-- What a deleted notebook leaves behind: 1100 has a chat, a note and a studio
-- output. The output goes with it; the chat and the note stay, unfiled.
INSERT INTO new_chat_threads (id, user_id, title, notebook_id)
VALUES (1100,'11111111-1111-4111-8111-111111111111','The harbour chat',1100);
INSERT INTO documents (id, user_id, title, document_type, content, content_hash)
VALUES (1150,'11111111-1111-4111-8111-111111111111','A note on the harbour','NOTE','','nb-hash-1150');
INSERT INTO notes (document_id, user_id, notebook_id)
VALUES (1150,'11111111-1111-4111-8111-111111111111',1100);
INSERT INTO notebook_outputs (id, notebook_id, user_id, kind, title)
VALUES (1100,1100,'11111111-1111-4111-8111-111111111111','flashcards','A deck');

-- Alice has edited one report template; Bob has edited the same one, and
-- another. Neither may see or change the other's.
INSERT INTO notebook_report_templates (id, user_id, template_key, title, description, prompt,
                                       created_at, updated_at)
VALUES
 (1100,'11111111-1111-4111-8111-111111111111','blog_post','Alice''s post','Hers','Write it her way.',
  '2026-06-01 00:00:00+00','2026-06-01 00:00:00+00'),
 (1190,'22222222-2222-4222-8222-222222222222','blog_post','Bob''s post','His','Write it his way.',
  '2026-06-01 00:00:00+00','2026-06-01 00:00:00+00'),
 (1191,'22222222-2222-4222-8222-222222222222','study_guide','Bob''s guide','His','Guide it his way.',
  '2026-06-01 00:00:00+00','2026-06-01 00:00:00+00');

-- Past every id seeded above: a create must never be handed an id a seeded
-- row holds.
SELECT setval('notebooks_id_seq', 1199, true);
SELECT setval('documents_id_seq', 1199, true);
SELECT setval('notebook_sources_id_seq', 1199, true);
SELECT setval('notebook_report_templates_id_seq', 1199, true);
SELECT setval('notebook_outputs_id_seq', 1199, true);
SELECT setval('new_chat_threads_id_seq', 1199, true);
