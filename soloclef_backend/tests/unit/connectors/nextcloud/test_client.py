"""The read-only WebDAV client against recorded Nextcloud replies."""

import os

import httpx
import pytest

from app.connectors.nextcloud.client import (
    NextcloudAuthError,
    NextcloudFileTooLargeError,
    NextcloudNotFoundError,
    NextcloudUnavailableError,
)

from .fake_server import DAV, FakeNextcloud, fixture, multistatus

pytestmark = pytest.mark.unit


async def test_listing_a_folder_sends_a_depth_one_propfind_with_basic_auth():
    server = FakeNextcloud()
    server.reply(
        "PROPFIND", f"{DAV}/Open Notebook", multistatus("propfind_open_notebook.xml")
    )

    entries = await server.client().list_folder("/Open Notebook/")

    assert [e.name for e in entries] == ["Week 1", "Anatomy notes.pdf"]
    request = server.requests[0]
    assert request.headers["Depth"] == "1"
    assert request.headers["Authorization"].startswith("Basic ")
    assert str(request.url).endswith("/remote.php/dav/files/alice/Open%20Notebook")


async def test_a_refused_login_is_an_auth_error():
    server = FakeNextcloud()
    server.reply(
        "PROPFIND",
        f"{DAV}/",
        httpx.Response(401, content=fixture("error_unauthorized.xml")),
    )
    with pytest.raises(NextcloudAuthError):
        await server.client().list_folder("/")


async def test_a_missing_path_is_not_found():
    server = FakeNextcloud()
    with pytest.raises(NextcloudNotFoundError):
        await server.client().stat("/Missing")


async def test_slow_down_replies_are_retried_after_the_servers_wait_capped():
    server = FakeNextcloud()
    server.reply(
        "PROPFIND",
        f"{DAV}/",
        httpx.Response(429, headers={"Retry-After": "120"}),
        httpx.Response(503),
        multistatus("propfind_root.xml"),
    )
    client = server.client(max_retry_wait=5.0)
    entries = await client.list_folder("/")
    assert len(entries) == 5
    assert server.waits == [5.0, 4.0]


async def test_a_server_that_keeps_refusing_gives_up():
    server = FakeNextcloud()
    server.reply("PROPFIND", f"{DAV}/", httpx.Response(503))
    with pytest.raises(NextcloudUnavailableError):
        await server.client(max_attempts=3).list_folder("/")
    assert len(server.requests) == 3


async def test_a_timeout_is_reported_as_unavailable():
    def slow(request: httpx.Request) -> httpx.Response:
        raise httpx.ReadTimeout("slow", request=request)

    from app.connectors.nextcloud.client import NextcloudClient

    from .fake_server import ACCOUNT

    async def no_sleep(_: float) -> None:
        return None

    client = NextcloudClient(
        ACCOUNT, transport=httpx.MockTransport(slow), sleep=no_sleep
    )
    with pytest.raises(NextcloudUnavailableError):
        await client.list_folder("/")


async def test_search_asks_the_whole_account_and_keeps_only_its_own_files():
    server = FakeNextcloud()
    server.reply("SEARCH", "/remote.php/dav/", multistatus("search_reply.xml"))

    entries = await server.client().search("note%book")

    assert [e.path for e in entries] == [
        "/Backups/Old Notebook",
        "/Open Notebook",
        "/Notes/notebook tips.md",
    ]
    body = server.requests[0].content.decode()
    assert "<d:literal>%notebook%</d:literal>" in body
    assert "<d:href>/files/alice</d:href>" in body


async def test_walking_a_folder_collects_files_in_every_sub_folder():
    server = FakeNextcloud()
    server.reply(
        "PROPFIND", f"{DAV}/Open Notebook", multistatus("propfind_open_notebook.xml")
    )
    server.reply(
        "PROPFIND", f"{DAV}/Open Notebook/Week 1", multistatus("propfind_week1.xml")
    )

    files, truncated = await server.client().walk_files("/Open Notebook", max_files=10)

    assert not truncated
    assert [f.path for f in files] == [
        "/Open Notebook/Anatomy notes.pdf",
        "/Open Notebook/Week 1/clip.mkv",
        "/Open Notebook/Week 1/fluids.md",
    ]


async def test_walking_stops_at_the_file_cap():
    server = FakeNextcloud()
    server.reply(
        "PROPFIND", f"{DAV}/Open Notebook", multistatus("propfind_open_notebook.xml")
    )
    server.reply(
        "PROPFIND", f"{DAV}/Open Notebook/Week 1", multistatus("propfind_week1.xml")
    )

    files, truncated = await server.client().walk_files("/Open Notebook", max_files=2)

    assert truncated
    assert len(files) == 2


async def test_download_streams_to_a_temporary_file():
    server = FakeNextcloud()
    server.reply(
        "GET",
        f"{DAV}/Open Notebook/Week 1/fluids.md",
        httpx.Response(200, content=b"# Fluids\n"),
    )

    path = await server.client().download(
        "/Open Notebook/Week 1/fluids.md", max_bytes=100
    )
    try:
        assert path.endswith(".md")
        with open(path, "rb") as handle:
            assert handle.read() == b"# Fluids\n"
    finally:
        os.unlink(path)


async def test_a_download_past_the_limit_is_abandoned_and_leaves_nothing(
    tmp_path, monkeypatch
):
    monkeypatch.setenv("TMPDIR", str(tmp_path))
    import tempfile

    monkeypatch.setattr(tempfile, "tempdir", str(tmp_path))
    server = FakeNextcloud()
    server.reply("GET", f"{DAV}/big.pdf", httpx.Response(200, content=b"x" * 1000))

    with pytest.raises(NextcloudFileTooLargeError):
        await server.client().download("/big.pdf", max_bytes=10)
    assert list(tmp_path.iterdir()) == []


async def test_only_read_methods_are_ever_sent():
    server = FakeNextcloud()
    server.reply(
        "PROPFIND", f"{DAV}/Open Notebook", multistatus("propfind_open_notebook.xml")
    )
    server.reply(
        "PROPFIND", f"{DAV}/Open Notebook/Week 1", multistatus("propfind_week1.xml")
    )
    server.reply("SEARCH", "/remote.php/dav/", multistatus("search_reply.xml"))
    server.reply(
        "GET",
        f"{DAV}/Open Notebook/Week 1/fluids.md",
        httpx.Response(200, content=b"x"),
    )
    client = server.client()

    await client.walk_files("/Open Notebook", max_files=10)
    await client.search("note")
    os.unlink(await client.download("/Open Notebook/Week 1/fluids.md", max_bytes=10))

    assert {r.method for r in server.requests} <= {"PROPFIND", "SEARCH", "GET"}
    with pytest.raises(ValueError):
        await client._request("DELETE", "https://cloud.example.com/x", "/x")
