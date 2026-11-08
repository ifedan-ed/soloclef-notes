"""Paths inside a Nextcloud user's files, and the WebDAV URLs that reach them.

A remote path is always absolute and POSIX-shaped ("/", "/Open Notebook",
"/Open Notebook/a b.pdf"), never ends in a slash except for the root, and can
not climb out of the user's files. Anything a caller sends goes through
:func:`normalize_remote_path` before it touches a URL.
"""

from __future__ import annotations

from urllib.parse import quote, unquote, urlsplit

ROOT = "/"


class InvalidRemotePathError(ValueError):
    """A path that is not a plain location inside the user's files."""


def normalize_remote_path(raw: str | None) -> str:
    """``raw`` as a canonical absolute path; ``..``, NULs and backslashes are refused."""
    text = (raw or "").strip()
    if "\x00" in text or "\\" in text:
        raise InvalidRemotePathError("path contains a forbidden character")
    parts = [part for part in text.split("/") if part not in ("", ".")]
    if any(part == ".." for part in parts):
        raise InvalidRemotePathError("path may not contain '..'")
    return "/" + "/".join(parts) if parts else ROOT


def path_parts(path: str) -> list[str]:
    """The folder and file names of a normalized path, root first."""
    return [part for part in path.split("/") if part]


def parent_of(path: str) -> str:
    parts = path_parts(path)
    return "/" + "/".join(parts[:-1]) if len(parts) > 1 else ROOT


def join_path(folder: str, name: str) -> str:
    return normalize_remote_path(f"{folder.rstrip('/')}/{name}")


def files_root_href(username: str) -> str:
    """The URL path of the user's files root, as the server writes it in ``<d:href>``."""
    return f"/remote.php/dav/files/{quote(username, safe='')}/"


def dav_url(base_url: str, username: str, path: str, *, folder: bool = False) -> str:
    """The absolute WebDAV URL of ``path``; folders end in a slash."""
    encoded = "/".join(quote(part, safe="") for part in path_parts(path))
    url = base_url.rstrip("/") + files_root_href(username) + encoded
    if folder and encoded:
        url += "/"
    return url


def path_from_href(href: str, username: str) -> str | None:
    """The remote path a ``<d:href>`` names, or None when it is outside the user's files.

    Servers may send a bare path or a full URL, and may or may not keep a
    sub-path prefix (Nextcloud behind ``/nextcloud``); only what follows
    ``/remote.php/dav/files/<user>/`` matters.
    """
    href_path = urlsplit(href).path
    marker = files_root_href(username)
    index = href_path.find(marker)
    if index < 0:
        # Some servers leave the user segment unencoded.
        marker = f"/remote.php/dav/files/{username}/"
        index = href_path.find(marker)
        if index < 0:
            return None
    remainder = href_path[index + len(marker) :]
    try:
        return normalize_remote_path(unquote(remainder))
    except InvalidRemotePathError:
        return None
