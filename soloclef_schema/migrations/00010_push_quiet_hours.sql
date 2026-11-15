-- +goose Up

-- Quiet hours: a span of the day, in the person's own time zone, in which
-- nothing is pushed to their phone. The inbox row is written all the same,
-- so nothing is lost; only the phone stays silent.
--
-- Minutes since midnight rather than a `time`: the span is read by one Go
-- function (soloclef_notify/pushpolicy) that compares wall-clock minutes in
-- the zone, and a whole number says exactly that, with no seconds or time
-- zone of its own to misread. The zone is an IANA name ("Africa/Lagos"),
-- checked against the time zone database when it is saved, so a span always
-- means one thing even across a change to summer time.
--
-- All three are set or none is: quiet hours are off when they are NULL. A
-- span that starts where it ends would be either nothing or the whole day,
-- and which one is a guess, so it is refused.
ALTER TABLE push_preferences
    ADD COLUMN quiet_start_minute smallint,
    ADD COLUMN quiet_end_minute   smallint,
    ADD COLUMN time_zone          varchar(64),
    ADD CONSTRAINT ck_push_preferences_quiet_hours CHECK (
        (quiet_start_minute IS NULL AND quiet_end_minute IS NULL AND time_zone IS NULL)
        OR (quiet_start_minute BETWEEN 0 AND 1439
            AND quiet_end_minute BETWEEN 0 AND 1439
            AND quiet_start_minute <> quiet_end_minute
            AND time_zone IS NOT NULL AND time_zone <> '')
    );

-- +goose Down

ALTER TABLE push_preferences
    DROP CONSTRAINT ck_push_preferences_quiet_hours,
    DROP COLUMN time_zone,
    DROP COLUMN quiet_end_minute,
    DROP COLUMN quiet_start_minute;
