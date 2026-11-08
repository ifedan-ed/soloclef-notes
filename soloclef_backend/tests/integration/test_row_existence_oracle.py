"""A row in a workspace you are not in answers as a row that does not exist.

The three single-log routes and all seven single-folder routes loaded the row
before anyone was asked whether the caller may see it. The permission check
then refused with 403, while an id that was not a row had already been answered
404 -- so any caller could ask ``GET /api/v1/logs/<n>`` or
``GET /api/v1/folders/<n>`` and read the difference. That told them which log
and folder ids exist across every workspace of the deployment, their own or
not, and how many of each there are.

Membership is now part of the lookup, so the two cases are one answer. A
caller who *is* a member but lacks the permission still gets 403: that is the
permission being refused, not an existence leak -- they already know the
workspace.
"""

from __future__ import annotations

import uuid

import pytest
from fastapi import HTTPException
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import (
    Folder,
    Log,
    LogLevel,
    LogStatus,
    User,
    Workspace,
    WorkspaceMembership,
    WorkspaceRole,
)
from app.routes.folders_routes import (
    delete_folder,
    get_folder,
    get_folder_breadcrumb,
    move_folder,
    reorder_folder,
    stop_watching_folder,
    update_folder,
)
from app.routes.logs_routes import delete_log, read_log, update_log
from app.routes.workspaces_routes import create_default_roles_and_membership
from app.schemas import FolderMove, FolderReorder, FolderUpdate, LogUpdate

pytestmark = pytest.mark.integration

ABSENT_ID = 9_999_999


async def _user(session: AsyncSession) -> User:
    user = User(
        id=uuid.uuid4(),
        email=f"oracle-{uuid.uuid4()}@soloclef.test",
        hashed_password="x",
        is_active=True,
        is_superuser=False,
        is_verified=True,
    )
    session.add(user)
    await session.flush()
    return user


async def _workspace(session: AsyncSession, owner: User) -> Workspace:
    workspace = Workspace(name=f"Space {uuid.uuid4().hex[:8]}", user_id=owner.id)
    session.add(workspace)
    await session.flush()
    await create_default_roles_and_membership(session, workspace.id, owner.id)
    await session.flush()
    return workspace


@pytest.fixture
async def world(db_session: AsyncSession):
    """A stranger, and someone else's workspace holding a log and a folder."""
    stranger = await _user(db_session)
    await _workspace(db_session, stranger)

    owner = await _user(db_session)
    theirs = await _workspace(db_session, owner)

    log = Log(
        level=LogLevel.INFO,
        status=LogStatus.SUCCESS,
        message="indexed",
        source="indexer",
        workspace_id=theirs.id,
    )
    folder = Folder(
        name="Reports",
        position="a0",
        workspace_id=theirs.id,
        created_by_id=owner.id,
    )
    db_session.add_all([log, folder])
    await db_session.flush()

    return AuthContext.session(stranger), theirs, log, folder, stranger


def _refusal(error: HTTPException) -> tuple[int, str]:
    return error.status_code, str(error.detail)


async def _refused(call) -> tuple[int, str]:
    with pytest.raises(HTTPException) as raised:
        await call
    return _refusal(raised.value)


class TestLogs:
    async def test_a_log_of_another_workspace_reads_as_absent(
        self, db_session: AsyncSession, world
    ) -> None:
        auth, _, log, _, _ = world

        theirs = await _refused(read_log(log.id, db_session, auth))
        nothing = await _refused(read_log(ABSENT_ID, db_session, auth))

        assert theirs == nothing == (404, "Log not found")

    async def test_updating_one_reads_as_absent(
        self, db_session: AsyncSession, world
    ) -> None:
        auth, _, log, _, _ = world
        changes = LogUpdate(message="rewritten")

        theirs = await _refused(update_log(log.id, changes, db_session, auth))
        nothing = await _refused(update_log(ABSENT_ID, changes, db_session, auth))

        assert theirs == nothing == (404, "Log not found")
        await db_session.refresh(log)
        assert log.message == "indexed"

    async def test_deleting_one_reads_as_absent(
        self, db_session: AsyncSession, world
    ) -> None:
        auth, _, log, _, _ = world

        theirs = await _refused(delete_log(log.id, db_session, auth))
        nothing = await _refused(delete_log(ABSENT_ID, db_session, auth))

        assert theirs == nothing == (404, "Log not found")
        await db_session.refresh(log)

    async def test_a_member_without_the_permission_is_still_told_403(
        self, db_session: AsyncSession, world
    ) -> None:
        auth, theirs, log, _, stranger = db_session and world
        role = WorkspaceRole(
            workspace_id=theirs.id,
            name=f"Nothing {uuid.uuid4().hex[:6]}",
            permissions=[],
        )
        db_session.add(role)
        await db_session.flush()
        db_session.add(
            WorkspaceMembership(
                user_id=stranger.id,
                workspace_id=theirs.id,
                role_id=role.id,
                is_owner=False,
            )
        )
        await db_session.flush()

        status, detail = await _refused(read_log(log.id, db_session, auth))

        assert status == 403
        assert "permission" in detail


class TestFolders:
    async def test_every_single_folder_route_reads_as_absent(
        self, db_session: AsyncSession, world
    ) -> None:
        auth, _, _, folder, _ = world

        calls = {
            "get": lambda folder_id: get_folder(folder_id, db_session, auth),
            "breadcrumb": lambda folder_id: get_folder_breadcrumb(
                folder_id, db_session, auth
            ),
            "watched": lambda folder_id: stop_watching_folder(
                folder_id, db_session, auth
            ),
            "rename": lambda folder_id: update_folder(
                folder_id, FolderUpdate(name="Renamed"), db_session, auth
            ),
            "move": lambda folder_id: move_folder(
                folder_id, FolderMove(new_parent_id=None), db_session, auth
            ),
            "reorder": lambda folder_id: reorder_folder(
                folder_id, FolderReorder(), db_session, auth
            ),
            "delete": lambda folder_id: delete_folder(folder_id, db_session, auth),
        }

        for name, call in calls.items():
            theirs = await _refused(call(folder.id))
            nothing = await _refused(call(ABSENT_ID))
            assert theirs == nothing == (404, "Folder not found"), name

        await db_session.refresh(folder)
        assert folder.name == "Reports"
