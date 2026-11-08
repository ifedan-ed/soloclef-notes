"""Stop reaches a turn from any page, including one that did not start it."""

from __future__ import annotations

import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock

import anyio
import pytest
from fastapi import Response

from app.routes import new_chat_routes as routes
from app.tasks.chat.streaming.detached_turn import (
    is_turn_running,
    run_cleanup_to_the_end,
    start_turn_in_background,
)

pytestmark = pytest.mark.unit


class _Session:
    def __init__(self, thread):
        self._thread = thread

    async def execute(self, _statement):
        thread = self._thread
        return SimpleNamespace(scalars=lambda: SimpleNamespace(first=lambda: thread))


@pytest.fixture
def allowed(monkeypatch):
    monkeypatch.setattr(routes, "check_permission", AsyncMock())
    monkeypatch.setattr(routes, "check_thread_access", AsyncMock())


async def test_stop_from_another_page_ends_the_turn_and_it_saves(allowed):
    thread_id = 515151
    saved: list[str] = []

    async def turn():
        try:
            yield "data: working\n\n"
            await asyncio.Event().wait()
        finally:
            with anyio.CancelScope(shield=True):
                saved.append("partial")

    page = start_turn_in_background(thread_id, turn())
    assert await anext(page) == "data: working\n\n"
    await page.aclose()  # the starting tab is gone; Stop comes from elsewhere

    response = Response()
    result = await routes.cancel_active_turn(
        thread_id=thread_id,
        response=response,
        session=_Session(SimpleNamespace(id=thread_id, workspace_id=1)),
        auth=SimpleNamespace(user=SimpleNamespace(id="user-1")),
    )

    assert response.status_code == 202
    assert result.error_code == "TURN_CANCELLING"
    assert saved == ["partial"]
    assert not is_turn_running(str(thread_id))


async def test_stop_with_nothing_running_says_so(allowed):
    result = await routes.cancel_active_turn(
        thread_id=626262,
        response=Response(),
        session=_Session(SimpleNamespace(id=626262, workspace_id=1)),
        auth=SimpleNamespace(user=SimpleNamespace(id="user-1")),
    )
    assert result.status == "idle"
    assert result.error_code == "NO_ACTIVE_TURN"


async def test_stop_from_two_pages_cancels_once_and_both_see_it_stopping(allowed):
    thread_id = 838383
    saved: list[str] = []
    cancels_seen: list[int] = []
    cleanup_started = asyncio.Event()

    async def turn():
        try:
            yield "data: working\n\n"
            await asyncio.Event().wait()
        finally:
            cleanup_started.set()
            this_turn = asyncio.current_task()

            async def save():
                await asyncio.sleep(0.1)
                cancels_seen.append(this_turn.cancelling())
                saved.append("partial")

            await run_cleanup_to_the_end(save())

    page = start_turn_in_background(thread_id, turn())
    assert await anext(page) == "data: working\n\n"

    def press_stop():
        return routes.cancel_active_turn(
            thread_id=thread_id,
            response=Response(),
            session=_Session(SimpleNamespace(id=thread_id, workspace_id=1)),
            auth=SimpleNamespace(user=SimpleNamespace(id="user-1")),
        )

    first_page = asyncio.create_task(press_stop())
    await cleanup_started.wait()
    second_page = asyncio.create_task(press_stop())  # or the same page, twice
    first, second = await asyncio.gather(first_page, second_page)

    assert first.status == second.status == "cancelling"
    assert first.error_code == second.error_code == "TURN_CANCELLING"
    assert saved == ["partial"]
    assert cancels_seen == [1]  # one cancel reached the turn, not two
    assert not is_turn_running(str(thread_id))
