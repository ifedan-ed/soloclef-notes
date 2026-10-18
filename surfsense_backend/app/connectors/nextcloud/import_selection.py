"""Turn what was picked in the browser into the files an import will bring in.

A picked file is imported on its own; a picked folder brings every file under
it, keeping its sub-folders, up to a cap so one click cannot pull in a whole
drive. Files the chosen parser cannot read, or that exceed the upload size
limit, are reported rather than imported.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Literal

from app.etl_pipeline.etl_document import DocumentParser
from app.etl_pipeline.parser_selection import files_the_parser_cannot_read
from app.services.folder_service import MAX_FOLDER_DEPTH

from .client import NextcloudClient
from .remote_paths import ROOT, normalize_remote_path, parent_of, path_parts
from .webdav_listing import RemoteEntry

#: Most files one import may bring in; the picker asks to narrow a bigger pick.
MAX_IMPORT_FILES = 200


class SelectionError(ValueError):
    """The pick cannot be imported as it stands (e.g. the whole drive)."""


@dataclass(frozen=True)
class PickedItem:
    path: str
    kind: Literal["file", "folder"]


@dataclass(frozen=True)
class PlannedFile:
    entry: RemoteEntry
    #: SurfSense folder chain the document goes in; empty for a loose file.
    folder_parts: tuple[str, ...]
    #: The picked folder this file came from, or None for a loose file.
    picked_folder: str | None


@dataclass
class ImportPlan:
    files: list[PlannedFile] = field(default_factory=list)
    unsupported: list[str] = field(default_factory=list)
    too_large: list[str] = field(default_factory=list)
    #: True when the pick holds more files than ``MAX_IMPORT_FILES``.
    over_limit: bool = False
    #: Each picked folder's path and the SurfSense folder name it becomes.
    picked_folders: dict[str, tuple[str, ...]] = field(default_factory=dict)

    @property
    def total_bytes(self) -> int:
        return sum(item.entry.size or 0 for item in self.files)


def is_readable(name: str, parser: DocumentParser, *, vision: bool) -> bool:
    """Whether an upload of ``name`` with this parser choice would be accepted."""
    return not files_the_parser_cannot_read([name], parser, vision_requested=vision)


def _folder_chain(picked_folder: str, file_path: str) -> tuple[str, ...]:
    """The picked folder's own name, then the sub-folders down to the file.

    Deeper than SurfSense allows, the extra levels fold into the deepest one.
    """
    base = path_parts(parent_of(picked_folder))
    return tuple(path_parts(parent_of(file_path))[len(base) :][:MAX_FOLDER_DEPTH])


def _unique(items: list[PickedItem]) -> list[PickedItem]:
    seen: set[str] = set()
    unique = []
    for item in items:
        path = normalize_remote_path(item.path)
        if path in seen:
            continue
        seen.add(path)
        unique.append(PickedItem(path=path, kind=item.kind))
    return unique


async def plan_import(
    client: NextcloudClient,
    items: list[PickedItem],
    *,
    parser: DocumentParser,
    vision: bool,
    max_bytes: int,
    max_files: int = MAX_IMPORT_FILES,
) -> ImportPlan:
    plan = ImportPlan()
    seen_paths: set[str] = set()

    def consider(entry: RemoteEntry, picked_folder: str | None) -> None:
        if entry.path in seen_paths:
            return
        seen_paths.add(entry.path)
        if not is_readable(entry.name, parser, vision=vision):
            plan.unsupported.append(entry.path)
            return
        if entry.size is not None and entry.size > max_bytes:
            plan.too_large.append(entry.path)
            return
        if len(plan.files) >= max_files:
            plan.over_limit = True
            return
        chain = _folder_chain(picked_folder, entry.path) if picked_folder else ()
        plan.files.append(
            PlannedFile(entry=entry, folder_parts=chain, picked_folder=picked_folder)
        )

    for item in _unique(items):
        if item.kind == "folder":
            if item.path == ROOT:
                raise SelectionError(
                    "Pick folders or files inside Nextcloud, not the whole drive"
                )
            plan.picked_folders[item.path] = tuple(path_parts(item.path)[-1:])
            # Unreadable files do not count toward the cap, so the walk may
            # look further than it; past this it stops and calls the pick too big.
            files, truncated = await client.walk_files(
                item.path, max_files=max_files * 10
            )
            if truncated:
                plan.over_limit = True
            for entry in files:
                consider(entry, item.path)
        else:
            entry = await client.stat(item.path)
            if entry.is_folder:
                raise SelectionError(f"{entry.name} is a folder, not a file")
            consider(entry, None)
    return plan
