"""The number of cards or questions a request asked for, enforced at save.

Studio's "Fewer" asked for about ten flashcards and models wrote fifteen to
twenty, because the prompt said "about", the skill said fifteen at least,
and nothing checked. A headless run now carries the target and the fewest it
will accept. At save, a deck or quiz over the target is trimmed to it (the
first items are kept, in order); one under the minimum is refused with the
numbers, so the model adds items instead of the job saving the wrong size.
"""

from __future__ import annotations

import json
from typing import Annotated

from pydantic import BaseModel, ConfigDict, Field, model_validator

_ITEMS_BY_FORMAT = {
    "flashcards": ("cards", "cards"),
    "quiz": ("questions", "questions"),
}


class ItemCount(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True, frozen=True)

    target: Annotated[int, Field(ge=1, le=100)]
    minimum: Annotated[int, Field(ge=1, le=100)]

    @model_validator(mode="after")
    def _minimum_within_target(self) -> ItemCount:
        if self.minimum > self.target:
            raise ValueError("minimum must not exceed target")
        return self


class ItemCountError(ValueError):
    """Too few items for what the request asked."""


def applies_to(format_name: str) -> bool:
    return format_name in _ITEMS_BY_FORMAT


def enforce_item_count(format_name: str, data: bytes, count: ItemCount) -> bytes:
    """``data`` trimmed to ``count.target`` items, or ItemCountError if too few.

    ``data`` is JSON that already passed verification; bytes within the range
    come back unchanged so their verified hash still holds.
    """
    key, noun = _ITEMS_BY_FORMAT[format_name]
    value = json.loads(data.decode("utf-8"))
    items = value[key]
    if len(items) < count.minimum:
        raise ItemCountError(
            f"The {format_name} has {len(items)} {noun}; this request asked for "
            f"{count.target} (at least {count.minimum}). Add "
            f"{count.minimum - len(items)} or more distinct {noun}, verify, and "
            "save again."
        )
    if len(items) <= count.target:
        return data
    value[key] = items[: count.target]
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
