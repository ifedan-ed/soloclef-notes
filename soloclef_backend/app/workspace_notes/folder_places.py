"""Where a note document sits in the workspace's documents tree.

The Notes page groups note documents by their folder, so each group is
named by the folder's path from the top of the tree ("Projects / notes") and
the groups come in the tree's own order: depth first, siblings by
``Folder.position``, the order ``GET /folders`` and the composer's Files &
Folders list use.
"""

from __future__ import annotations

from collections.abc import Iterable
from typing import Protocol

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Folder

from .schemas import FolderRef


class _FolderRow(Protocol):
    id: int
    name: str
    parent_id: int | None


def places_from_folders(rows: Iterable[_FolderRow]) -> dict[int, FolderRef]:
    """``{folder id: its place}`` for a workspace's folders, given in sibling order.

    A folder whose parent is not among ``rows`` counts as a top-level one; a
    folder caught in a parent loop, which the tree can't reach, is named by
    itself and placed after the rest, so every folder still gets a group.
    """
    rows = list(rows)
    known = {row.id for row in rows}
    children: dict[int | None, list[_FolderRow]] = {}
    for row in rows:
        parent = row.parent_id if row.parent_id in known else None
        children.setdefault(parent, []).append(row)

    places: dict[int, FolderRef] = {}

    def place(row: _FolderRow, path: list[str]) -> None:
        places[row.id] = FolderRef(
            id=row.id, name=row.name, path=path, tree_order=len(places)
        )

    stack: list[tuple[_FolderRow, list[str]]] = [
        (row, []) for row in reversed(children.get(None, []))
    ]
    while stack:
        row, above = stack.pop()
        if row.id in places:
            continue
        path = [*above, row.name]
        place(row, path)
        stack.extend((child, path) for child in reversed(children.get(row.id, [])))

    for row in rows:
        if row.id not in places:
            place(row, [row.name])
    return places


async def folder_places(
    session: AsyncSession, workspace_id: int
) -> dict[int, FolderRef]:
    """Every folder of the workspace with its path and place in the tree."""
    rows = (
        await session.execute(
            select(Folder.id, Folder.name, Folder.parent_id)
            .where(Folder.workspace_id == workspace_id)
            .order_by(Folder.position, Folder.id)
        )
    ).all()
    return places_from_folders(rows)
