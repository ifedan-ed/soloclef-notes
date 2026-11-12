-- +goose Up

-- A studio output remembers the click that asked for it.
--
-- The web app sends an id of its own with every "Generate" click, so that a
-- click sent twice (a retry after a slow answer, a double press) is one
-- output. Until now that id lived only on the deliverable job an output
-- points at, and an audio overview has no such job: it is a podcast run. So
-- a repeated click for audio opened a second podcast and paid for a second
-- voicing. The key is kept on the output itself, where every kind has it.
--
-- It is "notebook-studio:<notebook>:<the click's id>", the same text the
-- job's tool_call_id holds. NULL for an output whose click sent no id and
-- for every output made before this.
ALTER TABLE notebook_outputs ADD COLUMN request_key text;

-- One output per click, held by the database: two requests of the same
-- click arriving together cannot both be listed. Partial, so the outputs
-- with no key are not in it.
CREATE UNIQUE INDEX uq_notebook_outputs_request_key
    ON notebook_outputs (notebook_id, request_key) WHERE request_key IS NOT NULL;

-- +goose Down

DROP INDEX uq_notebook_outputs_request_key;
ALTER TABLE notebook_outputs DROP COLUMN request_key;
