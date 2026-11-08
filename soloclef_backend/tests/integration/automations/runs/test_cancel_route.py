"""``POST /automations/{id}/runs/{run_id}/cancel`` over HTTP."""

from __future__ import annotations

import pytest

from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.models.automation import Automation
from app.automations.persistence.models.run import AutomationRun

from .conftest import definition

pytestmark = pytest.mark.integration


async def _run(db_session, db_workspace, db_user, status: RunStatus) -> AutomationRun:
    automation = Automation(
        workspace_id=db_workspace.id,
        created_by_user_id=db_user.id,
        name="Route",
        definition=definition(),
    )
    db_session.add(automation)
    await db_session.flush()
    run = AutomationRun(
        automation_id=automation.id,
        status=status,
        definition_snapshot=definition(),
        inputs={},
    )
    db_session.add(run)
    await db_session.flush()
    return run


async def test_cancel_route_cancels_a_running_run(
    client, db_session, db_workspace, db_user
):
    run = await _run(db_session, db_workspace, db_user, RunStatus.RUNNING)

    response = await client.post(
        f"/api/v1/automations/{run.automation_id}/runs/{run.id}/cancel"
    )

    assert response.status_code == 200
    assert response.json()["status"] == "cancelled"


async def test_cancel_route_refuses_a_finished_run(
    client, db_session, db_workspace, db_user
):
    run = await _run(db_session, db_workspace, db_user, RunStatus.SUCCEEDED)

    response = await client.post(
        f"/api/v1/automations/{run.automation_id}/runs/{run.id}/cancel"
    )

    assert response.status_code == 409
    assert response.json()["detail"] == "This run already finished."
