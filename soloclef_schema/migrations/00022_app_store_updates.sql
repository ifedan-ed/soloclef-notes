-- +goose Up

-- "Soloclef 1.2.0 is out": a push to each iPhone that runs an older version
-- than the App Store offers, in place of the banner the app drew after
-- asking Apple itself every day. The worker asks Apple once a day for the
-- whole deployment (soloclef_worker/internal/appupdate) and tells each phone
-- once per store version (soloclef_notify/notify, AppUpdated).
--
--   app_version              the version the phone's app reported when it
--                            last registered ("1.1.0", a dotted number as
--                            App Store Connect allows). NULL for a build
--                            from before this column, which still draws its
--                            own banner and so is never pushed about an
--                            update: nothing is guessed about a version it
--                            did not say.
--   update_notified_version  the App Store version this phone was last told
--                            about. A phone is told about a version when
--                            this is not that version, so it is told once
--                            per version however many times the worker
--                            looks, and again when a newer one comes out.
--
-- The version's shape is checked here as well as by the API, so the worker's
-- comparison never meets a value it cannot read.
ALTER TABLE push_devices
    ADD COLUMN app_version varchar(32),
    ADD COLUMN update_notified_version varchar(32),
    ADD CONSTRAINT ck_push_devices_app_version CHECK (app_version ~ '^[0-9]{1,9}(\.[0-9]{1,9}){0,3}$');

-- The newest version of the phone app the App Store has been seen to offer,
-- one row per bundle id the deployment names
-- (SOLOCLEF_WORKER_APP_STORE_BUNDLE_ID). It is kept so that the daily check
-- survives a restart (checked_at says when Apple was last asked), so that the
-- worker's hourly pass can tell phones about a version without asking Apple
-- again, and so that an answer from Apple older than one already seen (its
-- lookup can lag for a while after a release) never moves the version back.
--
--   track_id    the App Store's id for the app, from which the listing's
--               address (https://apps.apple.com/app/id<track_id>) is made
--   checked_at  when Apple last answered
--   changed_at  when the version last changed
CREATE TABLE app_store_versions (
    bundle_id  varchar(255) PRIMARY KEY,
    version    varchar(32) NOT NULL,
    track_id   bigint NOT NULL,
    checked_at timestamptz NOT NULL,
    changed_at timestamptz NOT NULL,
    CONSTRAINT ck_app_store_versions_version CHECK (version ~ '^[0-9]{1,9}(\.[0-9]{1,9}){0,3}$'),
    CONSTRAINT ck_app_store_versions_track_id CHECK (track_id > 0)
);

-- +goose Down

DROP TABLE app_store_versions;
ALTER TABLE push_devices
    DROP CONSTRAINT ck_push_devices_app_version,
    DROP COLUMN update_notified_version,
    DROP COLUMN app_version;
