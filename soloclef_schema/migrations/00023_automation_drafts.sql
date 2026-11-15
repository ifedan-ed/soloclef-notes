-- +goose Up

-- An automation the chat drafted and its owner has not yet created or
-- discarded. The chat's create_automation tool (soloclef_assistant/
-- automationdrafts) writes one row after checking the draft the way saving
-- checks an automation; the card in the chat reads it, the person edits it
-- there, and Create sends it through the ordinary create route
-- (POST /api/v1/automations with the draft's id), which marks the row
-- created in the same transaction that saves the automation. So a draft is
-- created at most once, however often Create is pressed or the page
-- reloaded, and nothing is saved that the person did not press Create for.
--
-- `request` is the create request exactly as the route takes it (name,
-- description, definition, triggers): the card edits a copy and sends it.
-- It is what the tool drafted and is never rewritten; what was saved is the
-- automation itself.
--
-- `replaced_by` is the newer draft the chat made when the person asked for
-- a change in words instead of on the card, so the older card can say so.
-- `automation_id` is the automation Create made; ON DELETE SET NULL keeps
-- the draft's "created" card (without its link) after the automation is
-- deleted. Rows go with their account (ON DELETE CASCADE).
CREATE TABLE automation_drafts (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    request       jsonb NOT NULL,
    status        text NOT NULL DEFAULT 'pending',
    replaced_by   uuid REFERENCES automation_drafts (id) ON DELETE SET NULL,
    automation_id bigint REFERENCES automations (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_automation_drafts_status
        CHECK (status IN ('pending', 'created', 'discarded', 'superseded')),
    -- Only a created draft names an automation. (A created one may name
    -- none: its automation was deleted afterwards.)
    CONSTRAINT ck_automation_drafts_created CHECK (status = 'created' OR automation_id IS NULL)
);
-- Every statement names the draft by its id and its owner together; this
-- serves the account's erasure cascade and the owner's reads.
CREATE INDEX ix_automation_drafts_user_id ON automation_drafts (user_id);
-- The two references that are SET NULL on delete are looked up by the
-- referenced row then; without an index each delete scans the table.
CREATE INDEX ix_automation_drafts_replaced_by ON automation_drafts (replaced_by) WHERE replaced_by IS NOT NULL;
CREATE INDEX ix_automation_drafts_automation_id ON automation_drafts (automation_id) WHERE automation_id IS NOT NULL;

-- +goose Down

DROP TABLE automation_drafts;
