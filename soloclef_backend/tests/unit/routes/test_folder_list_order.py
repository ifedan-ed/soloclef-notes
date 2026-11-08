"""GET /folders asks Postgres for an order it can only guess at.

The route ordered by ``position`` alone, and ``position`` ties: fractional
indexing gives every first child "a0", so a workspace's folders are full of
equal keys. Postgres is free to return tied rows in any order, and it does --
it switches a prepared statement to a generic plan on its fifth execution per
connection, which turned the index scan into a sort and changed the answer the
route gave on the fifth request to nine identical ones.

The order has to be total. Ordering by ``id`` after ``position`` costs nothing
and makes the answer the same every time.
"""

from __future__ import annotations

from types import SimpleNamespace
from uuid import uuid4

import pytest

from app.auth.context import AuthContext
from app.routes import folders_routes

pytestmark = pytest.mark.unit

CALLER = SimpleNamespace(id=uuid4(), is_superuser=False, email="caller@example.org")


class _Session:
    """Remembers the statements it was asked to run."""

    def __init__(self) -> None:
        self.statements: list = []

    async def execute(self, statement):
        self.statements.append(statement)
        return SimpleNamespace(scalars=lambda: SimpleNamespace(all=lambda: []))


@pytest.fixture
def session(monkeypatch):
    async def check_permission(*_args, **_kwargs):
        return None

    monkeypatch.setattr(folders_routes, "check_permission", check_permission)
    return _Session()


async def test_the_listing_is_ordered_by_position_then_id(session) -> None:
    await folders_routes.list_folders(
        workspace_id=1, session=session, auth=AuthContext.session(CALLER)
    )

    [statement] = session.statements
    ordering = [str(clause) for clause in statement._order_by_clauses]
    assert ordering == ["folders.position", "folders.id"]
