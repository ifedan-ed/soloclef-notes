"""A notebook's "Configure chat" choices and the instruction they become.

The choices are stored on the notebook and turned into a few sentences inside
the per-turn notebook context, so changing them takes effect on the next
message of every chat in the notebook without touching the cached agent.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal

ChatGoal = Literal["default", "learning_guide", "custom"]
ResponseLength = Literal["default", "longer", "shorter"]

CHAT_GOALS: tuple[ChatGoal, ...] = ("default", "learning_guide", "custom")
RESPONSE_LENGTHS: tuple[ResponseLength, ...] = ("default", "longer", "shorter")
MAX_INSTRUCTIONS_CHARS = 2_000

_GOAL_TEXT: dict[str, str] = {
    "learning_guide": (
        "Act as a learning guide: help the user understand the material "
        "rather than just handing over answers. Break ideas into steps, check "
        "understanding with a short question now and then, and suggest what "
        "to explore next."
    ),
}

_LENGTH_TEXT: dict[str, str] = {
    "longer": ("Prefer longer, more thorough answers with more detail and examples."),
    "shorter": "Prefer short, direct answers; expand only when asked.",
}


@dataclass(frozen=True)
class ChatSettings:
    goal: str = "default"
    instructions: str | None = None
    length: str = "default"


def render_chat_settings(settings: ChatSettings) -> list[str]:
    """Instruction lines for the notebook context; empty for the defaults."""
    lines: list[str] = []
    if settings.goal == "custom":
        custom = " ".join((settings.instructions or "").split())
        if custom:
            lines.append(
                "The notebook owner set these instructions for how to respond; "
                "follow them unless they conflict with safety rules: "
                f"{custom[:MAX_INSTRUCTIONS_CHARS]}"
            )
    elif settings.goal in _GOAL_TEXT:
        lines.append(_GOAL_TEXT[settings.goal])
    if settings.length in _LENGTH_TEXT:
        lines.append(_LENGTH_TEXT[settings.length])
    return lines
