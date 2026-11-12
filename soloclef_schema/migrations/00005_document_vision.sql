-- +goose Up

-- A document remembers whether its pictures are to be looked at.
--
-- An upload, a folder upload or a Nextcloud import can ask for the vision
-- model (`use_vision_llm`): an uploaded picture is then described by it, and
-- a PDF's embedded pictures are described and the descriptions put where
-- each picture sits in the text. The choice is kept on the row, beside the
-- parser it goes with, because a document is indexed again later (a
-- re-index, a retried failure, a file uploaded again) by a job that has
-- only the row to go on: a re-index that forgot the choice would quietly
-- drop every description the document had.
--
-- false for every document made before this, which is what they were
-- indexed with.
ALTER TABLE documents ADD COLUMN use_vision_llm boolean NOT NULL DEFAULT false;

-- +goose Down

ALTER TABLE documents DROP COLUMN use_vision_llm;
