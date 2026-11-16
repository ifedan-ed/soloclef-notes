-- +goose Up

-- A studio output remembers which of the notebook's sources it was made
-- from, so that Try again makes it from the same ones.
--
-- An output already keeps the documents it read (source_document_ids), but
-- a source can be a folder, whose documents change, and Try again sends a
-- request in the dialog's terms: notebook sources. Mapping documents back
-- to sources loses the folder, so a retried output was made from every
-- ticked source instead of the ones chosen. The chosen sources are kept as
-- they were asked for: the ids of notebook_sources rows. A source removed
-- since is simply not sent again (the web app leaves it out).
--
-- NULL is an output whose sources were never chosen as notebook sources:
-- one made before this, one a notebook's chat made, one started from a
-- report's own documents. Its Try again reads the ticked sources, as
-- before. No foreign key: the array outlives a removed source by design.
ALTER TABLE notebook_outputs ADD COLUMN notebook_source_ids bigint[];

-- +goose Down

ALTER TABLE notebook_outputs DROP COLUMN notebook_source_ids;
