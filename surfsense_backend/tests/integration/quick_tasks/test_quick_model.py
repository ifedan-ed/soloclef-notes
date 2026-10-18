"""The quick-tasks model: stored choice, default, fallback, and its route.

Real Postgres, real rows: which model a workspace resolves to depends on
connection scope, ownership and enabled flags, which is exactly what a mock
would get wrong.
"""

from __future__ import annotations

import importlib.util
import uuid
from pathlib import Path
from types import SimpleNamespace

import pytest
from fastapi import HTTPException
from sqlalchemy import text

from app.auth.context import AuthContext
from app.db import Connection, ConnectionScope, Model, User, Workspace
from app.quick_tasks import routes
from app.quick_tasks.llm import get_quick_llm, quick_llm
from app.quick_tasks.resolution import (
    DEFAULT_QUICK_MODEL_ID,
    SAME_AS_CHAT,
    resolve_quick_model,
)
from app.routes.workspaces_routes import create_default_roles_and_membership

pytestmark = pytest.mark.integration

_REVISION = (
    Path(__file__).resolve().parents[3]
    / "alembic"
    / "versions"
    / "c0004_workspace_quick_model.py"
)


async def _owner(db_session) -> SimpleNamespace:
    user = User(
        id=uuid.uuid4(),
        email=f"qm-{uuid.uuid4().hex[:8]}@surfsense.test",
        hashed_password="x",
        is_active=True,
        is_superuser=False,
        is_verified=True,
    )
    db_session.add(user)
    await db_session.flush()
    workspace = Workspace(name="Quick", user_id=user.id, chat_model_id=0)
    db_session.add(workspace)
    await db_session.flush()
    await create_default_roles_and_membership(db_session, workspace.id, user.id)
    await db_session.flush()
    return SimpleNamespace(
        user=user, workspace=workspace, auth=AuthContext.session(user)
    )


async def _model(
    db_session,
    owner,
    model_id: str,
    *,
    scope: ConnectionScope = ConnectionScope.SEARCH_SPACE,
    enabled: bool = True,
    user_id=None,
) -> Model:
    connection = Connection(
        provider="openai_compatible",
        base_url="http://litellm:4000/v1",
        api_key="sk-test",
        scope=scope,
        workspace_id=owner.workspace.id
        if scope == ConnectionScope.SEARCH_SPACE
        else None,
        user_id=user_id or owner.user.id,
    )
    db_session.add(connection)
    await db_session.flush()
    model = Model(
        connection_id=connection.id,
        model_id=model_id,
        supports_chat=True,
        enabled=enabled,
    )
    db_session.add(model)
    await db_session.flush()
    return model


async def _choose(db_session, owner, model_id):
    return await routes.update_quick_model(
        owner.workspace.id,
        routes.QuickModelUpdate(model_id=model_id),
        db_session,
        owner.auth,
    )


async def test_without_a_setting_the_default_is_the_deepseek_flash(db_session):
    owner = await _owner(db_session)
    await _model(db_session, owner, "some-other-model")
    flash = await _model(db_session, owner, DEFAULT_QUICK_MODEL_ID)

    resolved = await resolve_quick_model(db_session, owner.workspace)

    assert resolved.id == flash.id and resolved.source == "default"
    llm = await quick_llm(db_session, owner.workspace.id)
    assert llm.model == f"openai/{DEFAULT_QUICK_MODEL_ID}"
    # Thinking off travels in the body, where the LiteLLM proxy passes it on.
    assert llm.model_kwargs["extra_body"] == {"thinking": {"type": "disabled"}}


async def test_the_workspace_connection_wins_over_the_owners(db_session):
    owner = await _owner(db_session)
    await _model(db_session, owner, DEFAULT_QUICK_MODEL_ID, scope=ConnectionScope.USER)
    own = await _model(db_session, owner, DEFAULT_QUICK_MODEL_ID)

    assert (await resolve_quick_model(db_session, owner.workspace)).id == own.id


async def test_a_disabled_or_foreign_default_is_not_used(db_session):
    owner = await _owner(db_session)
    stranger = await _owner(db_session)
    await _model(db_session, owner, DEFAULT_QUICK_MODEL_ID, enabled=False)
    await _model(db_session, stranger, DEFAULT_QUICK_MODEL_ID)

    assert await resolve_quick_model(db_session, owner.workspace) is None


async def test_without_any_quick_model_the_chat_model_is_used(db_session, monkeypatch):
    owner = await _owner(db_session)
    chat = object()

    async def agent_llm(_session, workspace_id, disable_streaming=False):
        assert workspace_id == owner.workspace.id and disable_streaming
        return chat

    monkeypatch.setattr("app.quick_tasks.llm.get_agent_llm", agent_llm)
    assert await get_quick_llm(db_session, owner.workspace.id) is chat


async def test_a_chosen_model_wins_and_same_as_chat_opts_out(db_session):
    owner = await _owner(db_session)
    await _model(db_session, owner, DEFAULT_QUICK_MODEL_ID)
    gemini = await _model(db_session, owner, "google-gemini-3.8-flash")

    read = await _choose(db_session, owner, gemini.id)
    assert read.model_id == gemini.id
    resolved = await resolve_quick_model(db_session, owner.workspace)
    assert (resolved.id, resolved.source) == (gemini.id, "setting")
    # No thinking switch for a model not known to take it.
    assert (
        "extra_body"
        not in (await quick_llm(db_session, owner.workspace.id)).model_kwargs
    )

    read = await _choose(db_session, owner, SAME_AS_CHAT)
    assert read.model_id == SAME_AS_CHAT
    assert await resolve_quick_model(db_session, owner.workspace) is None

    read = await _choose(db_session, owner, None)
    assert read.model_id is None and read.default_model_id is not None


async def test_a_chosen_model_that_disappears_falls_back_to_the_default(db_session):
    owner = await _owner(db_session)
    flash = await _model(db_session, owner, DEFAULT_QUICK_MODEL_ID)
    chosen = await _model(db_session, owner, "going-away")
    await _choose(db_session, owner, chosen.id)

    chosen.enabled = False
    await db_session.flush()

    assert (await resolve_quick_model(db_session, owner.workspace)).id == flash.id


async def test_the_route_refuses_an_unusable_or_foreign_model(db_session):
    owner = await _owner(db_session)
    stranger = await _owner(db_session)
    foreign = await _model(db_session, stranger, "theirs")
    off = await _model(db_session, owner, "off", enabled=False)

    for model_id in (foreign.id, off.id, 999_999):
        with pytest.raises(HTTPException) as refused:
            await _choose(db_session, owner, model_id)
        assert refused.value.status_code == 400


async def test_only_members_can_read_or_change_it(db_session):
    owner = await _owner(db_session)
    stranger = await _owner(db_session)
    with pytest.raises(HTTPException) as refused:
        await routes.get_quick_model(owner.workspace.id, db_session, stranger.auth)
    assert refused.value.status_code == 403
    with pytest.raises(HTTPException):
        await routes.update_quick_model(
            owner.workspace.id,
            routes.QuickModelUpdate(model_id=0),
            db_session,
            stranger.auth,
        )


async def test_the_migration_creates_the_table_it_declares(db_session):
    spec = importlib.util.spec_from_file_location("c0004", _REVISION)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    connection = await db_session.connection()
    await connection.execute(text("DROP TABLE workspace_quick_models"))

    statements: list[str] = []
    module.op = SimpleNamespace(execute=statements.append)
    module.upgrade()
    for statement in statements:
        await connection.execute(text(statement))
    owner = await _owner(db_session)
    await _choose(db_session, owner, SAME_AS_CHAT)
    assert (
        await routes.get_quick_model(owner.workspace.id, db_session, owner.auth)
    ).model_id == 0
