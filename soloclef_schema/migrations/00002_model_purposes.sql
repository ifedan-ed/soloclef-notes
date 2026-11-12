-- +goose Up

-- An account chooses a model for four more jobs, each by the name the model
-- proxy knows it by, in the table its chat model is already in:
--
--   quick   small helper calls (the instructions check)
--   studio  long written work: a podcast's transcript, a video's script
--   vision  looking at pictures
--   image   drawing pictures
--
-- No row still means "the deployment's model for that job". The list stays a
-- CHECK so that a purpose is added here, with the code that reads it, and
-- never by a route that happens to be sent a new word.
ALTER TABLE user_models DROP CONSTRAINT ck_user_models_purpose;
ALTER TABLE user_models ADD CONSTRAINT ck_user_models_purpose
    CHECK (purpose IN ('chat', 'quick', 'studio', 'vision', 'image'));

-- +goose Down

-- The choices the narrower list cannot hold go with it.
DELETE FROM user_models WHERE purpose NOT IN ('chat');
ALTER TABLE user_models DROP CONSTRAINT ck_user_models_purpose;
ALTER TABLE user_models ADD CONSTRAINT ck_user_models_purpose
    CHECK (purpose IN ('chat'));
