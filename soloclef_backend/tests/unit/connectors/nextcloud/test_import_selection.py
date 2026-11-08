"""What a pick in the browser turns into: files, folders to mirror, and refusals."""

import httpx
import pytest

from app.connectors.nextcloud.import_selection import (
    PickedItem,
    SelectionError,
    plan_import,
)
from app.etl_pipeline.etl_document import DocumentParser

from .fake_server import DAV, FakeNextcloud, multistatus

pytestmark = pytest.mark.unit


def _server() -> FakeNextcloud:
    server = FakeNextcloud()
    server.reply(
        "PROPFIND", f"{DAV}/Open Notebook", multistatus("propfind_open_notebook.xml")
    )
    server.reply(
        "PROPFIND", f"{DAV}/Open Notebook/Week 1", multistatus("propfind_week1.xml")
    )
    return server


async def test_a_folder_brings_its_readable_files_and_keeps_its_structure():
    plan = await plan_import(
        _server().client(),
        [PickedItem("/Open Notebook", "folder")],
        parser=DocumentParser.LOCAL,
        vision=False,
        max_bytes=50 * 1024 * 1024,
    )
    chains = {f.entry.path: f.folder_parts for f in plan.files}
    assert chains == {
        "/Open Notebook/Anatomy notes.pdf": ("Open Notebook",),
        "/Open Notebook/Week 1/fluids.md": ("Open Notebook", "Week 1"),
    }
    assert plan.unsupported == ["/Open Notebook/Week 1/clip.mkv"]
    assert plan.picked_folders == {"/Open Notebook": ("Open Notebook",)}
    assert not plan.over_limit


async def test_files_over_the_size_limit_are_reported_not_planned():
    plan = await plan_import(
        _server().client(),
        [PickedItem("/Open Notebook", "folder")],
        parser=DocumentParser.LOCAL,
        vision=False,
        max_bytes=1024,
    )
    assert [f.entry.name for f in plan.files] == ["fluids.md"]
    assert plan.too_large == ["/Open Notebook/Anatomy notes.pdf"]


async def test_more_readable_files_than_the_cap_marks_the_pick_too_big():
    plan = await plan_import(
        _server().client(),
        [PickedItem("/Open Notebook", "folder")],
        parser=DocumentParser.LOCAL,
        vision=False,
        max_bytes=50 * 1024 * 1024,
        max_files=1,
    )
    assert plan.over_limit
    assert len(plan.files) == 1


async def test_a_loose_file_has_no_folder_and_a_file_picked_twice_counts_once():
    server = _server()
    server.reply(
        "PROPFIND",
        f"{DAV}/Open Notebook/Week 1/fluids.md",
        httpx.Response(
            207,
            content=(
                b'<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"><d:response>'
                b"<d:href>/remote.php/dav/files/alice/Open%20Notebook/Week%201/fluids.md</d:href>"
                b"<d:propstat><d:prop><d:resourcetype/><d:getcontentlength>12</d:getcontentlength>"
                b"<d:getetag>&quot;f1&quot;</d:getetag></d:prop>"
                b"<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>"
            ),
        ),
    )
    plan = await plan_import(
        server.client(),
        [
            PickedItem("/Open Notebook/Week 1/fluids.md", "file"),
            PickedItem("Open Notebook/Week 1/fluids.md/", "file"),
        ],
        parser=DocumentParser.LOCAL,
        vision=False,
        max_bytes=1024,
    )
    assert [(f.entry.path, f.folder_parts, f.picked_folder) for f in plan.files] == [
        ("/Open Notebook/Week 1/fluids.md", (), None)
    ]


async def test_the_whole_drive_cannot_be_picked_as_a_folder():
    with pytest.raises(SelectionError):
        await plan_import(
            _server().client(),
            [PickedItem("/", "folder")],
            parser=DocumentParser.LOCAL,
            vision=False,
            max_bytes=1024,
        )
