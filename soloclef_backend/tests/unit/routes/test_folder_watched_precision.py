"""PATCH /folders/{id}/watched rewrote every number in the folder's metadata.

Clearing one flag read the whole JSONB column into a Python dict, put the flag
in it and assigned the dict back, so SQLAlchemy sent the lot through
``json.dumps``. Every number in it came back as the float Python had parsed:
a stored ``0.1234567890123456789012345`` -- which JSONB holds exactly, as a
numeric -- was rewritten ``0.12345678901234568``. A local-folder sync value
nobody was editing lost precision because an unrelated flag was cleared.

The write is now one field, done by Postgres with ``jsonb_set``, so no other
value in the document is re-rendered at all.
"""

from __future__ import annotations

from types import SimpleNamespace
from uuid import uuid4

import pytest
from sqlalchemy.dialects import postgresql

from app.auth.context import AuthContext
from app.db import Folder
from app.routes import folders_routes

pytestmark = pytest.mark.unit

CALLER = SimpleNamespace(id=uuid4(), is_superuser=False, email="caller@example.org")
PRECISE = 0.1234567890123456789012345


class _Session:
    def __init__(self, folder) -> None:
        self.folder = folder
        self.statements: list = []
        self.commits = 0

    async def execute(self, statement):
        self.statements.append(statement)
        folder = self.folder
        return SimpleNamespace(scalars=lambda: SimpleNamespace(first=lambda: folder))

    async def commit(self) -> None:
        self.commits += 1


@pytest.fixture
def world(monkeypatch):
    async def check_permission(*_args, **_kwargs):
        return None

    monkeypatch.setattr(folders_routes, "check_permission", check_permission)
    folder = SimpleNamespace(
        id=4,
        workspace_id=1,
        folder_metadata={"watched": True, "last_scan_seconds": PRECISE},
    )
    return folder, _Session(folder)


async def test_clearing_the_flag_writes_only_that_field(world) -> None:
    _, session = world

    await folders_routes.stop_watching_folder(
        folder_id=4, session=session, auth=AuthContext.session(CALLER)
    )

    update = session.statements[-1]
    compiled = update.compile(dialect=postgresql.dialect())
    sql = str(compiled)
    assert "jsonb_set" in sql
    assert "'{watched}'" in sql
    assert "'false'::jsonb" in sql
    # The folder's own metadata is never a parameter of the write, so nothing
    # in it is re-rendered by the client.
    assert not any(isinstance(value, dict) for value in compiled.params.values()), (
        compiled.params
    )
    assert session.commits == 1


async def test_a_folder_with_no_metadata_is_not_written(world) -> None:
    folder, session = world
    folder.folder_metadata = None

    await folders_routes.stop_watching_folder(
        folder_id=4, session=session, auth=AuthContext.session(CALLER)
    )

    assert len(session.statements) == 1  # the row lookup, and nothing else
    assert session.commits == 1


def test_the_metadata_column_is_jsonb() -> None:
    """``jsonb_set`` is only available because the column is JSONB, not JSON."""
    assert Folder.__table__.c.metadata.type.__class__.__name__ == "JSONB"
