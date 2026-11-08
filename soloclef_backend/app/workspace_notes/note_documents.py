"""What a note document's row says about it: origin, automation, title, preview.

Pure functions over ``Document.document_metadata`` and text, shared by the
listing, the move into a notebook and the automation stamp, so each reads
the same keys the same way.
"""

from __future__ import annotations

import re
from typing import Any, Literal

from .readable_text import links_as_words, math_as_text

NoteOrigin = Literal["hand", "chat", "automation"]

# Keys an automation run writes into the metadata of the note documents its
# agent creates (``automation_stamp``), and the attach script for older ones.
AUTOMATION_ID_KEY = "automation_id"
AUTOMATION_NAME_KEY = "automation_name"
AUTOMATION_RUN_KEY = "automation_run_id"

# Only what the list shows under a title; the whole note is fetched when it is
# opened. A display length, not a cap on what a note may hold.
PREVIEW_CHARS = 240

_MARKDOWN_MARKS = re.compile(r"[#*_`>|~\[\]]")
_WHITESPACE = re.compile(r"\s+")


def automation_id_of(metadata: dict[str, Any] | None) -> int | None:
    value = (metadata or {}).get(AUTOMATION_ID_KEY)
    if isinstance(value, bool) or not isinstance(value, int) or value <= 0:
        return None
    return value


def automation_name_of(metadata: dict[str, Any] | None) -> str | None:
    value = (metadata or {}).get(AUTOMATION_NAME_KEY)
    return value if isinstance(value, str) and value.strip() else None


def origin_of(metadata: dict[str, Any] | None) -> NoteOrigin:
    """``automation`` when a run wrote it; ``chat`` when the assistant wrote it
    as a file (it carries a ``virtual_path``); otherwise ``hand``."""
    meta = metadata or {}
    if automation_id_of(meta) is not None or automation_name_of(meta) is not None:
        return "automation"
    if isinstance(meta.get("virtual_path"), str):
        return "chat"
    return "hand"


def display_title(title: str) -> str:
    """The file name the assistant gave it, without ``.md``."""
    stripped = (title or "").strip()
    if stripped.lower().endswith(".md") and len(stripped) > 3:
        stripped = stripped[:-3].rstrip()
    return stripped or "Untitled note"


def preview_of(markdown: str | None) -> str:
    """The note's opening words: its links and images as their words, its
    maths as text (``readable_text``), Markdown marks and line breaks
    removed."""
    plain = math_as_text(
        links_as_words(markdown or ""), lambda text: _MARKDOWN_MARKS.sub("", text)
    )
    text = _WHITESPACE.sub(" ", plain).strip()
    if len(text) <= PREVIEW_CHARS:
        return text
    return text[: PREVIEW_CHARS - 1].rstrip() + "…"
