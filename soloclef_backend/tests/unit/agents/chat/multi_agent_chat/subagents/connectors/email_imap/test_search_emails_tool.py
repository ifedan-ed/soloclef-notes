"""The chat tool over IMAP search: unread filter and an honest "truncated"."""

from __future__ import annotations

import pytest

from app.agents.chat.multi_agent_chat.subagents.connectors.email_imap.tools import (
    search_emails as module,
)

pytestmark = pytest.mark.unit


def _hits(n: int, account: str = "a") -> list[dict]:
    return [
        {"account_id": account, "uid": str(i), "date": "", "subject": "s"}
        for i in range(n)
    ]


@pytest.fixture
def calls(monkeypatch):
    seen: list[dict] = []
    answer: dict = {"rows": []}

    async def fake_search(_session, **kwargs):
        seen.append(kwargs)
        return list(answer["rows"])

    monkeypatch.setattr(module, "_search", fake_search)
    return seen, answer


def _tool():
    return module.create_search_emails_tool(
        db_session=object(), workspace_id=1, user_id="u"
    )


async def test_unread_only_reaches_the_search(calls):
    seen, answer = calls
    answer["rows"] = _hits(2)
    await _tool().ainvoke({"since": "2026-10-09", "unread_only": True})
    assert seen[0]["unread_only"] is True
    await _tool().ainvoke({"since": "2026-10-09"})
    assert seen[1]["unread_only"] is False


async def test_a_full_page_with_more_behind_it_says_truncated(calls):
    seen, answer = calls
    answer["rows"] = _hits(11)  # the tool asks for limit + 1
    reply = await _tool().ainvoke({"limit": 10})
    assert seen[0]["limit"] == 11
    assert reply["truncated"] is True
    assert len(reply["results"]) == 10
    assert "More messages match" in reply["message"]


async def test_everything_shown_is_not_truncated(calls):
    _seen, answer = calls
    answer["rows"] = [
        *_hits(3),
        {"account_id": "b", "account_label": "Work", "error": "Could not connect"},
    ]
    reply = await _tool().ainvoke({"limit": 10})
    assert reply["truncated"] is False
    assert "message" not in reply
    assert len(reply["results"]) == 4
