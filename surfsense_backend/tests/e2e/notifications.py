"""Test-only: put notifications in an account's inbox for the web suite.

``POST /__e2e__/notifications`` (mint secret required) inserts the rows a
handler would, so the Notifications panel's Delete and Clear all can be tried
on a known inbox; Zero syncs them like any other. Mounted by
``run_backend.py``; ``tests/`` never ships.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

from fastapi import APIRouter, FastAPI, Header, HTTPException
from pydantic import BaseModel, Field
from sqlalchemy import func, select

from app.notifications.types import NotificationType
from tests.e2e.auth_mint import _expected_secret

router = APIRouter(prefix="/__e2e__", tags=["__e2e__"])


class SeedNotification(BaseModel):
    type: NotificationType
    title: str
    message: str = "E2E notification"
    read: bool = False
    workspace_id: int | None = None
    metadata: dict = Field(default_factory=dict)
    minutes_ago: int = 0
    # A job still running: status "in_progress", with the job's Redis heartbeat
    # set for RUNNING_HEARTBEAT_S, so cleanup_stale_indexing_notifications (every
    # 5 minutes) does not mark it failed while a test looks at it.
    running: bool = False


# Longer than any test that seeds a running row; it lapses by itself afterwards.
RUNNING_HEARTBEAT_S = 600


class SeedNotifications(BaseModel):
    email: str
    notifications: list[SeedNotification]


@router.post("/notifications", status_code=201)
async def seed_notifications(
    body: SeedNotifications,
    x_e2e_mint_secret: str = Header(..., alias="X-E2E-Mint-Secret"),
) -> dict:
    if x_e2e_mint_secret != _expected_secret():
        raise HTTPException(status_code=403, detail="invalid e2e mint secret")

    from app.db import User, async_session_maker
    from app.notifications.persistence import Notification

    async with async_session_maker() as session:
        user_id = await session.scalar(
            select(User.id).where(func.lower(User.email) == body.email.strip().lower())
        )
        if user_id is None:
            raise HTTPException(status_code=404, detail="no such account")
        now = datetime.now(UTC)
        rows = [
            Notification(
                user_id=user_id,
                workspace_id=n.workspace_id,
                type=n.type,
                title=n.title,
                message=n.message,
                read=n.read,
                notification_metadata=(
                    {**n.metadata, "status": "in_progress"} if n.running else n.metadata
                ),
                created_at=now - timedelta(minutes=n.minutes_ago),
                updated_at=now - timedelta(minutes=n.minutes_ago),
            )
            for n in body.notifications
        ]
        session.add_all(rows)
        await session.commit()
        running = [
            row.id for row, n in zip(rows, body.notifications, strict=True) if n.running
        ]
        if running:
            import redis

            from app.config import config

            beat = redis.from_url(config.REDIS_APP_URL)
            for row_id in running:
                # The key the jobs refresh (document_tasks._get_heartbeat_key).
                beat.setex(f"indexing:heartbeat:{row_id}", RUNNING_HEARTBEAT_S, "e2e")
        return {"ids": [row.id for row in rows]}


def install(app: FastAPI) -> None:
    app.include_router(router)
