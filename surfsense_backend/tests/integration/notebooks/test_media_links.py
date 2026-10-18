"""Media links: issued only for the viewer's own workspace media, and re-checked on use."""

from __future__ import annotations

import time
from collections.abc import AsyncGenerator
from types import SimpleNamespace

import httpx
import pytest
import pytest_asyncio
from httpx import ASGITransport
from sqlalchemy import delete

from app.app import app
from app.artifacts.media_links.token import (
    MediaLinkClaim,
    issue_media_token,
    read_media_token,
)
from app.auth.context import AuthContext
from app.config import config as app_config
from app.db import WorkspaceMembership, get_async_session
from app.users import get_auth_context

pytestmark = pytest.mark.integration

AUDIO = b"ID3" + b"\x00" * 400


@pytest_asyncio.fixture
async def as_user(db_session) -> AsyncGenerator[SimpleNamespace, None]:
    """An HTTP client whose principal the test sets with ``as_user.auth = ...``."""
    state = SimpleNamespace(auth=None)

    async def override_session():
        yield db_session

    async def override_auth() -> AuthContext:
        return state.auth

    previous = app.dependency_overrides.copy()
    app.dependency_overrides[get_async_session] = override_session
    app.dependency_overrides[get_auth_context] = override_auth
    try:
        async with httpx.AsyncClient(
            transport=ASGITransport(app=app), base_url="http://test"
        ) as client:
            state.client = client
            yield state
    finally:
        app.dependency_overrides.clear()
        app.dependency_overrides.update(previous)


async def _audio_output(make_owner, make_notebook, make_studio_output):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    made = await make_studio_output(
        notebook,
        kind="audio",
        artifact_format="podcast",
        files=(("overview.mp3", "audio/mpeg", AUDIO),),
    )
    return owner, made


async def _links(as_user, workspace_id, refs):
    return await as_user.client.post(
        f"/api/v1/workspaces/{workspace_id}/artifacts/media-links",
        json={"files": [{"artifact_id": a, "file_id": f} for a, f in refs]},
    )


async def test_a_link_plays_the_file_with_ranges_and_no_session(
    as_user, make_owner, make_notebook, make_studio_output
):
    owner, made = await _audio_output(make_owner, make_notebook, make_studio_output)
    as_user.auth = owner.auth
    response = await _links(
        as_user, owner.workspace.id, [(made.artifact.id, made.files[0].id)]
    )
    assert response.status_code == 200
    (link,) = response.json()["links"]
    assert link["url"].startswith("/api/v1/public/artifact-media/")
    assert link["mime_type"] == "audio/mpeg"

    as_user.auth = None  # the player in the sandboxed report has no session
    full = await as_user.client.get(link["url"])
    assert full.status_code == 200 and full.content == AUDIO
    part = await as_user.client.get(link["url"], headers={"Range": "bytes=0-2"})
    assert part.status_code == 206 and part.content == b"ID3"
    assert part.headers["referrer-policy"] == "no-referrer"


async def test_links_skip_other_workspaces_and_non_media(
    as_user, make_owner, make_notebook, make_studio_output
):
    owner, made = await _audio_output(make_owner, make_notebook, make_studio_output)
    stranger, foreign = await _audio_output(
        make_owner, make_notebook, make_studio_output
    )
    notebook = await make_notebook(owner.workspace.id)
    quiz = await make_studio_output(
        notebook,
        kind="quiz",
        artifact_format="quiz",
        files=(("quiz.json", "application/json", b"{}"),),
    )
    as_user.auth = owner.auth

    response = await _links(
        as_user,
        owner.workspace.id,
        [
            (made.artifact.id, made.files[0].id),
            (foreign.artifact.id, foreign.files[0].id),
            (quiz.artifact.id, quiz.files[0].id),
            (made.artifact.id, 999_999),
        ],
    )

    assert [link["file_id"] for link in response.json()["links"]] == [made.files[0].id]

    # Asking in the stranger's workspace is refused outright.
    denied = await _links(
        as_user, stranger.workspace.id, [(foreign.artifact.id, foreign.files[0].id)]
    )
    assert denied.status_code == 403


async def test_pat_principals_cannot_mint_links(
    as_user, make_owner, make_notebook, make_studio_output
):
    owner, made = await _audio_output(make_owner, make_notebook, make_studio_output)
    as_user.auth = AuthContext.pat_auth(owner.user, SimpleNamespace(scopes=[]))
    response = await _links(
        as_user, owner.workspace.id, [(made.artifact.id, made.files[0].id)]
    )
    assert response.status_code == 403


async def test_forged_expired_or_revoked_links_are_404(
    as_user, db_session, make_owner, make_notebook, make_studio_output
):
    owner, made = await _audio_output(make_owner, make_notebook, make_studio_output)
    claim = MediaLinkClaim(
        user_id=str(owner.user.id),
        workspace_id=owner.workspace.id,
        artifact_id=made.artifact.id,
        file_id=made.files[0].id,
        expires_at=int(time.time()) + 60,
    )
    good = issue_media_token(claim, secret_key=app_config.SECRET_KEY)
    as_user.auth = None
    base = "/api/v1/public/artifact-media/"
    assert (await as_user.client.get(base + good)).status_code == 200

    forged = issue_media_token(claim, secret_key="someone-elses-key")
    expired = issue_media_token(
        MediaLinkClaim(**{**claim.__dict__, "expires_at": int(time.time()) - 1}),
        secret_key=app_config.SECRET_KEY,
    )
    retargeted = good.split(".")[0][:-2] + "xx." + good.split(".")[1]
    for token in (forged, expired, retargeted, "garbage"):
        assert (await as_user.client.get(base + token)).status_code == 404

    # Removed from the workspace: the same, still-unexpired link stops working.
    await db_session.execute(
        delete(WorkspaceMembership).where(
            WorkspaceMembership.workspace_id == owner.workspace.id
        )
    )
    await db_session.flush()
    assert (await as_user.client.get(base + good)).status_code == 404


def test_tokens_round_trip_and_expire():
    claim = MediaLinkClaim("u", 1, 2, 3, expires_at=100)
    token = issue_media_token(claim, secret_key="k")
    assert read_media_token(token, secret_key="k", now=99) == claim
    assert read_media_token(token, secret_key="k", now=101) is None
    assert read_media_token(token, secret_key="other", now=99) is None
