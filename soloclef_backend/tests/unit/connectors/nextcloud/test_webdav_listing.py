"""Reading recorded Nextcloud PROPFIND/SEARCH replies."""

from datetime import UTC, datetime

import pytest

from app.connectors.nextcloud.webdav_listing import (
    MalformedListingError,
    folder_children,
    parse_multistatus,
)

from .fake_server import fixture

pytestmark = pytest.mark.unit


def test_a_depth_one_listing_reads_every_entry_with_its_facts():
    entries = parse_multistatus(fixture("propfind_root.xml"), "alice")
    by_path = {entry.path: entry for entry in entries}

    assert "/" in by_path and by_path["/"].is_folder
    folder = by_path["/Open Notebook"]
    assert folder.is_folder and folder.name == "Open Notebook"
    assert folder.content_type is None

    odt = by_path["/Zebra notes.odt"]
    assert not odt.is_folder
    assert odt.size == 44015
    assert odt.etag == "6a9a121517dc5"
    assert odt.content_type == "application/vnd.oasis.opendocument.text"
    assert odt.modified == datetime(2024, 12, 14, 22, 20, 22, tzinfo=UTC)


def test_names_with_spaces_unicode_and_reserved_characters_are_decoded():
    paths = {e.path for e in parse_multistatus(fixture("propfind_root.xml"), "alice")}
    assert "/Résumé \u2013 2024 (final).pdf" in paths
    assert "/C# & notes+1.md" in paths


def test_children_leave_out_the_folder_itself_and_list_folders_first():
    entries = parse_multistatus(fixture("propfind_root.xml"), "alice")
    names = [entry.name for entry in folder_children(entries, "/")]
    assert names[0] == "Open Notebook"
    assert names[1:] == [
        "C# & notes+1.md",
        "Movie.mkv",
        "Résumé \u2013 2024 (final).pdf",
        "Zebra notes.odt",
    ]


def test_search_results_outside_the_account_are_dropped():
    entries = parse_multistatus(fixture("search_reply.xml"), "alice")
    assert [entry.path for entry in entries] == [
        "/Backups/Old Notebook",
        "/Open Notebook",
        "/Notes/notebook tips.md",
    ]


def test_a_reply_that_is_not_xml_is_refused():
    with pytest.raises(MalformedListingError):
        parse_multistatus(b"<html>Login</html", "alice")


def test_a_reply_that_is_not_a_multistatus_is_refused():
    with pytest.raises(MalformedListingError):
        parse_multistatus(fixture("error_unauthorized.xml"), "alice")
