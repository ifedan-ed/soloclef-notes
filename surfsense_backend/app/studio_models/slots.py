"""The output types a Studio model can be set for, and which one a run is.

Reports split by what they produce, because the measured comparison ranked
models differently for an interactive report than for a PDF or Word file.
The Learning Overview template is an interactive report, so it shares that
slot.
"""

from __future__ import annotations

from typing import Any, Literal

StudioSlot = Literal[
    "audio",
    "video",
    "report_interactive",
    "report_pdf",
    "report_docx",
    "slides",
    "mindmap",
    "flashcards",
    "quiz",
    "infographic",
    "data_table",
]

STUDIO_SLOTS: tuple[str, ...] = (
    "audio",
    "video",
    "report_interactive",
    "report_pdf",
    "report_docx",
    "slides",
    "mindmap",
    "flashcards",
    "quiz",
    "infographic",
    "data_table",
)


def slot_for(kind: str, options: dict[str, Any] | None) -> str | None:
    """The slot of a notebook output, from its kind and the dialog's options."""
    options = options or {}
    if kind == "report":
        if options.get("format") == "interactive":
            return "report_interactive"
        return "report_docx" if options.get("file_type") == "docx" else "report_pdf"
    return kind if kind in STUDIO_SLOTS else None
