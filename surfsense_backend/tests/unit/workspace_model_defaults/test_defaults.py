"""A new workspace starts on the owner's vision and image models."""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from app.workspace_model_defaults import (
    apply_default_role_models,
    default_role_model_ids,
)

pytestmark = pytest.mark.unit


def _model(id_, model_id, **caps):
    return {"id": id_, "model_id": model_id, "enabled": True, **caps}


SHARED = [
    _model(-1, "ds-deepseek-v4.1-flash", supports_chat=True, supports_image_input=True),
    _model(
        -4, "google-gemini-3.7-flash", supports_chat=True, supports_image_input=True
    ),
    _model(-2001, "openai-gpt-image-2", supports_image_generation=True),
]


def test_vision_and_image_start_on_the_named_shared_models():
    assert default_role_model_ids(SHARED) == {
        "vision_model_id": -4,
        "image_gen_model_id": -2001,
    }


def test_a_missing_or_incapable_model_leaves_the_role_on_auto():
    incapable = [_model(-4, "google-gemini-3.7-flash", supports_chat=True)]
    assert default_role_model_ids(incapable) == {}
    assert default_role_model_ids([]) == {}


def test_applying_sets_only_the_defaulted_columns():
    workspace = SimpleNamespace(
        chat_model_id=0, vision_model_id=0, image_gen_model_id=0
    )
    apply_default_role_models(workspace, SHARED)
    assert (
        workspace.chat_model_id,
        workspace.vision_model_id,
        workspace.image_gen_model_id,
    ) == (
        0,
        -4,
        -2001,
    )
