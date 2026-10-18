"""The kinds of push notification, each a switch in the app's More → Notifications.

Every kind is on by default (decision 9, "every event in §6 on"). The app
lists what ``GET /api/v1/push/preferences`` returns, so adding a kind here is
enough for it to show up on phones; the app never carries its own list.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Literal

PushKind = Literal[
    "answer",
    "approval",
    "output",
    "automation",
    "source",
    "mention",
    "announcement",
    "invite",
    "credits",
]


@dataclass(frozen=True, slots=True)
class KindInfo:
    kind: PushKind
    label: str
    description: str


KINDS: tuple[KindInfo, ...] = (
    KindInfo(
        "answer",
        "Chat answers",
        "An answer that finished, or failed, while you were away from the chat.",
    ),
    KindInfo("approval", "Approvals", "A chat is waiting for you to approve a step."),
    KindInfo(
        "output",
        "Studio outputs",
        "An audio overview, report, slides or other output is ready or failed.",
    ),
    KindInfo("automation", "Automations", "A run finished or failed."),
    KindInfo("source", "Sources", "A source finished indexing, or could not be added."),
    KindInfo("mention", "Mentions and replies", "Someone mentioned you or replied."),
    KindInfo("announcement", "Announcements", "From the site's administrators."),
    KindInfo("invite", "Invites", "Someone accepted an invite you sent."),
    KindInfo("credits", "Credits", "Your credits ran out."),
)

KIND_NAMES: tuple[PushKind, ...] = tuple(k.kind for k in KINDS)


def is_kind(value: object) -> bool:
    return isinstance(value, str) and value in KIND_NAMES


def default_choices() -> dict[str, bool]:
    """Every kind on: what a person has before they change anything."""
    return dict.fromkeys(KIND_NAMES, True)


def merged_choices(saved: dict | None) -> dict[str, bool]:
    """The saved choices over the defaults; unknown keys and non-booleans dropped."""
    choices = default_choices()
    for key, value in (saved or {}).items():
        if key in choices and isinstance(value, bool):
            choices[key] = value
    return choices


def wants(kind: str, saved: dict | None) -> bool:
    """Whether this person has the kind on (the default is on)."""
    return merged_choices(saved).get(kind, False)
