"""The Celery side: the send task does nothing while push is off."""

from __future__ import annotations

from uuid import uuid4

import pytest

from app.push import tasks
from app.push.kinds import KIND_NAMES

pytestmark = pytest.mark.unit


def test_the_send_task_is_off_by_default(monkeypatch) -> None:
    from app.config import config

    monkeypatch.setattr(config, "PUSH_ENABLED", False)
    assert tasks.push_send_task.run({"kind": "credits", "user_id": "x"}) == {
        "reason": "off"
    }
    assert tasks.push_receipts_task.run() == {"read": 0, "dead": 0}


def test_an_unknown_kind_is_dropped_before_any_lookup(monkeypatch) -> None:
    from app.config import config

    monkeypatch.setattr(config, "PUSH_ENABLED", True)
    assert tasks.push_send_task.run({"kind": "smoke", "user_id": "x"}) == {
        "reason": "off"
    }


class _Session:
    """Answers the one lookup ``_resolve_workspace`` makes, and records what it asked."""

    def __init__(self, answer: int | None) -> None:
        self.answer = answer
        self.asked: list[str] = []

    async def scalar(self, statement):
        self.asked.append(str(statement))
        return self.answer


@pytest.mark.asyncio
async def test_the_workspace_is_looked_up_from_the_notebook_or_the_automation() -> None:
    session = _Session(4)
    filled = await tasks._resolve_workspace(
        session, {"kind": "mention", "path": "/notebook/11?pane=chat"}
    )
    assert filled["workspace_id"] == 4
    assert "FROM notebooks" in session.asked[0]

    session = _Session(6)
    filled = await tasks._resolve_workspace(
        session, {"kind": "automation", "path": "/automation/3/runs/9"}
    )
    assert filled["workspace_id"] == 6
    assert "FROM automations" in session.asked[0]


@pytest.mark.asyncio
async def test_a_named_workspace_or_a_path_outside_one_asks_nothing() -> None:
    session = _Session(9)
    named = {"kind": "invite", "path": "/workspace/members", "workspace_id": 5}
    assert await tasks._resolve_workspace(session, named) == named
    for path in ("/inbox", "/you/plan", None):
        payload = {"kind": "source", "path": path}
        assert await tasks._resolve_workspace(session, payload) == payload
    assert session.asked == []
    # A notebook that is gone leaves the push without a workspace (the app opens it in the open one).
    gone = await tasks._resolve_workspace(
        _Session(None), {"kind": "mention", "path": "/notebook/404"}
    )
    assert "workspace_id" not in gone


@pytest.mark.asyncio
async def test_a_title_is_only_an_announcements_or_the_notebooks_name() -> None:
    # PUSH_INCLUDE_TITLES: what the docs promise and nothing more.
    session = _Session("Bronchiolitis guidelines")
    answer = await tasks._resolve_title(
        session,
        {"kind": "answer", "path": "/notebook/11?pane=chat", "title": "Chat about X"},
    )
    assert answer["title"] == "Bronchiolitis guidelines"
    assert "FROM notebooks" in session.asked[0]

    session = _Session("unused")
    news = {"kind": "announcement", "path": "/inbox?tab=announcements", "title": "News"}
    assert await tasks._resolve_title(session, news) == news
    for payload in (
        {"kind": "output", "path": None, "title": "Episode about a patient"},
        {"kind": "automation", "path": "/automation/3/runs/9", "title": "Run"},
        {"kind": "source", "path": "/inbox", "title": "scan.pdf"},
    ):
        assert (await tasks._resolve_title(session, payload))["title"] is None
    assert session.asked == []


class _NoLookups:
    """A session for a push that needs no lookup (no thread, a path outside a workspace)."""

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc) -> None:
        return None


def _send_capturing(monkeypatch, public_url: str | None) -> list:
    from app import brand
    from app.config import config
    from app.push.sending import Delivery

    monkeypatch.setattr(config, "PUSH_ENABLED", True)
    monkeypatch.setattr(config, "PUSH_INCLUDE_TITLES", False)
    monkeypatch.setattr(config, "SURFSENSE_PUBLIC_URL", public_url)
    # A server whose public site lives elsewhere: never the address a push names.
    monkeypatch.setattr(brand, "BRAND_SITE_URL", "https://www.example.org")
    monkeypatch.setattr(tasks, "get_celery_session_maker", lambda: _NoLookups)
    monkeypatch.setattr(tasks, "_client", lambda: None)
    sent: list = []

    async def deliver(store, client, *, user_id, message):
        sent.append(message)
        return Delivery("sent", sent=1)

    monkeypatch.setattr(tasks, "deliver", deliver)
    return sent


@pytest.mark.asyncio
@pytest.mark.parametrize("kind", KIND_NAMES)
async def test_every_push_sent_names_this_servers_origin(monkeypatch, kind) -> None:
    # The app ignores a push whose server is not the one it is now set to.
    sent = _send_capturing(monkeypatch, "https://Notes.Example.org/")
    payload = {"kind": kind, "user_id": str(uuid4()), "path": "/inbox"}
    assert (await tasks._send(payload))["reason"] == "sent"
    assert sent[0].data["server"] == "https://notes.example.org"


@pytest.mark.asyncio
@pytest.mark.parametrize("public_url", [None, "", "http://localhost:3929"])
async def test_a_server_phones_cannot_match_sends_no_server_key(
    monkeypatch, public_url
) -> None:
    # Unset, or http (no phone is set to an http server): a key would have the
    # app drop every push, none has it shown (server_origin).
    sent = _send_capturing(monkeypatch, public_url)
    await tasks._send({"kind": "credits", "user_id": str(uuid4()), "path": "/you/plan"})
    assert "server" not in sent[0].data
