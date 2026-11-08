"""What a lock screen shows: a generic line, never content, a title only when allowed."""

from __future__ import annotations

import pytest

from app.push.kinds import KIND_NAMES
from app.push.message import (
    ANDROID_CHANNEL,
    TITLE_MAX,
    build_message,
    expo_payload,
    notebook_path,
    path_owner,
)

pytestmark = pytest.mark.unit


@pytest.mark.parametrize("kind", KIND_NAMES)
def test_every_kind_has_words_and_a_path(kind: str) -> None:
    message = build_message(kind)
    assert message.title and message.body
    assert message.path == "/inbox"
    assert message.data == {"kind": kind, "path": "/inbox", "v": 1}


@pytest.mark.parametrize("kind", KIND_NAMES)
def test_every_kind_names_the_server_it_comes_from_when_it_has_an_address(
    kind: str,
) -> None:
    # The app ignores a push from a server it is no longer set to (tasks gives
    # this server's origin, server_origin.this_server); none: as before.
    sent = build_message(kind, server="https://notes.example.org")
    assert sent.data["server"] == "https://notes.example.org"
    assert "server" not in build_message(kind).data
    assert "server" not in build_message(kind, server="").data


def test_ready_and_failed_read_differently() -> None:
    assert build_message("answer").title == "Your answer is ready"
    assert build_message("answer", failed=True).title == "Answer could not be finished"
    assert build_message("output", output_kind="podcast").title == (
        "Your audio overview is ready"
    )
    assert build_message("output", output_kind="report", failed=True).title == (
        "Your report could not be made"
    )
    assert build_message("output", output_kind="whatever").title == (
        "Your output is ready"
    )
    assert build_message("automation", failed=True).title == "An automation failed"
    assert build_message("source", variant="connector").title == (
        "A connector sync finished"
    )
    assert build_message("source", failed=True).title == "A source could not be added"
    assert build_message("mention", variant="reply").title == "Someone replied to you"


def test_titles_stay_out_unless_the_owner_allows_them() -> None:
    plain = build_message("answer", title="Bronchiolitis guidelines")
    assert "Bronchiolitis" not in plain.title
    allowed = build_message(
        "answer", title="  Bronchiolitis   guidelines ", include_titles=True
    )
    assert allowed.title == "Your answer is ready: Bronchiolitis guidelines"
    long = build_message("announcement", title="x" * 200, include_titles=True)
    assert len(long.title) <= len("New announcement: ") + TITLE_MAX
    assert long.title.endswith("…")
    assert build_message("answer", title="   ", include_titles=True).title == (
        "Your answer is ready"
    )


def test_the_path_is_the_apps_and_carried_in_data() -> None:
    message = build_message("approval", path="/notebook/11?pane=chat")
    assert message.path == "/notebook/11?pane=chat"
    assert message.data["path"] == "/notebook/11?pane=chat"
    assert notebook_path(11, "chat") == "/notebook/11?pane=chat"
    assert notebook_path(11) == "/notebook/11"
    assert notebook_path(None, "chat") == "/inbox"


def test_the_expo_payload_carries_only_the_generic_text_and_the_path() -> None:
    message = build_message("output", output_kind="podcast", path="/notebook/3")
    payload = expo_payload(message, "ExponentPushToken[abc]", badge=4)
    assert payload == {
        "to": "ExponentPushToken[abc]",
        "title": "Your audio overview is ready",
        "body": "Open Studio to see it.",
        "data": {"kind": "output", "path": "/notebook/3", "v": 1},
        "sound": "default",
        "priority": "high",
        "channelId": ANDROID_CHANNEL,
        "badge": 4,
    }
    assert "badge" not in expo_payload(message, "ExponentPushToken[abc]", badge=None)
    named = build_message(
        "credits", path="/you/plan", server="https://notes.example.org"
    )
    assert expo_payload(named, "ExponentPushToken[abc]", badge=None)["data"] == {
        "kind": "credits",
        "path": "/you/plan",
        "v": 1,
        "server": "https://notes.example.org",
    }


def test_the_workspace_travels_as_ws_only_when_there_is_one() -> None:
    with_ws = build_message("mention", path="/notebook/11?pane=chat", workspace_id=4)
    assert with_ws.data == {
        "kind": "mention",
        "path": "/notebook/11?pane=chat",
        "v": 1,
        "ws": 4,
    }
    for none in (None, 0, -2):
        assert (
            "ws"
            not in build_message("credits", path="/you/plan", workspace_id=none).data
        )


def test_which_paths_belong_to_a_workspace() -> None:
    assert path_owner("/notebook/11") == ("notebook", 11)
    assert path_owner("/notebook/11?pane=studio") == ("notebook", 11)
    assert path_owner("/automation/3/runs/9") == ("automation", 3)
    assert path_owner("/automation/3") == ("automation", 3)
    for other in (
        None,
        "",
        "/inbox",
        "/you/plan",
        "/workspace/members",
        "/notebook/x",
        "/notebooks/1",
    ):
        assert path_owner(other) is None
