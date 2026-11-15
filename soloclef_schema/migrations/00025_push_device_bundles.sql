-- +goose Up

-- Which JavaScript bundle a phone runs, beside which app version
-- (00022): an over-the-air update replaces a build's bundle without a new
-- version, so the version alone cannot say whether any phone still runs an
-- old bundle, and the server keeps answering every bundle still installed
-- (soloclef_api/internal/installedbuilds). A phone reports both when it
-- registers for push, on every start of the app.
--
--   update_id        expo-updates' id of the update the phone runs; NULL for
--                    the bundle built into the app (no update applied yet),
--                    and for every bundle from before this column
--   runtime_version  the native runtime the bundle was made for (its
--                    fingerprint); NULL from a bundle that does not say
--
-- Both are shaped here as well as by the API, so a report never meets a
-- value it cannot read.
ALTER TABLE push_devices
    ADD COLUMN update_id uuid,
    ADD COLUMN runtime_version varchar(64),
    ADD CONSTRAINT ck_push_devices_runtime_version CHECK (runtime_version ~ '^[A-Za-z0-9._-]{1,64}$');

-- +goose Down

ALTER TABLE push_devices
    DROP CONSTRAINT ck_push_devices_runtime_version,
    DROP COLUMN runtime_version,
    DROP COLUMN update_id;
