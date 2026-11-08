"""Remote paths: normalizing what callers send, and mapping to and from URLs."""

import pytest

from app.connectors.nextcloud.remote_paths import (
    InvalidRemotePathError,
    dav_url,
    normalize_remote_path,
    parent_of,
    path_from_href,
)

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        (None, "/"),
        ("", "/"),
        ("/", "/"),
        ("Open Notebook", "/Open Notebook"),
        ("/Open Notebook/", "/Open Notebook"),
        ("//a//./b/", "/a/b"),
        ("/Résumé \u2013 2024.pdf", "/Résumé \u2013 2024.pdf"),
    ],
)
def test_paths_are_normalized_to_absolute_posix(raw, expected):
    assert normalize_remote_path(raw) == expected


@pytest.mark.parametrize("raw", ["/a/../b", "..", "/a\\b", "/a\x00b"])
def test_paths_that_could_escape_are_refused(raw):
    with pytest.raises(InvalidRemotePathError):
        normalize_remote_path(raw)


def test_urls_encode_each_segment_and_folders_end_in_a_slash():
    base = "https://cloud.example.com/"
    assert (
        dav_url(base, "alice", "/Open Notebook/C# & notes+1.md")
        == "https://cloud.example.com/remote.php/dav/files/alice/Open%20Notebook/C%23%20%26%20notes%2B1.md"
    )
    assert (
        dav_url(base, "alice", "/Open Notebook", folder=True)
        == "https://cloud.example.com/remote.php/dav/files/alice/Open%20Notebook/"
    )
    assert dav_url(base, "a@b.org", "/") == (
        "https://cloud.example.com/remote.php/dav/files/a%40b.org/"
    )


@pytest.mark.parametrize(
    "href",
    [
        "/remote.php/dav/files/alice/R%C3%A9sum%C3%A9%20%E2%80%93%202024.pdf",
        "https://cloud.example.com/remote.php/dav/files/alice/R%C3%A9sum%C3%A9%20%E2%80%93%202024.pdf",
        "/nextcloud/remote.php/dav/files/alice/R%C3%A9sum%C3%A9%20%E2%80%93%202024.pdf",
    ],
)
def test_hrefs_map_back_to_paths(href):
    assert path_from_href(href, "alice") == "/Résumé \u2013 2024.pdf"


def test_hrefs_of_another_account_or_climbing_out_map_to_nothing():
    assert path_from_href("/remote.php/dav/files/bob/x.md", "alice") is None
    assert path_from_href("/remote.php/dav/files/alice/a/%2E%2E/x", "alice") is None


def test_an_email_login_matches_encoded_or_plain_hrefs():
    assert path_from_href("/remote.php/dav/files/a%40b.org/x.md", "a@b.org") == "/x.md"
    assert path_from_href("/remote.php/dav/files/a@b.org/x.md", "a@b.org") == "/x.md"


def test_parent_of_the_root_is_the_root():
    assert parent_of("/") == "/"
    assert parent_of("/a") == "/"
    assert parent_of("/a/b.pdf") == "/a"
