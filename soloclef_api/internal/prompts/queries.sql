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
       version,
       created_at
FROM prompts
WHERE user_id = $1
ORDER BY created_at DESC, id DESC;

-- name: PromptOfAccount :one
-- The row update_prompt and delete_prompt resolve before anything else. The
-- owner is part of the predicate, so another account's prompt is "Prompt not
-- found" rather than refused — the two routes were never existence oracles the
-- way the log and folder routes were.
SELECT id,
       name,
       prompt,
       mode::text AS mode,
       version,
       created_at
FROM prompts
WHERE id = $1 AND user_id = $2;

-- name: CreatePrompt :one
-- create_prompt's insert. created_at and version are passed in rather than
-- defaulted in SQL because both were Python-side defaults on the model,
-- evaluated in the application.
INSERT INTO prompts (user_id, name, prompt, mode, version, created_at)
VALUES (@user_id,
        @name,
        @prompt,
        @mode::prompt_mode,
        @version,
        @created_at)
RETURNING id,
          name,
          prompt,
          mode::text AS mode,
          version,
          created_at;

-- name: UpdatePrompt :one
-- update_prompt's write, run only when the body mentioned at least one field
-- (update_route.go). Each CASE is one of those mentions: an unmentioned
-- column keeps its value and an explicit null is written as NULL, which every
-- column here refuses — a 500.
--
-- The version is bumped by the column's own expression rather than the loaded
-- value, so two updates racing cannot both write the same next version.
UPDATE prompts
SET name = CASE WHEN @set_name::boolean THEN sqlc.narg(name)::varchar ELSE name END,
    prompt = CASE WHEN @set_prompt::boolean THEN sqlc.narg(prompt)::text ELSE prompt END,
    mode = CASE WHEN @set_mode::boolean THEN sqlc.narg(mode)::prompt_mode ELSE mode END,
    version = version + 1
WHERE id = @id AND user_id = @user_id
RETURNING id,
          name,
          prompt,
          mode::text AS mode,
          version,
          created_at;

-- name: DeletePrompt :exec
-- delete_prompt's write. The owner is in the predicate as well as in the
-- lookup that already resolved the row, so the statement cannot act on a row
-- the 404 did not agree to.
DELETE FROM prompts WHERE id = $1 AND user_id = $2;
