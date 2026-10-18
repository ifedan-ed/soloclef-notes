"""Which saved artifact formats can stand for each Studio output.

A run may save more than one artifact (an illustration beside a report, say),
or the model may pick the wrong skill. The job is only ready when an artifact
of the requested kind exists; anything else would fill the wrong Studio tile.
Format strings are the verification adapters' names, which save_artifact
stores on the artifact.
"""

from __future__ import annotations

from collections.abc import Iterable
from typing import Any

ALLOWED_FORMATS: dict[str, frozenset[str]] = {
    "report": frozenset({"pdf", "docx", "html"}),
    "slides": frozenset({"pptx"}),
    "data_table": frozenset({"xlsx"}),
    "quiz": frozenset({"quiz"}),
    "flashcards": frozenset({"flashcards"}),
    "mindmap": frozenset({"mindmap"}),
    "infographic": frozenset({"infographic"}),
}


def pick_requested_artifact(output: str, artifacts: Iterable[Any]) -> Any | None:
    """The first artifact, in the order the run saved them, that ``output`` accepts."""
    allowed = ALLOWED_FORMATS[output]
    return next((a for a in artifacts if a.format in allowed), None)
