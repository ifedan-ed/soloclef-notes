"""Which picks can be imported, and the connector's remembered picks.

A pick is remembered on the connector (``selected_files``) so re-syncs keep it
fresh; under ``drive.file`` the app can only ever reach what was picked.
"""

from __future__ import annotations

from typing import Any

from ..file_types import (
    GOOGLE_FOLDER,
    GOOGLE_SHORTCUT,
    is_google_workspace_file,
    should_skip_by_extension,
)
from .schemas import PickedDriveFile


def is_importable(picked: PickedDriveFile) -> bool:
    if picked.mime_type in (GOOGLE_FOLDER, GOOGLE_SHORTCUT):
        return False
    if is_google_workspace_file(picked.mime_type):
        from ..file_types import get_export_mime_type

        return get_export_mime_type(picked.mime_type) is not None
    skip, _ext = should_skip_by_extension(picked.name)
    return not skip


def remember_picks(
    connector_config: dict[str, Any] | None, picks: list[PickedDriveFile]
) -> dict[str, Any]:
    """The config with these picks added to ``selected_files`` (no duplicates)."""
    cfg = dict(connector_config or {})
    known = {f.get("id"): f for f in cfg.get("selected_files") or [] if f.get("id")}
    for picked in picks:
        known[picked.id] = {"id": picked.id, "name": picked.name}
    cfg["selected_files"] = list(known.values())
    return cfg
