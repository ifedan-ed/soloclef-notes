SET client_min_messages = warning;
-- The rows every comparison is run against, and the only state either tier is
-- allowed to see.
--
-- Every account, workspace, document and address here is made up for the
-- comparison; nothing was read from a live database. The suite reseeds from
-- this file before each tier answers a write, so the two tiers are asked their
-- question of byte-identical rows.
--
-- Shapes matter more than quantity. A row is here because some route renders it
-- differently from its neighbour: a NULL beside an empty string, metadata
-- absent beside metadata that is the JSON scalar null, a float at pydantic's
-- decimal/scientific boundary, text carrying <, >, &, an em dash and U+2028, a
-- name that fills its column, and a status column holding something that is
-- truthy and not an object.
TRUNCATE TABLE logs, documents, workspace_memberships, workspace_roles,
               workspaces, personal_access_tokens, "user" RESTART IDENTITY CASCADE;

-- 1 owner: holds every permission everywhere it is a member.
-- 2 scribe: a role that may write logs and edit roles, but not delete.
-- 3 bare: a member whose membership has no role at all.
-- 4 outsider: a member of nothing the other accounts can see.
-- 5 inactive: a valid token for an account that is switched off.
-- 6 remover: a role whose only permission is documents:delete.
-- 7 sole: owns exactly one workspace, which is the last-workspace refusal.
INSERT INTO "user" (id, email, hashed_password, is_active, is_superuser,
                    is_verified, display_name, avatar_url, last_login, memory_md)
VALUES
 ('11111111-1111-1111-1111-111111111111','owner@example.invalid','x',true,false,true,
  'Ada Owner',NULL,'2026-01-02 03:04:05.123456+00','## Preferences
- 2026-01-02: prefers short answers'),
 ('22222222-2222-2222-2222-222222222222','scribe@example.invalid','x',true,false,true,
  NULL,'','2026-01-02 03:04:05+00',''),
 ('33333333-3333-3333-3333-333333333333','bare@example.invalid','x',true,false,true,
  'Bare Member',NULL,NULL,NULL),
 ('44444444-4444-4444-4444-444444444444','outsider@example.invalid','x',true,false,true,
  'Outsider',NULL,NULL,''),
 ('55555555-5555-5555-5555-555555555555','inactive@example.invalid','x',false,false,true,
  'Gone Away',NULL,NULL,''),
 ('66666666-6666-6666-6666-666666666666','remover@example.invalid','x',true,false,true,
  'May Remove',NULL,NULL,''),
 ('77777777-7777-7777-7777-777777777777','sole@example.invalid','x',true,false,true,
  'Only One',NULL,NULL,'');

-- 1 the workspace most comparisons are asked about: API access off, so a key
--   is refused there and accepted in 2.
-- 2 API access on.
-- 3 already carrying the soft-delete marker.
-- 4 a name that fills varchar(100) with em dashes, so the marker's truncation
--   is a cut in characters and not in bytes.
-- 5 the sole account's only workspace.
-- 6 a workspace the other accounts are not members of.
INSERT INTO workspaces (id, name, description, citations_enabled,
                        api_access_enabled, user_id, created_at, shared_memory_md)
VALUES
 (1,'Comparison — the “gated” one',NULL,true,false,
  '11111111-1111-1111-1111-111111111111','2026-01-01 00:00:00+00',
  '## Team notes
- 2026-01-01: the comparison workspace'),
 (2,'Open to keys',NULL,true,true,
  '11111111-1111-1111-1111-111111111111','2026-01-01 00:00:01+00',''),
 (3,'[DELETING] Comparison — already going',NULL,true,false,
  '11111111-1111-1111-1111-111111111111','2026-01-01 00:00:02+00',''),
 (4,repeat('—',100),NULL,true,false,
  '11111111-1111-1111-1111-111111111111','2026-01-01 00:00:03+00',''),
 (5,'The only one',NULL,true,false,
  '77777777-7777-7777-7777-777777777777','2026-01-01 00:00:04+00',''),
 (6,'Nobody you know',NULL,true,false,
  '44444444-4444-4444-4444-444444444444','2026-01-01 00:00:05+00','');
SELECT setval('workspaces_id_seq', 6, true);

INSERT INTO workspace_roles (id, name, description, permissions, is_default,
                             is_system_role, workspace_id, created_at)
VALUES
 (1,'Owner','Full access','{*}',false,true,1,'2026-01-01 00:00:00+00'),
 (2,'Reader','Reads — logs, members and roles','{logs:read,members:view,roles:read}',
  true,false,1,'2026-01-01 00:00:00.5+00'),
 (3,'Scribe','Writes logs too','{logs:create,logs:read,logs:update,logs:delete,roles:read,roles:update,roles:delete,roles:create}',
  false,false,1,'2026-01-01 00:00:01+00'),
 (4,'Nothing',NULL,'{}',false,false,1,'2026-01-01 00:00:02+00'),
 (5,'Owner','Full access','{*}',false,true,2,'2026-01-01 00:00:00+00'),
 (6,'Reader','Reads','{logs:read,members:view,roles:read}',false,false,2,'2026-01-01 00:00:03+00'),
 (7,'Remover','Deletes documents and nothing else','{documents:delete}',
  false,false,1,'2026-01-01 00:00:04+00'),
 (8,'Owner','Full access','{*}',false,true,3,'2026-01-01 00:00:00+00'),
 (9,'Owner','Full access','{*}',false,true,4,'2026-01-01 00:00:00+00'),
 (10,'Owner','Full access','{*}',false,true,5,'2026-01-01 00:00:00+00'),
 (11,'Owner','Full access','{*}',false,true,6,'2026-01-01 00:00:00+00');
SELECT setval('workspace_roles_id_seq', 11, true);

INSERT INTO workspace_memberships (id, user_id, workspace_id, role_id, is_owner,
                                   joined_at, created_at)
VALUES
 (1,'11111111-1111-1111-1111-111111111111',1,1,true,'2026-01-01 00:00:00+00','2026-01-01 00:00:00+00'),
 (2,'22222222-2222-2222-2222-222222222222',1,3,false,'2026-01-01 00:00:00.5+00','2026-01-01 00:00:00.5+00'),
 (3,'33333333-3333-3333-3333-333333333333',1,NULL,false,'2026-01-01 00:00:01+00','2026-01-01 00:00:01+00'),
 (4,'11111111-1111-1111-1111-111111111111',2,5,true,'2026-01-01 00:00:02+00','2026-01-01 00:00:02+00'),
 (5,'22222222-2222-2222-2222-222222222222',2,6,false,'2026-01-01 00:00:03+00','2026-01-01 00:00:03+00'),
 (6,'66666666-6666-6666-6666-666666666666',1,7,false,'2026-01-01 00:00:04+00','2026-01-01 00:00:04+00'),
 (7,'11111111-1111-1111-1111-111111111111',3,8,true,'2026-01-01 00:00:05+00','2026-01-01 00:00:05+00'),
 (8,'11111111-1111-1111-1111-111111111111',4,9,true,'2026-01-01 00:00:06+00','2026-01-01 00:00:06+00'),
 (9,'77777777-7777-7777-7777-777777777777',5,10,true,'2026-01-01 00:00:07+00','2026-01-01 00:00:07+00'),
 (10,'44444444-4444-4444-4444-444444444444',6,11,true,'2026-01-01 00:00:08+00','2026-01-01 00:00:08+00');
SELECT setval('workspace_memberships_id_seq', 10, true);

-- An API key for the owner and one for the member. The tokens themselves are
-- minted nowhere: they are the literal strings below, and the stored prefix is
-- token[:16] exactly as app/utils/pat.py:token_prefix computes it, because that
-- prefix is rendered in the key list's body.
INSERT INTO personal_access_tokens (id, user_id, token_hash, token_prefix, label,
                                    expires_at, last_used_at, created_at)
VALUES
 (1,'11111111-1111-1111-1111-111111111111',
  encode(sha256('ss_pat_parity_owner_key'::bytea),'hex'),
  left('ss_pat_parity_owner_key',16),'owner key',NULL,NULL,'2026-01-01 00:00:00+00'),
 (2,'22222222-2222-2222-2222-222222222222',
  encode(sha256('ss_pat_parity_scribe_key'::bytea),'hex'),
  left('ss_pat_parity_scribe_key',16),'scribe key',NULL,NULL,'2026-01-01 00:00:01+00');
SELECT setval('personal_access_tokens_id_seq', 2, true);

-- Documents, for the one delete that owes a Celery message. The status column
-- decides the answer, so each row is one status the route branches on, plus one
-- that is truthy and not an object — which is the row Python raises on.
INSERT INTO documents (id, title, document_type, content, content_hash,
                       workspace_id, status, created_at)
VALUES
 (1,'A ready document','NOTE','body one','hash-1',1,'{"state": "ready"}','2026-01-01 00:00:00+00'),
 (2,'Being processed','NOTE','body two','hash-2',1,'{"state": "processing"}','2026-01-01 00:00:01+00'),
 (3,'Still pending','NOTE','body three','hash-3',1,'{"state": "pending"}','2026-01-01 00:00:02+00'),
 (4,'Already deleting','NOTE','body four','hash-4',1,'{"state": "deleting"}','2026-01-01 00:00:03+00'),
 (5,'A status with no state','NOTE','body five','hash-5',1,'{}','2026-01-01 00:00:04+00'),
 (6,'In a workspace you are not in','NOTE','body six','hash-6',6,'{"state": "ready"}','2026-01-01 00:00:05+00'),
 (7,'A status that is not an object','NOTE','body seven','hash-7',1,'"ready"','2026-01-01 00:00:06+00');
SELECT setval('documents_id_seq', 7, true);

-- Logs: the shapes that decide the bytes. Metadata absent, {}, null, nested,
-- a non-string task name, a float, a source that is NULL and one that is '',
-- text carrying <, >, &, an em dash, curly quotes and U+2028.
INSERT INTO logs (id, level, status, message, source, log_metadata, workspace_id, created_at)
VALUES
 (1,'INFO','IN_PROGRESS','indexing — step one','indexer','{"task_name":"index","document_id":7}',1,'2026-11-01 10:00:00+00'),
 (2,'ERROR','FAILED','a < b & c > d','indexer','{"task_name":42,"error_details":{"code":"E1","at":1.5}}',1,'2026-11-01 10:00:01.500000+00'),
 (3,'WARNING','SUCCESS','“quoted” and U+2028:
 after',NULL,NULL,1,'2026-11-01 10:00:02+00'),
 (4,'DEBUG','SUCCESS','empty source','','{}',1,'2026-11-01 10:00:03+00'),
 (5,'CRITICAL','FAILED','no metadata at all','worker',NULL,1,'2026-11-01 10:00:04+00'),
 (6,'INFO','SUCCESS','another workspace','indexer','{"task_name":"other"}',2,'2026-11-01 10:00:05+00'),
 -- Floats at both ends of pydantic's decimal/scientific boundary, written as
 -- SQLAlchemy would have written them, and text carrying U+2028 and U+2029.
 (7,'INFO','SUCCESS','floats at the boundary','indexer','{"a": 1e+16, "b": 1e-05, "c": 0.0001, "d": 1e-06, "e": -0.0, "f": 1000000000000000.0}',1,'2026-11-01 10:00:06+00'),
 (8,'WARNING','SUCCESS',E'  and   in the text','','{"nested": {"k": [1, 2.5, null]}, "big": 99999999999999999999}',1,'2026-11-01 10:00:07.000001+00');
SELECT setval('logs_id_seq', 8, true);
