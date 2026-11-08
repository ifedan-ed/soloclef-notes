"""The full collection after a chat turn runs at most once a minute."""

from __future__ import annotations

import pytest

from app.tasks.chat.streaming.flows.shared import finally_cleanup

pytestmark = pytest.mark.unit


def test_a_full_pass_runs_at_most_once_a_minute(monkeypatch):
    calls = []
    clock = [1000.0]
    monkeypatch.setattr(finally_cleanup.time, "monotonic", lambda: clock[0])
    monkeypatch.setattr(
        finally_cleanup.gc, "collect", lambda gen=2: calls.append(gen) or 0
    )
    monkeypatch.setattr(finally_cleanup, "trim_native_heap", lambda: None)
    monkeypatch.setattr(finally_cleanup, "log_system_snapshot", lambda _label: None)
    monkeypatch.setattr(finally_cleanup, "_last_full_gc", 0.0)

    finally_cleanup.run_gc_pass(log_prefix="t", chat_id=1)
    assert calls == [0, 1, 2]

    clock[0] += 30  # another turn half a minute later: skipped
    finally_cleanup.run_gc_pass(log_prefix="t", chat_id=1)
    assert calls == [0, 1, 2]

    clock[0] += 31  # past the minute: runs again
    finally_cleanup.run_gc_pass(log_prefix="t", chat_id=1)
    assert calls == [0, 1, 2, 0, 1, 2]
