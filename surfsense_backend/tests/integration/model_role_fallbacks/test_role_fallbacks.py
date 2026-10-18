"""Image generation and vision retry once on a named fallback model.

Real Postgres rows: whether a fallback is reachable depends on connection
scope, ownership, enabled flags and capability, which a mock would get wrong.
The provider call itself is the external boundary and is faked.
"""

from __future__ import annotations

import uuid
from types import SimpleNamespace

import litellm
import pytest

from app.artifacts.media.image import generation
from app.db import Connection, ConnectionScope, Model, User, Workspace
from app.model_role_fallbacks.named import fallback_model
from app.model_role_fallbacks.vision import with_vision_fallback
from app.services.llm_service import get_vision_llm

pytestmark = pytest.mark.integration

IMAGE_PRIMARY = "openai-gpt-image-2"
IMAGE_FALLBACK = "openai-gpt-image-2.5-sunburst"
VISION_PRIMARY = "google-gemini-3.7-flash"
VISION_FALLBACK = "google-gemini-3.8-flash"


async def _owner(db_session) -> SimpleNamespace:
    user = User(
        id=uuid.uuid4(),
        email=f"rf-{uuid.uuid4().hex[:8]}@surfsense.test",
        hashed_password="x",
        is_active=True,
        is_superuser=False,
        is_verified=True,
    )
    db_session.add(user)
    await db_session.flush()
    workspace = Workspace(name="Fallbacks", user_id=user.id, chat_model_id=0)
    db_session.add(workspace)
    await db_session.flush()
    connection = Connection(
        provider="openai_compatible",
        base_url="http://litellm:4000/v1",
        api_key="sk-test",
        scope=ConnectionScope.USER,
        workspace_id=None,
        user_id=user.id,
    )
    db_session.add(connection)
    await db_session.flush()
    return SimpleNamespace(user=user, workspace=workspace, connection=connection)


async def _model(db_session, owner, model_id: str, **capabilities) -> Model:
    model = Model(connection_id=owner.connection.id, model_id=model_id, **capabilities)
    db_session.add(model)
    await db_session.flush()
    return model


def _image_model(db_session, owner, model_id, **kw):
    return _model(db_session, owner, model_id, supports_image_generation=True, **kw)


def _vision_model(db_session, owner, model_id, **kw):
    return _model(
        db_session,
        owner,
        model_id,
        supports_chat=True,
        supports_image_input=True,
        **kw,
    )


class _FakeImages:
    """The image provider: fails for the listed models, succeeds otherwise."""

    def __init__(self, failing: set[str], exc: Exception | None = None) -> None:
        self.failing = failing
        self.exc = exc
        self.models: list[str] = []

    async def __call__(self, *, prompt, model, **kwargs):
        self.models.append(model)
        if model.rsplit("/", 1)[-1] in self.failing:
            raise self.exc or litellm.exceptions.InternalServerError(
                message="upstream exploded", llm_provider="openai", model=model
            )
        return {"data": [{"b64_json": "aGk="}]}


@pytest.fixture
def images(monkeypatch):
    def install(failing: set[str], exc: Exception | None = None) -> _FakeImages:
        fake = _FakeImages(failing, exc)
        monkeypatch.setattr(generation, "aimage_generation", fake)
        return fake

    return install


async def test_a_failing_image_model_is_retried_once_on_the_fallback(
    db_session, monkeypatch, images
):
    owner = await _owner(db_session)
    primary = await _image_model(db_session, owner, IMAGE_PRIMARY)
    fallback = await _image_model(db_session, owner, IMAGE_FALLBACK)
    owner.workspace.image_gen_model_id = primary.id
    monkeypatch.setenv("IMAGE_GEN_FALLBACK_MODEL", IMAGE_FALLBACK)
    fake = images({IMAGE_PRIMARY})

    result = await generation.generate_image_response(
        db_session, workspace_id=owner.workspace.id, prompt="An owl"
    )

    assert [m.rsplit("/", 1)[-1] for m in fake.models] == [
        IMAGE_PRIMARY,
        IMAGE_FALLBACK,
    ]
    assert result.config_id == fallback.id


async def test_a_working_image_model_never_touches_the_fallback(
    db_session, monkeypatch, images
):
    owner = await _owner(db_session)
    primary = await _image_model(db_session, owner, IMAGE_PRIMARY)
    await _image_model(db_session, owner, IMAGE_FALLBACK)
    owner.workspace.image_gen_model_id = primary.id
    monkeypatch.setenv("IMAGE_GEN_FALLBACK_MODEL", IMAGE_FALLBACK)
    fake = images(set())

    result = await generation.generate_image_response(
        db_session, workspace_id=owner.workspace.id, prompt="An owl"
    )

    assert len(fake.models) == 1
    assert result.config_id == primary.id


@pytest.mark.parametrize(
    "case", ["no-env", "disabled-fallback", "not-image-capable", "not-a-provider-error"]
)
async def test_no_fallback_means_the_original_error(
    db_session, monkeypatch, images, case
):
    owner = await _owner(db_session)
    primary = await _image_model(db_session, owner, IMAGE_PRIMARY)
    if case == "disabled-fallback":
        await _image_model(db_session, owner, IMAGE_FALLBACK, enabled=False)
    elif case == "not-image-capable":
        await _model(db_session, owner, IMAGE_FALLBACK, supports_chat=True)
    else:
        await _image_model(db_session, owner, IMAGE_FALLBACK)
    owner.workspace.image_gen_model_id = primary.id
    if case != "no-env":
        monkeypatch.setenv("IMAGE_GEN_FALLBACK_MODEL", IMAGE_FALLBACK)
    else:
        monkeypatch.delenv("IMAGE_GEN_FALLBACK_MODEL", raising=False)
    exc = RuntimeError("our own bug") if case == "not-a-provider-error" else None
    fake = images({IMAGE_PRIMARY}, exc)

    with pytest.raises(Exception) as raised:
        await generation.generate_image_response(
            db_session, workspace_id=owner.workspace.id, prompt="An owl"
        )

    assert len(fake.models) == 1
    assert "exploded" in str(raised.value) or "our own bug" in str(raised.value)


async def test_the_fallback_is_never_the_primary_model_again(db_session, monkeypatch):
    owner = await _owner(db_session)
    await _image_model(db_session, owner, IMAGE_PRIMARY)
    monkeypatch.setenv("IMAGE_GEN_FALLBACK_MODEL", IMAGE_PRIMARY)

    assert (
        await fallback_model(
            db_session, owner.workspace, "image_gen", primary_model=IMAGE_PRIMARY
        )
        is None
    )


async def test_another_owners_model_is_not_a_fallback(db_session, monkeypatch):
    owner = await _owner(db_session)
    stranger = await _owner(db_session)
    await _image_model(db_session, stranger, IMAGE_FALLBACK)
    monkeypatch.setenv("IMAGE_GEN_FALLBACK_MODEL", IMAGE_FALLBACK)

    assert (
        await fallback_model(
            db_session, owner.workspace, "image_gen", primary_model=IMAGE_PRIMARY
        )
        is None
    )


async def test_the_vision_role_gets_its_fallback_behind_it(db_session, monkeypatch):
    owner = await _owner(db_session)
    primary = await _vision_model(db_session, owner, VISION_PRIMARY)
    await _vision_model(db_session, owner, VISION_FALLBACK)
    owner.workspace.vision_model_id = primary.id
    await db_session.flush()
    monkeypatch.setenv("VISION_FALLBACK_MODEL", VISION_FALLBACK)

    llm = await get_vision_llm(db_session, owner.workspace.id)

    assert llm.runnable.model.endswith(VISION_PRIMARY)
    assert [f.model.rsplit("/", 1)[-1] for f in llm.fallbacks] == [VISION_FALLBACK]


async def test_without_a_vision_fallback_the_role_model_is_returned_as_is(
    db_session, monkeypatch
):
    owner = await _owner(db_session)
    primary = await _vision_model(db_session, owner, VISION_PRIMARY)
    owner.workspace.vision_model_id = primary.id
    await db_session.flush()
    monkeypatch.delenv("VISION_FALLBACK_MODEL", raising=False)

    llm = await get_vision_llm(db_session, owner.workspace.id)

    assert llm.model.endswith(VISION_PRIMARY)
    assert not hasattr(llm, "fallbacks")


async def test_a_failing_vision_reply_is_answered_by_the_fallback(
    db_session, monkeypatch
):
    from langchain_core.language_models.fake_chat_models import FakeListChatModel
    from langchain_core.runnables import RunnableLambda

    owner = await _owner(db_session)
    await _vision_model(db_session, owner, VISION_FALLBACK)
    monkeypatch.setenv("VISION_FALLBACK_MODEL", VISION_FALLBACK)

    def refuse(_messages):
        raise litellm.exceptions.BadRequestError(
            message="image/tiff is not supported", model="x", llm_provider="openai"
        )

    import app.agents.chat.runtime.llm_config as llm_config

    monkeypatch.setattr(
        llm_config,
        "SanitizedChatLiteLLM",
        lambda **_kw: FakeListChatModel(responses=["A chest X-ray."]),
    )

    llm = await with_vision_fallback(
        db_session, owner.workspace.id, RunnableLambda(refuse)
    )
    reply = await llm.ainvoke("describe")

    assert reply.content == "A chest X-ray."
