-- name: PromptsOfAccount :many
-- The caller's own prompts only: user_id is the predicate rather than a check
-- afterwards, so this route is not an oracle for anyone else's prompts.
--
-- The ORDER BY carries the id as well as the timestamp, and so does the Python
-- route as of 2026-11-08. created_at is **not unique** — the defaults a new
-- account is given are written in one flush — and ordering by it alone leaves
-- the tied rows to the plan, which is not the same plan on both tiers: a
-- prepared statement switches to a generic plan on its fifth execution on a
-- connection, and the two drivers' connection reuse differs. That is exactly
-- what stopped GET /api/v1/folders being ported, and the id closes it here
-- (custom/docs/go-api-tier.md, "The folder list's order is not a property
-- either tier has").
SELECT id,
       name,
       prompt,
       mode::text AS mode,
       is_public,
       version,
       created_at
FROM prompts
WHERE user_id = $1
ORDER BY created_at DESC, id DESC;

-- name: PublicPromptsOfOtherAccounts :many
-- list_public_prompts: every community prompt except the caller's own.
--
-- The author's row is joined rather than fetched in a second statement. The
-- Python route uses selectinload, which is a second SELECT over the ids it
-- just read; the rows are the same either way because prompts.user_id is NOT
-- NULL and carries a foreign key to a primary key, so the join can neither
-- multiply a prompt nor drop one. (The route still reads `p.user if p.user`,
-- and the renderer keeps that nullability, because the column's NOT NULL is
-- the only reason it cannot happen.)
--
-- display_name, not email. The Python route sent the address until 2026-11-08,
-- which handed every signed-in account the site administrators' addresses —
-- only an administrator can publish a community prompt — and both tiers send
-- the display name now.
SELECT prompts.id,
       prompts.name,
       prompts.prompt,
       prompts.mode::text AS mode,
       prompts.is_public,
       prompts.version,
       prompts.created_at,
       account.display_name AS author_name
FROM prompts
JOIN "user" AS account ON account.id = prompts.user_id
WHERE prompts.is_public = true AND prompts.user_id <> $1
ORDER BY prompts.created_at DESC, prompts.id DESC;

-- name: PromptOfAccount :one
-- The row update_prompt and delete_prompt resolve before anything else. The
-- owner is part of the predicate, so another account's prompt is "Prompt not
-- found" rather than refused — the two routes were never existence oracles the
-- way the log and folder routes were.
SELECT id,
       name,
       prompt,
       mode::text AS mode,
       is_public,
       version,
       created_at
FROM prompts
WHERE id = $1 AND user_id = $2;

-- name: PublicPromptByID :one
-- copy_public_prompt's source. is_public is part of the predicate and the
-- owner is not, so any account may copy any published prompt — including one
-- of their own, which neither tier excludes.
SELECT id,
       name,
       prompt,
       mode::text AS mode,
       is_public,
       version,
       created_at
FROM prompts
WHERE id = $1 AND is_public = true;

-- name: AccountIsAdministrator :one
-- Whether the caller administers this deployment: the account's own flag,
-- the one the web app reads from /users/me. Only run when a request actually
-- asks to publish (internal/prompts/admin.go).
SELECT is_superuser FROM "user" WHERE id = $1;

-- name: CreatePrompt :one
-- create_prompt's and copy_public_prompt's insert. created_at and version are
-- passed in rather than defaulted in SQL because both are Python-side defaults
-- on the model, evaluated in the application.
INSERT INTO prompts (user_id, name, prompt, mode, version, is_public, created_at)
VALUES (@user_id,
        @name,
        @prompt,
        @mode::prompt_mode,
        @version,
        @is_public,
        @created_at)
RETURNING id,
          name,
          prompt,
          mode::text AS mode,
             is_public,
          version,
          created_at;

-- name: UpdatePrompt :one
-- update_prompt's write. The Python route assigns only the fields the body
-- mentioned (model_dump(exclude_unset=True)), so each CASE here is one of
-- those decisions: an unmentioned column keeps its value and an explicit null
-- is written as NULL, which every column here refuses — a 500 on both tiers.
--
-- The version is bumped by the same expression the Python route assigns
-- (`Prompt.version = Prompt.version + 1`, which is the column and not the
-- loaded value), and only when the body mentioned one of name, prompt or mode.
-- Mentioning is_public alone changes no content and bumps nothing.
UPDATE prompts
SET name = CASE WHEN @set_name::boolean THEN sqlc.narg(name)::varchar ELSE name END,
    prompt = CASE WHEN @set_prompt::boolean THEN sqlc.narg(prompt)::text ELSE prompt END,
    mode = CASE WHEN @set_mode::boolean THEN sqlc.narg(mode)::prompt_mode ELSE mode END,
    is_public = CASE WHEN @set_is_public::boolean THEN sqlc.narg(is_public)::boolean ELSE is_public END,
    version = CASE WHEN @bump_version::boolean THEN version + 1 ELSE version END
WHERE id = @id AND user_id = @user_id
RETURNING id,
          name,
          prompt,
          mode::text AS mode,
             is_public,
          version,
          created_at;

-- name: DeletePrompt :exec
-- delete_prompt's write. The owner is in the predicate as well as in the
-- lookup that already resolved the row, so the statement cannot act on a row
-- the 404 did not agree to.
DELETE FROM prompts WHERE id = $1 AND user_id = $2;
