"""Remove the rows a Regenerate replaced once the agent's memory has left them.

The re-run starts at the rewind point, so its first checkpoint takes the
replaced turn out of the agent's memory. From then on the rows go too, also
when the re-run is stopped or fails part-way, so the chat on screen and the
agent's memory agree. When the re-run never reached the agent (no model, no
credit, an error before it started) the memory still holds the old turn and
the rows stay.

Their token usage stays: the database sets its ``message_id`` to NULL, so
what was spent is still in the usage history. (Deleting through the ORM
relationship removed it, so spent tokens dropped out of the history.)
"""

from __future__ import annotations

import logging

from sqlalchemy import delete

from app.db import NewChatMessage, shielded_async_session

from .agent_memory import checkpoint_id_of, latest_checkpoint

logger = logging.getLogger(__name__)


async def remove_replaced_messages(
    thread_id: int, message_ids: list[int], *, latest_before_rerun: str | None
) -> None:
    """Delete ``message_ids`` if the re-run moved the agent's memory; never raise."""
    if not message_ids:
        return
    try:
        if not await _memory_moved(thread_id, latest_before_rerun):
            logger.info(
                "[regenerate] thread=%s: the re-run never reached the agent; "
                "keeping the replaced messages",
                thread_id,
            )
            return
        async with shielded_async_session() as session:
            await session.execute(
                delete(NewChatMessage).where(
                    NewChatMessage.thread_id == thread_id,
                    NewChatMessage.id.in_(message_ids),
                )
            )
            await session.commit()

            from app.services.public_chat_service import delete_affected_snapshots

            await delete_affected_snapshots(session, thread_id, message_ids)
    except Exception as error:
        logger.warning(
            "[regenerate] Failed to delete old messages: %s",
            error,
        )


async def _memory_moved(thread_id: int, latest_before_rerun: str | None) -> bool:
    from app.agents.chat.runtime.checkpointer import get_checkpointer

    latest = await latest_checkpoint(await get_checkpointer(), thread_id)
    latest_id = checkpoint_id_of(latest.config) if latest else None
    return latest_id is not None and latest_id != latest_before_rerun
