"""Read a WebDAV multistatus reply (PROPFIND or SEARCH) into remote entries.

Only the properties the picker and the import need are kept. A response whose
properties came back 404 simply lacks them; a response for a path outside the
user's files is dropped.
"""

from __future__ import annotations

import xml.etree.ElementTree as ET
from dataclasses import dataclass
from datetime import datetime
from email.utils import parsedate_to_datetime

from .remote_paths import path_from_href, path_parts

_DAV = "{DAV:}"
_OC = "{http://owncloud.org/ns}"

#: The properties asked for on every listing, as the PROPFIND/SEARCH body's <d:prop>.
REQUESTED_PROPS = (
    "<d:displayname/><d:resourcetype/><d:getcontentlength/>"
    "<d:getlastmodified/><d:getcontenttype/><d:getetag/><oc:size/>"
)


class MalformedListingError(ValueError):
    """The server's reply was not a WebDAV multistatus document."""


@dataclass(frozen=True)
class RemoteEntry:
    path: str
    name: str
    is_folder: bool
    size: int | None
    modified: datetime | None
    content_type: str | None
    etag: str | None


def _text(prop: ET.Element, tag: str) -> str | None:
    element = prop.find(tag)
    if element is None or element.text is None:
        return None
    value = element.text.strip()
    return value or None


def _int(value: str | None) -> int | None:
    if value is None:
        return None
    try:
        return int(value)
    except ValueError:
        return None


def _date(value: str | None) -> datetime | None:
    if value is None:
        return None
    try:
        return parsedate_to_datetime(value)
    except (TypeError, ValueError):
        return None


def _ok_props(response: ET.Element) -> ET.Element | None:
    """Merge every 200 propstat's <prop> children into one element."""
    merged = ET.Element("prop")
    for propstat in response.findall(f"{_DAV}propstat"):
        status = propstat.findtext(f"{_DAV}status") or ""
        if " 200 " not in f"{status} ":
            continue
        prop = propstat.find(f"{_DAV}prop")
        if prop is not None:
            merged.extend(list(prop))
    return merged if len(merged) else None


def _entry(response: ET.Element, username: str) -> RemoteEntry | None:
    href = response.findtext(f"{_DAV}href")
    if not href:
        return None
    path = path_from_href(href.strip(), username)
    if path is None:
        return None
    props = _ok_props(response)
    if props is None:
        return None
    resourcetype = props.find(f"{_DAV}resourcetype")
    is_folder = resourcetype is not None and (
        resourcetype.find(f"{_DAV}collection") is not None
    )
    parts = path_parts(path)
    # The href is the source of truth for the name: displayname can be absent
    # or, on shares, differ from what the path must use.
    name = parts[-1] if parts else (_text(props, f"{_DAV}displayname") or "")
    size = _int(_text(props, f"{_DAV}getcontentlength"))
    if size is None:
        size = _int(_text(props, f"{_OC}size"))
    etag = _text(props, f"{_DAV}getetag")
    return RemoteEntry(
        path=path,
        name=name,
        is_folder=is_folder,
        size=size,
        modified=_date(_text(props, f"{_DAV}getlastmodified")),
        content_type=None if is_folder else _text(props, f"{_DAV}getcontenttype"),
        etag=etag.strip('"') if etag else None,
    )


def parse_multistatus(body: bytes | str, username: str) -> list[RemoteEntry]:
    """Every entry the reply describes, in the server's order."""
    try:
        root = ET.fromstring(body)
    except ET.ParseError as exc:
        raise MalformedListingError(f"unreadable WebDAV reply: {exc}") from exc
    if root.tag != f"{_DAV}multistatus":
        raise MalformedListingError(f"expected a multistatus reply, got {root.tag}")
    entries = []
    for response in root.findall(f"{_DAV}response"):
        entry = _entry(response, username)
        if entry is not None:
            entries.append(entry)
    return entries


def folder_children(entries: list[RemoteEntry], folder: str) -> list[RemoteEntry]:
    """A Depth-1 listing without the folder itself: folders first, then by name."""
    children = [entry for entry in entries if entry.path != folder]
    return sorted(children, key=lambda e: (not e.is_folder, e.name.casefold()))
