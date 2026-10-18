"""Fixtures for run-lifecycle tests: committed rows, a controllable action.

These tests need what one rolled-back transaction cannot give: two workers'
sessions racing for the same automation lock, an executor and a canceller on
separate connections. So ``world`` commits real rows through the test engine
and deletes them afterwards (the schema itself is dropped at session end).
"""

from __future__ import annotations

import asyncio
import uuid
from collections.abc import AsyncIterator, Iterator
from dataclasses import dataclass, field
from typing import Any

import pytest
import pytest_asyncio
from pydantic import BaseModel
from sqlalchemy import delete
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.automations.actions import store as action_store
from app.automations.actions.store import register_action
from app.automations.actions.types import ActionDefinition
from app.automations.persistence.enums.run_status import RunStatus
from app.automations.persistence.enums.trigger_type import TriggerType
from app.automations.persistence.models.automation import Automation
from app.automations.persistence.models.run import AutomationRun
from app.automations.persistence.models.trigger import AutomationTrigger
from app.db import User, Workspace
from app.routes.workspaces_routes import create_default_roles_and_membership


class _AnyParams(BaseModel):
    model_config = {"extra": "allow"}


@dataclass
class Gate:
    """The ``test_gate`` action: signals when it starts, waits to be released."""

    started: asyncio.Event = field(default_factory=asyncio.Event)
    release: asyncio.Event = field(default_factory=asyncio.Event)
    cancelled: asyncio.Event = field(default_factory=asyncio.Event)
    calls: int = 0


@pytest.fixture
def gate() -> Iterator[Gate]:
    """Register ``test_gate`` for the test, restore the registry after."""
    snapshot = dict(action_store._REGISTRY)
    state = Gate()

    async def handler(_params: dict[str, Any]) -> dict[str, Any]:
        state.calls += 1
        state.started.set()
        try:
            await state.release.wait()
        except asyncio.CancelledError:
            state.cancelled.set()
            raise
        return {"final_message": "released"}

    register_action(
        ActionDefinition(
            type="test_gate",
            name="Gate",
            description="Waits until the test releases it.",
            params_model=_AnyParams,
            build_handler=lambda _ctx: handler,
        )
    )
    try:
        yield state
    finally:
        action_store._REGISTRY.clear()
        action_store._REGISTRY.update(snapshot)


def definition(
    *,
    concurrency: str = "drop_if_running",
    step_timeout: int | None = None,
    **execution,
) -> dict[str, Any]:
    step: dict[str, Any] = {"step_id": "step_1", "action": "test_gate", "params": {}}
    if step_timeout is not None:
        step["timeout_seconds"] = step_timeout
    return {
        "schema_version": "1.0",
        "name": "Run lifecycle",
        "plan": [step],
        "execution": {
            "timeout_seconds": 60,
            "max_retries": 0,
            "retry_backoff": "none",
            "concurrency": concurrency,
            **execution,
        },
    }


@dataclass
class World:
    """Committed rows shared by separate sessions."""

    sessions: async_sessionmaker[AsyncSession]
    user: User
    workspace_id: int
    automation_ids: list[int] = field(default_factory=list)

    async def automation(self, **kwargs) -> tuple[int, int]:
        """Create an active automation with one schedule trigger; (automation, trigger) ids."""
        async with self.sessions() as session:
            automation = Automation(
                workspace_id=self.workspace_id,
                created_by_user_id=self.user.id,
                name="Run lifecycle",
                definition=definition(**kwargs),
            )
            automation.triggers.append(
                AutomationTrigger(
                    type=TriggerType.SCHEDULE,
                    params={"cron": "* * * * *", "timezone": "UTC"},
                    static_inputs={},
                )
            )
            session.add(automation)
            await session.commit()
            self.automation_ids.append(automation.id)
            return automation.id, automation.triggers[0].id

    async def pending_run(self, automation_id: int, **kwargs) -> int:
        async with self.sessions() as session:
            run = AutomationRun(
                automation_id=automation_id,
                status=RunStatus.PENDING,
                definition_snapshot=definition(**kwargs),
                inputs={},
            )
            session.add(run)
            await session.commit()
            return run.id

    async def run(self, run_id: int) -> AutomationRun:
        async with self.sessions() as session:
            return await session.get(AutomationRun, run_id)

    async def runs(self, automation_id: int) -> list[AutomationRun]:
        async with self.sessions() as session:
            from sqlalchemy import select

            rows = await session.scalars(
                select(AutomationRun)
                .where(AutomationRun.automation_id == automation_id)
                .order_by(AutomationRun.id)
            )
            return list(rows.all())


@pytest_asyncio.fixture
async def world(async_engine) -> AsyncIterator[World]:
    sessions = async_sessionmaker(async_engine, expire_on_commit=False)
    async with sessions() as session:
        user = User(
            id=uuid.uuid4(),
            email=f"runs-{uuid.uuid4().hex[:8]}@surfsense.net",
            hashed_password="hashed",
            is_active=True,
            is_superuser=False,
            is_verified=True,
        )
        session.add(user)
        await session.flush()
        workspace = Workspace(name="Run lifecycle", user_id=user.id)
        session.add(workspace)
        await session.flush()
        await create_default_roles_and_membership(session, workspace.id, user.id)
        await session.commit()
        state = World(sessions=sessions, user=user, workspace_id=workspace.id)
    try:
        yield state
    finally:
        async with sessions() as session:
            await session.execute(
                delete(Workspace).where(Workspace.id == state.workspace_id)
            )
            await session.execute(delete(User).where(User.id == state.user.id))
            await session.commit()
