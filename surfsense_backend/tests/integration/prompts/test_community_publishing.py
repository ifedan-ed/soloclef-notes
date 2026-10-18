"""Community prompts reach every account, so only administrators publish them."""

from __future__ import annotations

import pytest
import pytest_asyncio

from app.config import config
from tests.utils.acting_client import acting_client
from tests.utils.workspace_team import new_user

pytestmark = pytest.mark.integration


@pytest_asyncio.fixture
async def client(db_session):
    async with acting_client(db_session) as http:
        yield http


async def test_members_cannot_publish_but_admins_can(db_session, client, monkeypatch):
    member = await new_user(db_session, "member")
    admin = await new_user(db_session, "admin")
    monkeypatch.setattr(config, "BILLING_ADMIN_EMAILS", admin.email)
    body = {"name": "Plan", "prompt": "Summarise {selection}", "mode": "transform"}

    client.act_as(member)
    refused = await client.post("/api/v1/prompts", json={**body, "is_public": True})
    assert refused.status_code == 403
    private = await client.post("/api/v1/prompts", json=body)
    assert private.status_code == 200
    publish = await client.put(
        f"/api/v1/prompts/{private.json()['id']}", json={"is_public": True}
    )
    assert publish.status_code == 403

    allowed = await client.act_as(admin).post(
        "/api/v1/prompts", json={**body, "is_public": True}
    )
    assert allowed.status_code == 200, allowed.text


async def test_a_prompt_already_public_can_still_be_edited(
    db_session, client, monkeypatch
):
    from app.db import Prompt, PromptMode

    member = await new_user(db_session, "member")
    prompt = Prompt(
        user_id=member.id,
        name="Old public",
        prompt="x",
        mode=PromptMode.transform,
        is_public=True,
    )
    db_session.add(prompt)
    await db_session.flush()
    response = await client.act_as(member).put(
        f"/api/v1/prompts/{prompt.id}", json={"name": "Renamed", "is_public": True}
    )
    assert response.status_code == 200
    private = await client.put(f"/api/v1/prompts/{prompt.id}", json={"is_public": False})
    assert private.status_code == 200
