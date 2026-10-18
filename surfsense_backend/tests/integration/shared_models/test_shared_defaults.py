"""With only the shared models, a workspace still gets the owner's quick-task
model, role fallbacks, and is ready to chat without onboarding."""

from __future__ import annotations

import uuid
from types import SimpleNamespace

import pytest

from app.auth.context import AuthContext
from app.config import config
from app.db import User, Workspace
from app.model_role_fallbacks.named import fallback_model
from app.quick_tasks.resolution import resolve_quick_model
from app.routes.model_connections_routes import compute_llm_setup_status
from app.routes.workspaces_routes import create_default_roles_and_membership

pytestmark = pytest.mark.integration

CONNECTION = {
    "id": -1,
    "provider": "openai_compatible",
    "base_url": "http://litellm:4000/v1",
    "api_key": "sk-shared",
    "extra": {},
    "scope": "GLOBAL",
    "enabled": True,
}


def _shared(id_, model_id, **caps):
    return {
        "id": id_,
        "connection_id": -1,
        "model_id": model_id,
        "enabled": True,
        "capabilities_override": {},
        **caps,
    }


SHARED = [
    _shared(
        -1, "ds-deepseek-v4.1-flash", supports_chat=True, supports_image_input=True
    ),
    _shared(
        -5, "google-gemini-3.8-flash", supports_chat=True, supports_image_input=True
    ),
    _shared(-2002, "openai-gpt-image-2.5-sunburst", supports_image_generation=True),
]


@pytest.fixture
def shared(monkeypatch):
    monkeypatch.setattr(config, "GLOBAL_CONNECTIONS", [CONNECTION])
    monkeypatch.setattr(config, "GLOBAL_MODELS", SHARED)
    monkeypatch.setenv("IMAGE_GEN_FALLBACK_MODEL", "openai-gpt-image-2.5-sunburst")
    monkeypatch.setenv("VISION_FALLBACK_MODEL", "google-gemini-3.8-flash")


async def _owner(db_session) -> SimpleNamespace:
    user = User(
        id=uuid.uuid4(),
        email=f"sm-{uuid.uuid4().hex[:8]}@surfsense.test",
        hashed_password="x",
        is_active=True,
        is_superuser=False,
        is_verified=True,
    )
    db_session.add(user)
    await db_session.flush()
    workspace = Workspace(name="Shared", user_id=user.id)
    db_session.add(workspace)
    await db_session.flush()
    await create_default_roles_and_membership(db_session, workspace.id, user.id)
    await db_session.flush()
    return SimpleNamespace(user=user, workspace=workspace)


async def test_quick_tasks_default_to_the_shared_deepseek(db_session, shared):
    owner = await _owner(db_session)
    model = await resolve_quick_model(db_session, owner.workspace)
    assert (model.id, model.model_id, model.source) == (
        -1,
        "ds-deepseek-v4.1-flash",
        "default",
    )
    assert model.connection["api_key"] == "sk-shared"


async def test_role_fallbacks_find_the_shared_models(db_session, shared):
    owner = await _owner(db_session)
    image = await fallback_model(
        db_session, owner.workspace, "image_gen", primary_model="openai-gpt-image-2"
    )
    vision = await fallback_model(
        db_session, owner.workspace, "vision", primary_model="google-gemini-3.7-flash"
    )
    assert (image.id, image.model_id) == (-2002, "openai-gpt-image-2.5-sunburst")
    assert (vision.id, vision.model_id) == (-5, "google-gemini-3.8-flash")
    assert vision.connection["base_url"] == "http://litellm:4000/v1"
    # Never "fall back" to the model that just failed.
    assert (
        await fallback_model(
            db_session,
            owner.workspace,
            "vision",
            primary_model="google-gemini-3.8-flash",
        )
        is None
    )


async def test_a_brand_new_workspace_is_ready_without_onboarding(
    db_session, shared, monkeypatch
):
    monkeypatch.setattr(config, "GLOBAL_LLM_CONFIG_FILE_EXISTS", True)
    owner = await _owner(db_session)
    status = await compute_llm_setup_status(
        db_session, AuthContext.session(owner.user), owner.workspace.id
    )
    assert (status.status, status.stage, status.source) == (
        "ready",
        "ready",
        "global_config",
    )
