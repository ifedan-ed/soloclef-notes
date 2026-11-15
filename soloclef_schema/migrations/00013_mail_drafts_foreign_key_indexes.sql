-- +goose Up

-- Indexes for mail_drafts' two foreign keys (migration 7 left them without).
--
-- Without an index on the referencing column, deleting an account (and its
-- user row) or a superseded draft makes Postgres scan the whole mail_drafts
-- table once per deleted row to enforce the key, under a lock. Every other
-- foreign key in the schema has one; TestEveryForeignKeyHasAnIndex holds the
-- schema to that. superseded_by is mostly empty, so its index is partial.
CREATE INDEX IF NOT EXISTS mail_drafts_user_id_idx ON mail_drafts (user_id);
CREATE INDEX IF NOT EXISTS mail_drafts_superseded_by_idx ON mail_drafts (superseded_by)
    WHERE superseded_by IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS mail_drafts_superseded_by_idx;
DROP INDEX IF EXISTS mail_drafts_user_id_idx;
