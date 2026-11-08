"""What a push says and where it opens: pure, so it is unit-tested in full.

The lock-screen text is generic (decision 9). ``include_titles`` adds the
notebook's or announcement's title when the owner has turned that on
(``PUSH_INCLUDE_TITLES``); nothing else ever goes into a push. The path is an
app route the phone opens on a tap; the app checks it against its own
allow-list before navigating (``soloclef_mobile/src/lib/push/deepLinks.ts``),
``ws`` names the workspace that path belongs to, when it belongs to one, and
``server`` the server it comes from (``server_origin``), so a phone set to
another server ignores it.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any

from app.push.kinds import PushKind

# The web's Studio kinds, in the words the app and the web use for them.
OUTPUT_LABELS: dict[str, str] = {
    "audio": "audio overview",
    "podcast": "audio overview",
    "video": "video overview",
    "video_presentation": "video overview",
    "report": "report",
    "slides": "slide deck",
    "mindmap": "mind map",
    "flashcards": "flashcards",
    "quiz": "quiz",
    "data_table": "data table",
    "infographic": "infographic",
}

# A title is cut to this many characters before it goes into a push.
TITLE_MAX = 80

# The Android notification channel every push lands in (the app makes it).
ANDROID_CHANNEL = "soloclef"


@dataclass(frozen=True, slots=True)
class PushMessage:
    kind: PushKind
    title: str
    body: str
    path: str
    data: dict[str, Any] = field(default_factory=dict)


def _with_title(sentence: str, title: str | None, include_titles: bool) -> str:
    if include_titles and title and title.strip():
        return f"{sentence}: {_shorten(title)}"
    return sentence


def _shorten(title: str) -> str:
    title = " ".join(title.split())
    return title if len(title) <= TITLE_MAX else title[: TITLE_MAX - 1].rstrip() + "…"


# The app routes whose workspace the sender looks up when the event did not
# name it (``tasks._resolve_workspace``): a notebook's, and an automation's
# (its runs too).
_NOTEBOOK_PATH = re.compile(r"^/notebook/(\d+)(?:[/?]|$)")
_AUTOMATION_PATH = re.compile(r"^/automation/(\d+)(?:[/?]|$)")


def path_owner(path: str | None) -> tuple[str, int] | None:
    """What a path opens that belongs to a workspace: ("notebook", 11), ("automation", 3), or None."""
    if not path:
        return None
    for name, pattern in (
        ("notebook", _NOTEBOOK_PATH),
        ("automation", _AUTOMATION_PATH),
    ):
        match = pattern.match(path)
        if match:
            return name, int(match.group(1))
    return None


def notebook_path(notebook_id: int | None, pane: str | None = None) -> str:
    """Where the app opens a notebook, or the Inbox when there is none."""
    if not notebook_id:
        return "/inbox"
    return f"/notebook/{notebook_id}" + (f"?pane={pane}" if pane else "")


def build_message(
    kind: PushKind,
    *,
    failed: bool = False,
    path: str | None = None,
    title: str | None = None,
    output_kind: str | None = None,
    variant: str | None = None,
    include_titles: bool = False,
    workspace_id: int | None = None,
    server: str = "",
) -> PushMessage:
    """The push for one event: a generic title line, a short body, the path.

    ``variant`` picks a second wording within a kind: a ``source`` push for a
    connector sync ("connector"), a ``mention`` push for a reply ("reply").
    ``workspace_id`` goes into the data as ``ws`` when the path belongs to a
    workspace, so the app switches to it before opening the path.
    ``server`` (this server's origin, ``server_origin.this_server``) goes in
    as ``server`` for every kind; empty (no address phones could match) leaves
    it out, and the app then treats the push as an older server's.
    """
    if kind == "answer":
        head = "Answer could not be finished" if failed else "Your answer is ready"
        body = (
            "Open the chat to see what went wrong."
            if failed
            else "Open the chat to read it."
        )
    elif kind == "approval":
        head, body = "Approval needed", "A chat is waiting for you to approve a step."
    elif kind == "output":
        label = OUTPUT_LABELS.get(output_kind or "", "output")
        head = f"Your {label} {'could not be made' if failed else 'is ready'}"
        body = "Open Studio to try again." if failed else "Open Studio to see it."
    elif kind == "automation":
        head = "An automation failed" if failed else "An automation finished"
        body = "Open the run to see its results."
    elif kind == "source" and variant == "connector":
        head = "A connector sync failed" if failed else "A connector sync finished"
        body = (
            "Open the Inbox for details."
            if failed
            else "Its sources are now searchable."
        )
    elif kind == "source":
        head = "A source could not be added" if failed else "A source is ready"
        body = "Open the Inbox for details." if failed else "It is now searchable."
    elif kind == "mention" and variant == "reply":
        head, body = "Someone replied to you", "Open the chat to see the reply."
    elif kind == "mention":
        head, body = "You were mentioned", "Open the chat to see the comment."
    elif kind == "announcement":
        head, body = "New announcement", "Open the Inbox to read it."
    elif kind == "invite":
        head, body = "Invite accepted", "Someone joined your workspace."
    elif kind == "credits":
        head, body = "Out of credits", "Add credits on the website to continue."
    else:  # pragma: no cover - the Literal keeps this unreachable
        raise ValueError(f"unknown push kind {kind!r}")
    where = path or "/inbox"
    data: dict[str, Any] = {"kind": kind, "path": where, "v": 1}
    if isinstance(workspace_id, int) and workspace_id > 0:
        data["ws"] = workspace_id
    if server:
        data["server"] = server
    return PushMessage(
        kind=kind,
        title=_with_title(head, title, include_titles),
        body=body,
        path=where,
        data=data,
    )


def expo_payload(message: PushMessage, token: str, *, badge: int | None) -> dict:
    """One message as Expo's push service takes it (one per device token)."""
    payload: dict[str, Any] = {
        "to": token,
        "title": message.title,
        "body": message.body,
        "data": message.data,
        "sound": "default",
        "priority": "high",
        "channelId": ANDROID_CHANNEL,
    }
    if badge is not None:
        payload["badge"] = badge
    return payload
