-- +goose Up

-- Which push service a phone's token belongs to. iPhones are pushed straight
-- through Apple's APNs with the raw device token the phone hands the app;
-- Android phones stay on Expo's push service with an Expo push token. The
-- worker's delivery job (soloclef_notify/delivery) sends each row to the
-- sender its kind names, so the token's format alone is never guessed at.
--
--   kind              'expo' (an "ExponentPushToken[...]") or 'apns' (a
--                     device token in hex)
--   apns_environment  for an APNs token, the APNs environment it was minted
--                     in: 'production' (App Store and TestFlight builds) or
--                     'sandbox' (development builds). A token only works in
--                     its own environment; the worker pushes only the tokens
--                     of the environment it is set up for and leaves the
--                     others alone, so a development phone signed in to the
--                     live server is not deleted as a bad token. NULL for an
--                     Expo token, which carries no environment of its own.
--
-- Every row there is today was registered with an Expo token, so they become
-- 'expo'. The default exists only to fill them: it is dropped at once, and
-- every registration from now on names its kind. An iPhone moves to APNs by
-- registering again on its next launch (the API replaces its sign-in's old
-- token, so it is never pushed twice).
--
-- The check says IS NOT NULL in so many words: a CHECK passes when it is
-- NULL, and "NULL IN (...)" is NULL, so without it an APNs row with no
-- environment would be let in.
ALTER TABLE push_devices
    ADD COLUMN kind varchar(8) NOT NULL DEFAULT 'expo',
    ADD COLUMN apns_environment varchar(16),
    ADD CONSTRAINT ck_push_devices_kind CHECK (
        (kind = 'expo' AND apns_environment IS NULL)
        OR (kind = 'apns' AND apns_environment IS NOT NULL
            AND apns_environment IN ('production', 'sandbox'))
    );
ALTER TABLE push_devices ALTER COLUMN kind DROP DEFAULT;

-- +goose Down

-- The code before this migration hands every token to Expo, which refuses a
-- raw APNs token, so those rows would only fail; they are removed, and the
-- phones register again with whatever the app they run sends.
DELETE FROM push_devices WHERE kind = 'apns';
ALTER TABLE push_devices
    DROP CONSTRAINT ck_push_devices_kind,
    DROP COLUMN apns_environment,
    DROP COLUMN kind;
