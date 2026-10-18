"""Which Picker picks are imported, and how picks are remembered on the connector."""

import pytest

from app.connectors.google_drive.notebook_import.picked_files import (
    is_importable,
    remember_picks,
)
from app.connectors.google_drive.notebook_import.schemas import PickedDriveFile

pytestmark = pytest.mark.unit


def _pick(name: str, mime: str = "", file_id: str = "fileid000001") -> PickedDriveFile:
    return PickedDriveFile(id=file_id, name=name, mime_type=mime)


@pytest.mark.parametrize(
    ("name", "mime"),
    [
        ("notes.md", "text/markdown"),
        ("paper.pdf", "application/pdf"),
        ("Plan", "application/vnd.google-apps.document"),
        ("Budget", "application/vnd.google-apps.spreadsheet"),
        ("Deck", "application/vnd.google-apps.presentation"),
    ],
)
def test_documents_and_google_files_are_importable(name, mime):
    assert is_importable(_pick(name, mime))


@pytest.mark.parametrize(
    ("name", "mime"),
    [
        ("Folder", "application/vnd.google-apps.folder"),
        ("Shortcut", "application/vnd.google-apps.shortcut"),
        ("Form", "application/vnd.google-apps.form"),
    ],
)
def test_folders_shortcuts_and_unexportable_google_types_are_not(name, mime):
    assert not is_importable(_pick(name, mime))


def test_picks_join_the_remembered_ones_without_duplicates():
    config = {"selected_files": [{"id": "fileid000001", "name": "old.md"}], "x": 1}
    merged = remember_picks(
        config,
        [
            _pick("renamed.md", file_id="fileid000001"),
            _pick("b.md", file_id="fileid000002"),
        ],
    )
    assert merged["selected_files"] == [
        {"id": "fileid000001", "name": "renamed.md"},
        {"id": "fileid000002", "name": "b.md"},
    ]
    assert merged["x"] == 1
    assert config["selected_files"] == [{"id": "fileid000001", "name": "old.md"}]


def test_a_file_id_must_look_like_a_drive_id():
    with pytest.raises(ValueError):
        PickedDriveFile(id="../../etc", name="x")
