-- name: ReportedBundles :many
-- Each bundle the phones registered for push report -- platform, app
-- version, native runtime and over-the-air update -- with how many devices
-- were seen since @since and when one was last seen. A NULL version is a
-- build from before 1.2 that has not had an update which says it; a NULL
-- update is the bundle built into the app, or one from before updates were
-- reported. Phones that never allowed notifications never register, so they
-- are not here (internal/installedbuilds/README.md).
SELECT platform,
       app_version,
       runtime_version,
       update_id,
       count(*) FILTER (WHERE last_seen_at >= sqlc.arg(since)::timestamptz)::bigint AS seen_since,
       count(*)::bigint AS registered,
       max(last_seen_at)::timestamptz AS last_seen_at
FROM push_devices
GROUP BY platform, app_version, runtime_version, update_id
ORDER BY platform, app_version NULLS FIRST, runtime_version NULLS FIRST, update_id NULLS FIRST;

-- name: StoreVersions :many
-- The newest phone app version the App Store has been seen to offer, per
-- bundle id (the worker's daily check, soloclef_worker/internal/appupdate).
SELECT bundle_id, version, checked_at
FROM app_store_versions
ORDER BY bundle_id;
