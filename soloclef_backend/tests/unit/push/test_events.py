"""What each event queues, and the chat turn's rule for when a push is earned."""

from __future__ import annotations

from uuid import uuid4

import pytest

from app.push import events
from app.push.events import ANSWER_MIN_SECONDS, turn_rule
from app.tasks.chat.streaming.detached_turn.relay import TurnEnd

pytestmark = pytest.mark.unit

ANN = uuid4()


@pytest.fixture
def queued(monkeypatch) -> list[dict]:
    sent: list[dict] = []
    monkeypatch.setattr(events, "_enqueue", sent.append)
    return sent


def test_a_turn_nobody_was_watching_earns_a_push_only_when_it_took_a_while() -> None:
    long = ANSWER_MIN_SECONDS + 1
    assert turn_rule(
        attached=False, seconds=long, failed=False, cancelled=False, interrupted=False
    ) == [("answer", False)]
    assert turn_rule(
        attached=False, seconds=long, failed=True, cancelled=False, interrupted=False
    ) == [("answer", True)]
    assert (
        turn_rule(
            attached=False, seconds=5, failed=False, cancelled=False, interrupted=False
        )
        == []
    )
    # A page was reading the stream: it saw the answer.
    assert (
        turn_rule(
            attached=True,
            seconds=long,
            failed=False,
            cancelled=False,
            interrupted=False,
        )
        == []
    )
    # Stop: the person did it.
    assert (
        turn_rule(
            attached=False,
            seconds=long,
            failed=False,
            cancelled=True,
            interrupted=False,
        )
        == []
    )


def test_an_approval_waiting_for_someone_who_left_earns_a_push_at_once() -> None:
    assert turn_rule(
        attached=False, seconds=1, failed=False, cancelled=False, interrupted=True
    ) == [("approval", False)]
    assert (
        turn_rule(
            attached=True, seconds=1, failed=False, cancelled=False, interrupted=True
        )
        == []
    )


def test_turn_end_hook_binds_the_thread_and_the_notebook(queued) -> None:
    on_end = events.turn_end_hook(thread_id=42, user_id=ANN, notebook_id=11)
    on_end(
        TurnEnd(
            attached=False, seconds=60, failed=False, cancelled=False, interrupted=False
        )
    )
    assert queued == [
        {
            "kind": "answer",
            "user_id": str(ANN),
            "failed": False,
            "path": "/notebook/11?pane=chat",
            # Never the chat's title; the task may add the notebook's name.
            "title": None,
            "output_kind": None,
            "thread_id": 42,
            "variant": None,
            "workspace_id": None,  # the task reads it from the thread
        }
    ]
    queued.clear()
    on_end(
        TurnEnd(
            attached=False, seconds=1, failed=False, cancelled=False, interrupted=True
        )
    )
    assert [q["kind"] for q in queued] == ["approval"]
    queued.clear()
    on_end(
        TurnEnd(
            attached=True, seconds=99, failed=True, cancelled=False, interrupted=False
        )
    )
    assert queued == []


def test_a_thread_without_a_notebook_opens_the_inbox(queued) -> None:
    events.turn_ended(
        thread_id="abc",
        user_id=ANN,
        notebook_id=None,
        attached=False,
        seconds=30,
        failed=False,
        cancelled=False,
        interrupted=False,
    )
    assert queued[0]["path"] == "/inbox"
    assert queued[0]["thread_id"] is None


def test_outputs_automations_sources_mentions_announcements_invites_credits(
    queued,
) -> None:
    events.output_ended(user_id=None, thread_id=7, output_kind="podcast", failed=False)
    events.automation_run_ended(
        owner_user_id=ANN, automation_id=3, run_id=9, status="failed"
    )
    events.automation_run_ended(
        owner_user_id=ANN, automation_id=3, run_id=10, status="cancelled"
    )
    events.automation_run_ended(
        owner_user_id=None, automation_id=3, run_id=11, status="succeeded"
    )
    events.source_ended(user_id=ANN, failed=True)
    events.source_ended(user_id=ANN, failed=False, connector=True)
    events.mentioned(user_id=ANN, notebook_id=11)
    events.mentioned(user_id=ANN, notebook_id=None, reply=True)
    events.announced([ANN, ANN, uuid4()], title="News")
    events.invite_accepted(inviter_user_id=ANN, workspace_id=5)
    events.invite_accepted(inviter_user_id=None, workspace_id=5)
    events.credits_out(user_id=ANN)

    by_kind = [(q["kind"], q["failed"], q["path"], q["variant"]) for q in queued]
    assert by_kind == [
        ("output", False, None, None),
        ("automation", True, "/automation/3/runs/9", None),
        ("source", True, "/inbox", None),
        ("source", False, "/inbox", "connector"),
        ("mention", False, "/notebook/11?pane=chat", None),
        ("mention", False, "/inbox", "reply"),
        ("announcement", False, "/inbox?tab=announcements", None),
        ("announcement", False, "/inbox?tab=announcements", None),
        ("invite", False, "/workspace/members", None),
        ("credits", False, "/you/plan", None),
    ]
    assert queued[0]["thread_id"] == 7 and queued[0]["output_kind"] == "podcast"
    assert queued[0]["user_id"] is None  # the task reads it from the thread
    assert queued[6]["title"] == "News"
    assert queued[0]["title"] is None  # an output's own title never travels
    # The invite names its workspace; the rest are looked up by the task (None here).
    assert queued[8]["workspace_id"] == 5
    assert all(q["workspace_id"] is None for i, q in enumerate(queued) if i != 8)


def test_nothing_is_queued_while_push_is_off(monkeypatch) -> None:
    from app.config import config

    monkeypatch.setattr(config, "PUSH_ENABLED", False)
    calls: list[dict] = []
    monkeypatch.setattr(
        "app.push.tasks.push_send_task.delay", calls.append, raising=False
    )
    events.credits_out(user_id=ANN)
    assert calls == []


def test_a_broker_that_is_down_never_raises(monkeypatch) -> None:
    from app.config import config

    monkeypatch.setattr(config, "PUSH_ENABLED", True)

    def boom(payload):
        raise ConnectionError("no broker")

    monkeypatch.setattr("app.push.tasks.push_send_task.delay", boom, raising=False)
    events.credits_out(user_id=ANN)  # logs a warning, nothing more
