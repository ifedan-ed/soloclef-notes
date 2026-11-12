-- Which vision model an account chose: what every caller that shows an
-- account's pictures to a vision model asks first. Keyed by the account;
-- the owner of the work is the only one ever asked about.

-- name: VisionChoiceOfAccount :one
-- The account's own vision choice; no row is "the deployment's default"
-- (the rule soloclef_api/internal/modelchoices answers by).
SELECT model FROM user_models
WHERE user_id = sqlc.arg(user_id)::uuid AND purpose = 'vision';
