"""Unit tests for the periodic schedule checker's enablement-to-task dispatch."""

from __future__ import annotations

from contextlib import asynccontextmanager
from datetime import UTC, datetime, timedelta
from types import SimpleNamespace
from unittest.mock import MagicMock

import pytest

from app.db import SearchSourceConnectorType
from app.tasks.celery_tasks import schedule_checker_task

pytestmark = pytest.mark.unit


class _FakeScalars:
    def __init__(self, enablements):
        self._enablements = enablements

    def all(self):
        return self._enablements


class _FakeDueResult:
    def __init__(self, enablements):
        self._enablements = enablements

    def scalars(self):
        return _FakeScalars(self._enablements)


class _FakeEmptyResult:
    def first(self):
        return None


class _FakeSession:
    """Session stub: first execute() returns due enablements, later ones no rows."""

    def __init__(self, enablements):
        self._results = [_FakeDueResult(enablements)]
        self.commits = 0

    async def execute(self, _query):
        if self._results:
            return self._results.pop(0)
        return _FakeEmptyResult()

    async def commit(self):
        self.commits += 1

    async def rollback(self):
        pass


def _due_enablement(connector_type: SearchSourceConnectorType) -> SimpleNamespace:
    """A due SearchSourceConnectorEnablement, joined to its connector."""
    connector = SimpleNamespace(
        id=42,
        connector_type=connector_type,
        user_id="00000000-0000-0000-0000-000000000001",
        config={},
    )
    return SimpleNamespace(
        connector=connector,
        workspace_id=7,
        indexing_config=None,
        periodic_indexing_enabled=True,
        indexing_frequency_minutes=60,
        next_scheduled_at=datetime.now(UTC) - timedelta(minutes=5),
    )


async def _run_checker(
    monkeypatch: pytest.MonkeyPatch,
    enablement: SimpleNamespace,
    *,
    usable_here: bool = True,
):
    session = _FakeSession([enablement])
    asked: list[tuple[int, int]] = []

    async def _is_enabled(_session, connector, workspace_id):
        asked.append((connector.id, workspace_id))
        return usable_here

    monkeypatch.setattr(schedule_checker_task, "is_enabled_in_workspace", _is_enabled)
    session.asked = asked

    @asynccontextmanager
    async def _session_ctx():
        yield session

    monkeypatch.setattr(
        schedule_checker_task, "get_celery_session_maker", lambda: _session_ctx
    )
    monkeypatch.setattr(
        schedule_checker_task, "is_connector_indexing_locked", lambda _id: False
    )
    await schedule_checker_task._check_and_trigger_schedules()
    return session


@pytest.mark.asyncio
async def test_due_drive_connector_with_selection_is_dispatched(monkeypatch):
    """A due Google Drive enablement with a selection is sent to the Drive task."""
    from app.tasks.celery_tasks import connector_tasks

    task_mock = MagicMock()
    monkeypatch.setattr(connector_tasks, "index_google_drive_files_task", task_mock)

    enablement = _due_enablement(SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR)
    enablement.connector.config = {"selected_folders": [{"id": "f1", "name": "Docs"}]}
    await _run_checker(monkeypatch, enablement)

    task_mock.delay.assert_called_once()
    assert task_mock.delay.call_args.args[3]["folders"] == [
        {"id": "f1", "name": "Docs"}
    ]
    # Dispatched with the enablement's own workspace, not the connector's
    # (account-level, no single workspace).
    assert task_mock.delay.call_args.args[1] == 7


@pytest.mark.asyncio
async def test_workspace_indexing_config_overrides_the_account_default(monkeypatch):
    """The enablement's own indexing_config wins over the connector's config."""
    from app.tasks.celery_tasks import connector_tasks

    task_mock = MagicMock()
    monkeypatch.setattr(connector_tasks, "index_google_drive_files_task", task_mock)

    enablement = _due_enablement(SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR)
    enablement.connector.config = {
        "selected_folders": [{"id": "account-default", "name": "Default"}]
    }
    enablement.indexing_config = {
        "selected_folders": [{"id": "workspace-pick", "name": "This workspace only"}]
    }
    await _run_checker(monkeypatch, enablement)

    task_mock.delay.assert_called_once()
    assert task_mock.delay.call_args.args[3]["folders"] == [
        {"id": "workspace-pick", "name": "This workspace only"}
    ]


@pytest.mark.asyncio
async def test_unmapped_connector_type_does_not_dispatch(monkeypatch):
    """Connector types absent from task_map are skipped without dispatching."""
    from app.tasks.celery_tasks import connector_tasks

    task_mock = MagicMock()
    monkeypatch.setattr(connector_tasks, "index_google_drive_files_task", task_mock)

    enablement = _due_enablement(SearchSourceConnectorType.NEXTCLOUD_CONNECTOR)
    session = await _run_checker(monkeypatch, enablement)

    task_mock.delay.assert_not_called()
    assert session.commits == 0


@pytest.mark.asyncio
async def test_a_workspace_the_owner_is_not_in_is_never_indexed_into(monkeypatch):
    """An enablement for a workspace the connector's owner isn't a member of
    (written there by id, or the owner left) dispatches nothing and has its
    periodic indexing switched off."""
    from app.tasks.celery_tasks import connector_tasks

    task_mock = MagicMock()
    monkeypatch.setattr(connector_tasks, "index_google_drive_files_task", task_mock)

    enablement = _due_enablement(SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR)
    enablement.connector.config = {"selected_folders": [{"id": "f1", "name": "Docs"}]}
    session = await _run_checker(monkeypatch, enablement, usable_here=False)

    task_mock.delay.assert_not_called()
    assert session.asked == [(42, 7)]
    assert enablement.periodic_indexing_enabled is False
    assert enablement.next_scheduled_at is None
    assert session.commits == 1
