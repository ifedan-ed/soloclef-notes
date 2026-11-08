"""Every deliverable failure names the job, its kind and the reason in the log."""

from __future__ import annotations

import asyncio
import logging
from types import SimpleNamespace

import pytest

from app.deliverables.jobs import service

pytestmark = pytest.mark.unit


def test_failed_transition_logs_job_kind_code_and_error(monkeypatch, caplog) -> None:
    async def fake_transition(session, conditions, **values):
        return SimpleNamespace(id=42, kind="video", workspace_id=3, **values)

    monkeypatch.setattr(service, "_transition", fake_transition)
    with caplog.at_level(logging.WARNING, logger=service.__name__):
        asyncio.run(
            service.fail_deliverable_job(
                None,
                42,
                failure_code="render_failed",
                internal_error="ffmpeg exited 1\nstderr: bad frame",
            )
        )
    [rec] = [r for r in caplog.records if r.name == service.__name__]
    msg = rec.getMessage()
    assert "job=42 kind=video workspace=3 code=render_failed" in msg
    assert "ffmpeg exited 1 stderr: bad frame" in msg


def test_no_log_when_the_job_was_not_failed(monkeypatch, caplog) -> None:
    async def fake_transition(session, conditions, **values):
        return None

    monkeypatch.setattr(service, "_transition", fake_transition)
    with caplog.at_level(logging.WARNING, logger=service.__name__):
        asyncio.run(service.fail_deliverable_job(None, 7, failure_code="render_failed"))
    assert not [r for r in caplog.records if r.name == service.__name__]
