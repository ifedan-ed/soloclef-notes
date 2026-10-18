"""The chat turns running in this backend process, by thread.

A turn runs in its own asyncio task (see ``relay``) so it outlives the page
that started it. This registry is how the rest of the app asks "is a turn
running on this thread?" and how Stop reaches a turn from any page.

Process-local, like the per-thread busy lock: the backend runs one worker
(``python main.py``). A restart ends every running turn.
"""

from __future__ import annotations

import asyncio

# How long ``POST /threads/{id}/cancel-active-turn`` waits for a stopped turn
# to save what it wrote before answering, so the page's refetch right after
# Stop shows the saved partial answer. The save itself is not cut short when
# the wait runs out (``cleanup.run_cleanup_to_the_end``).
STOP_WAIT_SECONDS = 5.0

_running: dict[str, set[asyncio.Task[None]]] = {}
# Turns Stop has already cancelled. Each is cancelled once: a further cancel
# would only reach the turn's end-of-turn work, which must finish.
_stopped: set[asyncio.Task[None]] = set()


def register(thread_key: str, task: asyncio.Task[None]) -> None:
    """Track ``task`` as a running turn on ``thread_key`` until it finishes."""
    _running.setdefault(thread_key, set()).add(task)
    task.add_done_callback(lambda done: _forget(thread_key, done))


def _forget(thread_key: str, task: asyncio.Task[None]) -> None:
    _stopped.discard(task)
    tasks = _running.get(thread_key)
    if tasks is None:
        return
    tasks.discard(task)
    if not tasks:
        _running.pop(thread_key, None)


def _unfinished(thread_key: str) -> list[asyncio.Task[None]]:
    return [task for task in _running.get(thread_key, ()) if not task.done()]


def is_running(thread_key: str) -> bool:
    """True while a turn on this thread has not finished (saving included)."""
    return bool(_unfinished(thread_key))


def is_stopping(thread_key: str) -> bool:
    """True while a turn Stop has cancelled is still saving what it wrote."""
    return any(task in _stopped for task in _unfinished(thread_key))


async def stop(thread_key: str, *, wait_seconds: float) -> bool:
    """Cancel the thread's running turn once and wait briefly for it to save.

    Cancelling lands inside the turn's stream, whose ``finally`` saves what
    was written so far and releases the thread, exactly as a dropped
    connection used to. A turn that is already stopping is not cancelled
    again; this call then only waits, like ``wait_until_finished``. Returns
    ``False`` when nothing was running.
    """
    tasks = _unfinished(thread_key)
    if not tasks:
        return False
    for task in tasks:
        if task in _stopped or task.cancelling():
            continue
        _stopped.add(task)
        task.cancel()
    await asyncio.wait(tasks, timeout=wait_seconds)
    return True


async def wait_until_finished(thread_key: str, *, wait_seconds: float) -> bool:
    """Wait up to ``wait_seconds`` for the thread's turn to finish; never cancel.

    Returns ``False`` when nothing was running.
    """
    tasks = _unfinished(thread_key)
    if not tasks:
        return False
    await asyncio.wait(tasks, timeout=wait_seconds)
    return True
