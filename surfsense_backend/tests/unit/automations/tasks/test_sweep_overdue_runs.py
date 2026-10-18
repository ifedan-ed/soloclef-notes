"""The every-minute sweep runs both passes even when one of them fails."""

from __future__ import annotations

import pytest

import app.automations.tasks.sweep_overdue_runs as sweep_mod

pytestmark = pytest.mark.unit


class _Session:
    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        return False


@pytest.fixture
def passes(monkeypatch) -> list[str]:
    monkeypatch.setattr(sweep_mod, "get_celery_session_maker", lambda: _Session)
    return []


def _pass(passes: list[str], name: str, *, fails: bool):
    async def run(session, *, now):
        passes.append(name)
        if fails:
            raise RuntimeError(f"{name} hit a bad row")
        return []

    return run


@pytest.mark.parametrize("failing", ["time_out", "restart"])
async def test_one_pass_failing_does_not_stop_the_other(monkeypatch, passes, failing):
    monkeypatch.setattr(
        sweep_mod,
        "time_out_overdue_runs",
        _pass(passes, "time_out", fails=failing == "time_out"),
    )
    monkeypatch.setattr(
        sweep_mod,
        "restart_stalled_queues",
        _pass(passes, "restart", fails=failing == "restart"),
    )

    await sweep_mod._sweep()  # logs the failure, raises nothing

    assert passes == ["time_out", "restart"]
