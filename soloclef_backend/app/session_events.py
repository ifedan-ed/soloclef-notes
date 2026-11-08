"""SQLAlchemy session event hooks — wired once at app startup.

Detects document folder arrivals across every ORM commit and publishes
``document.entered_folder`` after the transaction is durable. A rollback drops
what the flush collected, so a fact only ever leaves here once it is true.
"""

from __future__ import annotations

import asyncio
import logging

from sqlalchemy import event
from sqlalchemy.orm import Session, attributes

from app.db import Document, DocumentStatus
from app.event_bus.events.document_entered_folder import payload_if_entered_folder
from app.event_bus.publish import publish

logger = logging.getLogger(__name__)

_PENDING_KEY = "_entered_folder_pending"

# A commit's publish outlives the hook that started it, so the task is held
# here: a task nothing references can be collected before it has run.
_publishing: set[asyncio.Task[None]] = set()


def _after_flush(session: Session, flush_context: object) -> None:
    """Collect folder-arrival candidates while attribute history is still available."""
    pending: list[dict] = []

    for obj in list(session.new) + list(session.dirty):
        if not isinstance(obj, Document):
            continue

        history = attributes.get_history(obj, "folder_id")
        if not history.added:
            continue

        new_folder_id = history.added[0]
        previous_folder_id = history.deleted[0] if history.deleted else None

        result = payload_if_entered_folder(
            document_id=obj.id,
            workspace_id=obj.workspace_id,
            new_folder_id=new_folder_id,
            previous_folder_id=previous_folder_id,
            folder_id_changed=True,
            status_state=DocumentStatus.get_state(obj.status) or "",
            document_type=obj.document_type.value if obj.document_type else "",
            title=obj.title or "",
            connector_id=obj.connector_id,
            created_by_id=str(obj.created_by_id) if obj.created_by_id else None,
        )
        if result is not None:
            pending.append(result)

    setattr(session, _PENDING_KEY, pending)


def _after_commit(session: Session) -> None:
    """Publish collected events now that the transaction is durable."""
    pending: list[dict] = getattr(session, _PENDING_KEY, [])
    if not pending:
        return
    setattr(session, _PENDING_KEY, [])

    try:
        loop = asyncio.get_running_loop()
    except RuntimeError:
        logger.warning("No running event loop — skipping %d event(s)", len(pending))
        return

    task = loop.create_task(_publish_in_order(pending))
    _publishing.add(task)
    task.add_done_callback(_publishing.discard)


async def _publish_in_order(pending: list[dict]) -> None:
    """Publish one commit's events one after another, so the stream holds the
    order the flush found them in. One that cannot be stated does not cost the
    rest."""
    for item in pending:
        try:
            await publish(
                item["event_type"],
                item["payload"],
                workspace_id=item["workspace_id"],
            )
        except Exception as exc:
            logger.error("event publish failed for %s: %s", item["event_type"], exc)


def _after_rollback(session: Session) -> None:
    """Discard any pending events — the transaction did not commit."""
    setattr(session, _PENDING_KEY, [])


def register_session_hooks() -> None:
    """Register document folder-arrival hooks on the SQLAlchemy Session class.

    Call once at application startup (e.g. in ``app.app`` lifespan). Idempotent
    — SQLAlchemy deduplicates identical listener registrations.
    """
    event.listen(Session, "after_flush", _after_flush)
    event.listen(Session, "after_commit", _after_commit)
    event.listen(Session, "after_rollback", _after_rollback)
