-- An account's model choices, by purpose (modelchoices.go). Each statement is
-- keyed by the account: a choice is one row (user_id, purpose), so there
-- is no id another account could guess.

-- name: ChoicesOfAccount :many
-- Every choice the account has made, in one statement: a default can depend
-- on another purpose's choice (quick and studio default to the chat model),
-- and one read is cheaper than one per purpose. At most five rows.
SELECT purpose, model FROM user_models
WHERE user_id = sqlc.arg(user_id)::uuid;

-- name: ChooseModel :exec
INSERT INTO user_models (user_id, purpose, model)
VALUES (sqlc.arg(user_id)::uuid, sqlc.arg(purpose), sqlc.arg(model))
ON CONFLICT (user_id, purpose) DO UPDATE
SET model = EXCLUDED.model, updated_at = now();

-- name: ClearModel :exec
-- Back to the default: no row, not a row that says so.
DELETE FROM user_models
WHERE user_id = sqlc.arg(user_id)::uuid AND purpose = sqlc.arg(purpose);
