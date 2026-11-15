-- +goose Up

-- Nothing in Soloclef is drawn by an image model any more: the chat's
-- figures and the infographics are drawn by code (soloclef_studio/figurekit
-- and posterrender), so there is no image model for an account to choose.
-- The choices accounts made for one are removed, and the purpose with
-- them, so that no row holds a setting nothing reads.
DELETE FROM user_models WHERE purpose = 'image';
ALTER TABLE user_models DROP CONSTRAINT ck_user_models_purpose;
ALTER TABLE user_models ADD CONSTRAINT ck_user_models_purpose
    CHECK (purpose IN ('chat', 'quick', 'studio', 'vision'));

-- +goose Down

-- The purpose comes back; the choices removed do not.
ALTER TABLE user_models DROP CONSTRAINT ck_user_models_purpose;
ALTER TABLE user_models ADD CONSTRAINT ck_user_models_purpose
    CHECK (purpose IN ('chat', 'quick', 'studio', 'vision', 'image'));
