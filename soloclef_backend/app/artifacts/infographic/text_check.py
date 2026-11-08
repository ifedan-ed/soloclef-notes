"""Does the rendered infographic say what the brief planned?

Image models garble text: a heading misspelled, a number changed, a label
dropped. The structural check only asked whether the PNG was a real image,
so a garbled infographic passed. The vision role's model (a cheap one) now
reads the image once per attempt and marks each planned item (the brief's
headings, short labels and numbers) present, missing or garbled. Below the
threshold, the first attempt is sent back for its one repair with the
problems as findings; after that the infographic is saved with the result as
a visible warning in its metadata rather than silently passing.
"""

from __future__ import annotations

import base64
import io
import logging
import re
from dataclasses import dataclass, field
from typing import Any, Literal

from langchain_core.messages import HumanMessage
from pydantic import BaseModel, ConfigDict, Field

from app.utils.structured_output import StructuredOutputError, invoke_json

logger = logging.getLogger(__name__)

PASS_THRESHOLD = 0.8
MAX_ITEMS = 30
_MAX_LABEL_CHARS = 80
_READ_TIMEOUT_SECONDS = 60
_MAX_EDGE = 2048

_HEADING = re.compile(r"^\s{0,3}#{1,6}\s+(.+?)\s*#*\s*$")
_LIST_ITEM = re.compile(r"^\s*(?:[-*+]|\d{1,3}[.)])\s+(.+)$")
_NUMBER = re.compile(r"(?<![\w.])[-+]?\d[\d,]*(?:\.\d+)?\s?%?")
_MARKUP = re.compile(r"[*_`]+|\[([^\]]*)\]\([^)]*\)")


class _ItemRead(BaseModel):
    model_config = ConfigDict(extra="ignore")

    id: int
    status: Literal["present", "missing", "garbled"]
    seen: str | None = Field(default=None, max_length=200)


class _Reading(BaseModel):
    model_config = ConfigDict(extra="ignore")

    items: list[_ItemRead] = []


@dataclass(frozen=True)
class TextCheck:
    """``score`` is the share of planned items the image shows correctly."""

    score: float | None
    checked: int = 0
    missing: list[str] = field(default_factory=list)
    garbled: list[str] = field(default_factory=list)
    unavailable_reason: str | None = None

    @property
    def passed(self) -> bool:
        return self.score is None or self.score >= PASS_THRESHOLD

    def findings(self) -> list[str]:
        found = [f'Missing from the image: "{item}"' for item in self.missing]
        found += [f"Garbled in the image: {item}" for item in self.garbled]
        return found

    def as_metadata(self) -> dict[str, Any]:
        return {
            "score": None if self.score is None else round(self.score, 2),
            "checked": self.checked,
            "missing": self.missing,
            "garbled": self.garbled,
            "passed": self.passed,
            "unavailable_reason": self.unavailable_reason,
        }


def _clean(text: str) -> str:
    return " ".join(_MARKUP.sub(lambda m: m.group(1) or "", text).split())


def planned_items(markdown: str, limit: int = MAX_ITEMS) -> list[str]:
    """The headings, short labels and numbers a reader should see, in order.

    ``limit`` follows the chosen level of detail: a concise infographic is
    checked on fewer items because it plans fewer.
    """
    items: list[str] = []
    for line in markdown.splitlines():
        heading = _HEADING.match(line)
        listed = _LIST_ITEM.match(line)
        if heading:
            items.append(_clean(heading.group(1)))
        elif listed:
            label = _clean(listed.group(1)).split(":", 1)[0]
            if len(label) <= _MAX_LABEL_CHARS:
                items.append(label)
        for number in _NUMBER.findall(line):
            number = number.strip()
            if any(ch.isdigit() for ch in number):
                items.append(number)
    unique = list(dict.fromkeys(item for item in items if item))
    return unique[: max(1, min(limit, MAX_ITEMS))]


def _jpeg_data_url(png: bytes) -> str:
    from PIL import Image

    with Image.open(io.BytesIO(png)) as image:
        frame = image.convert("RGB")
    frame.thumbnail((_MAX_EDGE, _MAX_EDGE))
    out = io.BytesIO()
    frame.save(out, format="JPEG", quality=88)
    return "data:image/jpeg;base64," + base64.b64encode(out.getvalue()).decode()


def _prompt(items: list[str]) -> str:
    listed = "\n".join(f"{index}. {item}" for index, item in enumerate(items, 1))
    return (
        "This infographic was generated from a plan. Read the image and, for each "
        "planned item below, say whether it appears legibly and correctly "
        '("present"), does not appear ("missing"), or appears misspelled, '
        'distorted or with a different number ("garbled"; give what you see). '
        "Wording may differ slightly; numbers must match exactly.\n\n"
        f"Planned items:\n{listed}\n\n"
        'Reply with only JSON: {"items": [{"id": 1, "status": "present", '
        '"seen": null}, ...]} with one entry per planned item.'
    )


async def check_rendered_text(
    vision_llm: Any, png: bytes, markdown: str, *, max_items: int = MAX_ITEMS
) -> TextCheck:
    """Read the image once and compare it with the planned items."""
    items = planned_items(markdown, max_items)
    if not items:
        return TextCheck(score=None, unavailable_reason="no planned text to compare")
    if vision_llm is None:
        return TextCheck(score=None, unavailable_reason="no vision model configured")
    try:
        reading = await invoke_json(
            vision_llm,
            [
                HumanMessage(
                    content=[
                        {"type": "text", "text": _prompt(items)},
                        {
                            "type": "image_url",
                            "image_url": {"url": _jpeg_data_url(png)},
                        },
                    ]
                )
            ],
            _Reading,
            attempt_timeout=_READ_TIMEOUT_SECONDS,
        )
    except (StructuredOutputError, TimeoutError, ValueError, OSError) as exc:
        logger.info("Infographic text check unavailable: %s", exc)
        return TextCheck(score=None, unavailable_reason=type(exc).__name__)
    except Exception as exc:  # the provider failing never blocks the infographic
        logger.warning("Infographic text check failed", exc_info=True)
        return TextCheck(score=None, unavailable_reason=type(exc).__name__)

    by_id = {read.id: read for read in reading.items}
    missing: list[str] = []
    garbled: list[str] = []
    for index, item in enumerate(items, 1):
        read = by_id.get(index)
        if read is None or read.status == "missing":
            missing.append(item)
        elif read.status == "garbled":
            seen = f' (reads "{read.seen}")' if read.seen else ""
            garbled.append(f'"{item}"{seen}')
    correct = len(items) - len(missing) - len(garbled)
    return TextCheck(
        score=correct / len(items),
        checked=len(items),
        missing=missing,
        garbled=garbled,
    )
