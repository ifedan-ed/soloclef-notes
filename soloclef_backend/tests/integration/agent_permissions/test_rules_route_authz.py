"""The agent-permission rule routes authorise with the caller's AuthContext.

All four routes passed the bare ``User`` where ``check_permission`` expects an
``AuthContext`` and reads ``auth.user``, so every call died with
AttributeError -> 500 before any permission was checked; the settings page
showed "An internal error occurred" and no rule could be listed or saved.
"""

from __future__ import annotations

import pytest
from fastapi import HTTPException
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.routes import agent_permissions_route as route

pytestmark = pytest.mark.integration


@pytest.fixture(autouse=True)
def _rules_enabled(monkeypatch):
    # The feature flag is deployment config, not what is under test here.
    monkeypatch.setattr(route, "_flag_guard", lambda: None)


async def test_owner_can_list_rules(db_session: AsyncSession, db_user, db_workspace):
    rules = await route.list_rules(
        workspace_id=db_workspace.id,
        session=db_session,
        auth=AuthContext.session(db_user),
    )
    assert rules == []


async def test_non_member_is_refused_not_crashed(
    db_session: AsyncSession, db_workspace, make_user
):
    outsider = await make_user()
    with pytest.raises(HTTPException) as refused:
        await route.list_rules(
            workspace_id=db_workspace.id,
            session=db_session,
            auth=AuthContext.session(outsider),
        )
    assert refused.value.status_code == 403


async def test_owner_can_create_then_delete_a_rule(
    db_session: AsyncSession, db_user, db_workspace
):
    auth = AuthContext.session(db_user)
    created = await route.create_rule(
        workspace_id=db_workspace.id,
        payload=route.AgentPermissionRuleCreate(
            permission="tool:web_search",
            pattern="*",
            action="ask",
        ),
        session=db_session,
        auth=auth,
    )
    listed = await route.list_rules(
        workspace_id=db_workspace.id, session=db_session, auth=auth
    )
    assert [r.id for r in listed] == [created.id]

    await route.delete_rule(
        workspace_id=db_workspace.id,
        rule_id=created.id,
        session=db_session,
        auth=auth,
    )
    assert (
        await route.list_rules(
            workspace_id=db_workspace.id, session=db_session, auth=auth
        )
        == []
    )
