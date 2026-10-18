"""Safe XML parsing and the flattening of mixed-content elements to plain text."""

from __future__ import annotations

import re
from xml.etree.ElementTree import Element

from defusedxml.ElementTree import fromstring

_SPACE = re.compile(r"\s+")


def parse_xml(text: str) -> Element:
    """Parse untrusted XML (no entity expansion, no external DTDs)."""
    return fromstring(text.encode("utf-8") if isinstance(text, str) else text)


def flat_text(element: Element | None) -> str:
    """Every text node under ``element``, whitespace collapsed."""
    if element is None:
        return ""
    return _SPACE.sub(" ", "".join(element.itertext())).strip()


def local_name(tag: str) -> str:
    """``{namespace}name`` -> ``name``."""
    return tag.rsplit("}", 1)[-1]
