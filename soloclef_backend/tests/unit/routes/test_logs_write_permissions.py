"""Writing a log asks for a write permission, not for ``logs:read``.

The four single-log routes all gated on ``logs:read`` except the delete, so
anyone who could *read* a workspace's activity log could also append to it and
rewrite an existing entry — level, message, metadata and all. A Viewer could
forge an audit trail. The log is the record of what happened in a workspace, so
appending to it and editing it are their own permissions.
"""

from __future__ import annotations

from types import SimpleNamespace
from uuid import uuid4

import pytest
from fastapi import HTTPException

from app.auth.context import AuthContext
from app.db import LogLevel, LogStatus, Permission
from app.routes import logs_routes
from app.schemas import LogCreate, LogUpdate

pytestmark = pytest.mark.unit

CALLER = SimpleNamespace(id=uuid4(), is_superuser=False, email="caller@example.org")


class _Session:
    """A session that answers one stored log and remembers what it was told."""

    def __init__(self, log=None) -> None:
        self.log = log
        self.commits = 0
        self.added: list = []
        self.deleted: list = []

    async def execute(self, _statement):
        log = self.log
        return SimpleNamespace(scalars=lambda: SimpleNamespace(first=lambda: log))

    def add(self, row) -> None:
        self.added.append(row)

    async def commit(self) -> None:
        self.commits += 1

    async def refresh(self, _row) -> None:
        pass

    async def delete(self, row) -> None:
        self.deleted.append(row)

    async def rollback(self) -> None:
        pass


@pytest.fixture
def asked(monkeypatch):
    """Record every (workspace_id, permission) the route checks."""
    checks: list[tuple[int, str]] = []

    async def check_permission(_session, _auth, workspace_id, permission, *_args):
        checks.append((workspace_id, permission))

    monkeypatch.setattr(logs_routes, "check_permission", check_permission)
    return checks


def _auth() -> AuthContext:
    return AuthContext.session(CALLER)


def _stored_log():
    return SimpleNamespace(
        id=7,
        workspace_id=3,
        level=LogLevel.INFO,
        status=LogStatus.SUCCESS,
        message="indexed",
        source="indexer",
        log_metadata=None,
    )


async def test_creating_a_log_asks_for_logs_create(asked):
    session = _Session()

    await logs_routes.create_log(
        log=LogCreate(
            level=LogLevel.INFO,
            status=LogStatus.SUCCESS,
            message="indexed",
            workspace_id=3,
        ),
        session=session,
        auth=_auth(),
    )

    assert asked == [(3, Permission.LOGS_CREATE.value)]


async def test_updating_a_log_asks_for_logs_update(asked):
    session = _Session(_stored_log())

    await logs_routes.update_log(
        log_id=7,
        log_update=LogUpdate(message="rewritten"),
        session=session,
        auth=_auth(),
    )

    assert asked == [(3, Permission.LOGS_UPDATE.value)]


async def test_reading_a_log_still_asks_only_for_logs_read(asked):
    session = _Session(_stored_log())

    await logs_routes.read_log(log_id=7, session=session, auth=_auth())

    assert asked == [(3, Permission.LOGS_READ.value)]


async def test_deleting_a_log_still_asks_for_logs_delete(asked):
    session = _Session(_stored_log())

    await logs_routes.delete_log(log_id=7, session=session, auth=_auth())

    assert asked == [(3, Permission.LOGS_DELETE.value)]


async def test_a_role_that_may_only_read_logs_cannot_write_one(monkeypatch):
    """The whole point, stated as the refusal a Viewer now gets."""

    async def check_permission(_session, _auth, _workspace_id, permission, message):
        if permission != Permission.LOGS_READ.value:
            raise HTTPException(status_code=403, detail=message)

    monkeypatch.setattr(logs_routes, "check_permission", check_permission)

    with pytest.raises(HTTPException) as refused:
        await logs_routes.create_log(
            log=LogCreate(
                level=LogLevel.ERROR,
                status=LogStatus.FAILED,
                message="forged",
                workspace_id=3,
            ),
            session=_Session(),
            auth=_auth(),
        )

    assert refused.value.status_code == 403


def test_the_two_write_permissions_are_offered_to_a_role():
    """A permission a role editor cannot see is a permission nobody can grant."""
    from app.routes.rbac_routes import PERMISSION_DESCRIPTIONS

    assert Permission.LOGS_CREATE.value in PERMISSION_DESCRIPTIONS
    assert Permission.LOGS_UPDATE.value in PERMISSION_DESCRIPTIONS


def test_no_default_role_but_the_owner_may_write_logs():
    """Nobody silently gains the ability; the owner's ``*`` keeps it."""
    from app.db import DEFAULT_ROLE_PERMISSIONS

    assert DEFAULT_ROLE_PERMISSIONS["Owner"] == [Permission.FULL_ACCESS.value]
    for role in ("Editor", "Viewer"):
        assert Permission.LOGS_CREATE.value not in DEFAULT_ROLE_PERMISSIONS[role]
        assert Permission.LOGS_UPDATE.value not in DEFAULT_ROLE_PERMISSIONS[role]
