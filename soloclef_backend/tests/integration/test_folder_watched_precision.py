"""Clearing a folder's watched flag leaves every other value's bytes alone.

JSONB holds a number as a `numeric`, so a stored
``0.1234567890123456789012345`` is exact in the column. The route read the whole
column into a Python dict, set one key and assigned the dict back, so
SQLAlchemy re-rendered the lot with ``json.dumps`` and the value came back
``0.12345678901234568``. The flag is now written by Postgres with ``jsonb_set``,
which touches nothing else.
"""

from __future__ import annotations

import uuid

import pytest
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import User, Workspace
from app.routes.folders_routes import stop_watching_folder
from app.routes.workspaces_routes import create_default_roles_and_membership

pytestmark = pytest.mark.integration

PRECISE = "0.1234567890123456789012345"
METADATA = '{"watched": true, "last_scan_seconds": ' + PRECISE + ', "source": "local"}'


@pytest.fixture
async def watched_folder(db_session: AsyncSession):
    owner = User(
        id=uuid.uuid4(),
        email=f"watched-{uuid.uuid4()}@soloclef.test",
        hashed_password="x",
        is_active=True,
        is_superuser=False,
        is_verified=True,
    )
    db_session.add(owner)
    await db_session.flush()
    workspace = Workspace(name=f"Space {uuid.uuid4().hex[:8]}", user_id=owner.id)
    db_session.add(workspace)
    await db_session.flush()
    await create_default_roles_and_membership(db_session, workspace.id, owner.id)
    folder_id = await db_session.scalar(
        text(
            "INSERT INTO folders (name, position, workspace_id, metadata, "
            "created_at, updated_at) VALUES ('Watched', 'a0', :workspace, "
            "cast(:metadata AS jsonb), now(), now()) RETURNING id"
        ),
        {"workspace": workspace.id, "metadata": METADATA},
    )
    await db_session.flush()
    return AuthContext.session(owner), folder_id


async def _stored(session: AsyncSession, folder_id: int) -> str:
    return await session.scalar(
        text("SELECT cast(metadata AS text) FROM folders WHERE id = :id"), {"id": folder_id}
    )


async def test_the_flag_is_cleared_without_rewriting_the_other_values(
    db_session: AsyncSession, watched_folder
) -> None:
    auth, folder_id = watched_folder
    assert PRECISE in await _stored(db_session, folder_id)

    answer = await stop_watching_folder(
        folder_id=folder_id, session=db_session, auth=auth
    )

    stored = await _stored(db_session, folder_id)
    assert answer == {"message": "Folder watch status updated"}
    assert '"watched": false' in stored
    assert PRECISE in stored
    assert '"source": "local"' in stored
