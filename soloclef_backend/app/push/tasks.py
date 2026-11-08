"""The Celery side of push: one task sends an event, one reads receipts.

``push_send_task`` runs on the worker so the request or stream that reported
the event never waits for Expo. ``push_receipts_task`` runs from beat every
15 minutes (``celery_app.py``) and drops the tokens of phones that
uninstalled the app.
"""

from __future__ import annotations

import logging
from typing import Any
from uuid import UUID

from sqlalchemy import select

from app.celery_app import celery_app
from app.config import config
from app.push.expo import ExpoPushClient
from app.push.kinds import is_kind
from app.push.message import build_message, notebook_path, path_owner
from app.push.sending import deliver, read_receipts
from app.push.server_origin import this_server
from app.push.store import DbStore
from app.tasks.celery_tasks import get_celery_session_maker, run_async_celery_task

logger = logging.getLogger(__name__)


def _client() -> ExpoPushClient:
    return ExpoPushClient(
        base_url=config.EXPO_PUSH_URL, access_token=config.EXPO_PUSH_ACCESS_TOKEN
    )


async def _resolve_thread(session, payload: dict[str, Any]) -> dict[str, Any]:
    """Fill the person and the notebook from the thread, for events that name only it."""
    thread_id = payload.get("thread_id")
    if not thread_id or (payload.get("user_id") and payload.get("path")):
        return payload
    from app.db import NewChatThread

    thread = (
        await session.execute(
            select(NewChatThread).where(NewChatThread.id == thread_id)
        )
    ).scalar_one_or_none()
    if thread is None:
        return payload
    filled = dict(payload)
    if not filled.get("user_id") and thread.created_by_id:
        filled["user_id"] = str(thread.created_by_id)
    if not filled.get("path"):
        pane = "studio" if filled.get("kind") == "output" else "chat"
        filled["path"] = notebook_path(thread.notebook_id, pane)
    if not filled.get("workspace_id") and thread.workspace_id:
        filled["workspace_id"] = thread.workspace_id
    return filled


async def _resolve_workspace(session, payload: dict[str, Any]) -> dict[str, Any]:
    """Fill the workspace a notebook or automation path belongs to, for the app's switch (``ws``)."""
    if payload.get("workspace_id"):
        return payload
    owner = path_owner(payload.get("path"))
    if owner is None:
        return payload
    from app.automations.persistence.models.automation import Automation
    from app.notebooks.persistence.models import Notebook

    model = Notebook if owner[0] == "notebook" else Automation
    workspace_id = await session.scalar(
        select(model.workspace_id).where(model.id == owner[1])
    )
    return {**payload, "workspace_id": workspace_id} if workspace_id else payload


async def _resolve_title(session, payload: dict[str, Any]) -> dict[str, Any]:
    """The one title a push may carry (``PUSH_INCLUDE_TITLES``): an
    announcement's own, or the name of the notebook the push opens.

    Never a chat's, an output's or a document's title, which are written from
    what was asked or read; a payload that carries one anyway loses it here.
    """
    if payload.get("kind") == "announcement":
        return payload
    title = None
    owner = path_owner(payload.get("path"))
    if owner is not None and owner[0] == "notebook":
        from app.notebooks.persistence.models import Notebook

        title = await session.scalar(
            select(Notebook.title).where(Notebook.id == owner[1])
        )
    return {**payload, "title": title}


async def _send(payload: dict[str, Any]) -> dict[str, Any]:
    if not config.PUSH_ENABLED or not is_kind(payload.get("kind")):
        return {"reason": "off"}
    async with get_celery_session_maker()() as session:
        payload = await _resolve_thread(session, payload)
        payload = await _resolve_workspace(session, payload)
        if config.PUSH_INCLUDE_TITLES:
            payload = await _resolve_title(session, payload)
        else:
            payload = {**payload, "title": None}
        user_id = payload.get("user_id")
        if not user_id:
            return {"reason": "no_user"}
        message = build_message(
            payload["kind"],
            failed=bool(payload.get("failed")),
            path=payload.get("path"),
            title=payload.get("title"),
            output_kind=payload.get("output_kind"),
            variant=payload.get("variant"),
            include_titles=config.PUSH_INCLUDE_TITLES,
            workspace_id=payload.get("workspace_id"),
            # Every push names its sender, so a phone now set to another
            # server ignores it (server_origin).
            server=this_server(),
        )
        delivery = await deliver(
            DbStore(session), _client(), user_id=UUID(user_id), message=message
        )
    logger.info(
        "push: %s for user %s: %s (%d sent, %d dead)",
        payload["kind"],
        user_id,
        delivery.reason,
        delivery.sent,
        len(delivery.dead),
    )
    return {
        "reason": delivery.reason,
        "sent": delivery.sent,
        "dead": len(delivery.dead),
    }


@celery_app.task(name="push_send", bind=True, ignore_result=True)
def push_send_task(self, payload: dict[str, Any]) -> dict[str, Any]:
    return run_async_celery_task(lambda: _send(payload))


async def _receipts() -> dict[str, Any]:
    if not config.PUSH_ENABLED:
        return {"read": 0, "dead": 0}
    async with get_celery_session_maker()() as session:
        read, dead = await read_receipts(DbStore(session), _client())
    if read or dead:
        logger.info("push: %d receipts read, %d dead tokens removed", read, len(dead))
    return {"read": read, "dead": len(dead)}


@celery_app.task(name="push_receipts", bind=True, ignore_result=True)
def push_receipts_task(self) -> dict[str, Any]:
    return run_async_celery_task(_receipts)
