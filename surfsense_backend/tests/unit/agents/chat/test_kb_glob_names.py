"""Globs find knowledge-base documents by the name the user knows.

Documents are served as ``<title>.xml``; an uploaded ``slides.pptx`` lives at
``/documents/slides.pptx.xml``. The agent globbed ``**/slides.pptx`` and
``**/*.pptx`` and got nothing.
"""

from __future__ import annotations

import fnmatch
import re

import pytest

from app.agents.chat.multi_agent_chat.shared.middleware.filesystem.backends.kb_postgres import (
    _glob_hit,
)

pytestmark = pytest.mark.unit

PATH = "/documents/slides.pptx.xml"


def _re(pattern: str) -> re.Pattern[str]:
    return re.compile(fnmatch.translate(pattern))


@pytest.mark.parametrize(
    "pattern",
    ["**/slides.pptx", "**/*.pptx", "**/slides.pptx.xml", "**/*.xml", "**"],
)
def test_a_document_matches_with_or_without_its_served_extension(pattern):
    assert _glob_hit(_re(pattern), PATH.lstrip("/"), PATH)


@pytest.mark.parametrize("pattern", ["**/*.pdf", "**/other.pptx", "**/slides"])
def test_other_names_still_do_not_match(pattern):
    assert not _glob_hit(_re(pattern), PATH.lstrip("/"), PATH)


def test_a_basename_glob_matches_the_known_name():
    assert _glob_hit(_re("*.pptx"), "slides.pptx.xml")
    assert not _glob_hit(_re("*.pptx"), "notes.md")
