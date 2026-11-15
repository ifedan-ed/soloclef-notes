-- +goose Up

-- The owner's decision (2026-11-13): a mail draft's card is its own
-- question. draft_email only stores a draft; nothing is sent until the
-- person presses Send on the card, so an "ask first" prompt before it asked
-- whether to show them their question. The chat no longer applies a mark to
-- it (soloclef_assistant/assembly, cardIsTheQuestion) and the settings page
-- no longer lists it (soloclef_api/internal/agentrules, the catalogue), so a
-- mark set before is removed rather than left as a row nothing reads.
-- Only the chat's own tool is meant: a connected app's tool of the same name
-- (connector_id set) is another tool, and its mark stays.
DELETE FROM agent_ask_first_tools
WHERE connector_id IS NULL
  AND tool = 'draft_email';

-- +goose Down

-- Nothing to put back: which accounts had marked the tool is not kept, and
-- an empty mark is what the tool behaved as anyway.
SELECT 1;
