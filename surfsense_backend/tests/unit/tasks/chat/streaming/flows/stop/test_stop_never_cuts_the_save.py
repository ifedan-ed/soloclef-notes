"""The real chat flows finish saving whatever Stop does, and however often.

Drives ``stream_new_chat`` and ``stream_resume_chat`` through the detached
turn, with the database calls of their cleanup replaced by slow fakes, and
presses Stop twice, or while the turn is already finishing on its own.
"""

from __future__ import annotations

import asyncio
from types import SimpleNamespace

import anyio
import pytest

import app.services.auto_model_pin_service as auto_model_pin_service
from app.tasks.chat.streaming.detached_turn import (
    is_turn_running,
    start_turn_in_background,
    stop_running_turn,
)
from app.tasks.chat.streaming.flows.new_chat import orchestrator as new_chat
from app.tasks.chat.streaming.flows.resume_chat import orchestrator as resume_chat

pytestmark = pytest.mark.unit


class _Turn:
    """Fakes for one turn: where its body waits, and what its cleanup saved."""

    def __init__(self) -> None:
        self.body_reached = asyncio.Event()
        self.cleanup_started = asyncio.Event()
        self.saved: list[str] = []

    async def block_body(self, *_args, **_kwargs):
        self.body_reached.set()
        await asyncio.Event().wait()  # a model call that never returns

    async def close_session_and_clear_ai_responding(self, _session, _chat_id):
        self.cleanup_started.set()
        await asyncio.sleep(0.05)
        self.saved.append("AI-responding flag cleared")

    async def finalize_assistant_message(self, **_kwargs):
        await asyncio.sleep(0.05)
        self.saved.append("reply and token usage saved")


@pytest.fixture
def turn(monkeypatch) -> _Turn:
    fakes = _Turn()
    for module in (new_chat, resume_chat):
        monkeypatch.setattr(
            module,
            "close_session_and_clear_ai_responding",
            fakes.close_session_and_clear_ai_responding,
        )
        monkeypatch.setattr(
            module, "finalize_assistant_message", fakes.finalize_assistant_message
        )
    monkeypatch.setattr(new_chat, "spawn_set_ai_responding_bg", lambda **_: None)
    return fakes


def _new_chat_waiting_for_its_model(monkeypatch, turn: _Turn, chat_id: int):
    monkeypatch.setattr(new_chat, "resolve_initial_auto_pin", turn.block_body)
    return new_chat.stream_new_chat(user_query="hi", workspace_id=1, chat_id=chat_id)


def _new_chat_that_ends_on_its_own(monkeypatch, chat_id: int):
    async def refuse(*_args, **_kwargs):
        return SimpleNamespace(
            error=("SERVER_ERROR", "server_error", "test"), llm_config_id=None
        )

    monkeypatch.setattr(new_chat, "resolve_initial_auto_pin", refuse)
    return new_chat.stream_new_chat(user_query="hi", workspace_id=1, chat_id=chat_id)


def _resume_waiting_for_its_model(monkeypatch, turn: _Turn, chat_id: int):
    monkeypatch.setattr(resume_chat, "set_ai_responding", turn.block_body)
    return resume_chat.stream_resume_chat(
        chat_id=chat_id,
        workspace_id=1,
        decisions=[],
        user_id="00000000-0000-0000-0000-000000000001",
    )


def _resume_that_ends_on_its_own(monkeypatch, chat_id: int):
    async def refuse(*_args, **_kwargs):
        raise ValueError("no model")

    monkeypatch.setattr(
        auto_model_pin_service, "resolve_or_get_pinned_llm_config_id", refuse
    )
    return resume_chat.stream_resume_chat(chat_id=chat_id, workspace_id=1, decisions=[])


async def _drain(page) -> None:
    async for _ in page:
        pass


@pytest.mark.parametrize("flow", ["new_chat", "resume"])
async def test_stop_pressed_twice_still_saves_everything(monkeypatch, turn, flow):
    chat_id = 910001 if flow == "new_chat" else 910002
    frames = (
        _new_chat_waiting_for_its_model(monkeypatch, turn, chat_id)
        if flow == "new_chat"
        else _resume_waiting_for_its_model(monkeypatch, turn, chat_id)
    )
    reader = asyncio.create_task(_drain(start_turn_in_background(chat_id, frames)))
    with anyio.fail_after(5):
        await turn.body_reached.wait()

        first = asyncio.create_task(stop_running_turn(str(chat_id), wait_seconds=3))
        await turn.cleanup_started.wait()
        await stop_running_turn(str(chat_id), wait_seconds=3)  # the second press
        await first
        await reader

    assert turn.saved == ["AI-responding flag cleared", "reply and token usage saved"]
    assert not is_turn_running(str(chat_id))


@pytest.mark.parametrize("flow", ["new_chat", "resume"])
async def test_stop_while_the_turn_is_finishing_still_saves_everything(
    monkeypatch, turn, flow
):
    chat_id = 910003 if flow == "new_chat" else 910004
    frames = (
        _new_chat_that_ends_on_its_own(monkeypatch, chat_id)
        if flow == "new_chat"
        else _resume_that_ends_on_its_own(monkeypatch, chat_id)
    )
    reader = asyncio.create_task(_drain(start_turn_in_background(chat_id, frames)))
    with anyio.fail_after(5):
        await turn.cleanup_started.wait()
        await stop_running_turn(str(chat_id), wait_seconds=3)
        await reader

    assert turn.saved == ["AI-responding flag cleared", "reply and token usage saved"]
    assert not is_turn_running(str(chat_id))
