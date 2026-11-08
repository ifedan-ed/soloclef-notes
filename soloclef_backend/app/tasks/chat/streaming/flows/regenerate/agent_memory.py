"""The chat agent's memory of one chat: the checkpoints of its main thread.

The main agent checkpoints under the chat id with an empty ``checkpoint_ns``.
Sub-agents checkpoint under thread ids of their own
(``{chat_id}::task:{key}``, see ``subagents/shared/invocation.py``), so they
never show up here.
"""

from __future__ import annotations

from typing import Any

from langgraph.checkpoint.base import BaseCheckpointSaver, CheckpointTuple


def main_thread_config(thread_id: int) -> dict[str, Any]:
    return {"configurable": {"thread_id": str(thread_id), "checkpoint_ns": ""}}


def checkpoint_id_of(config: dict[str, Any] | None) -> str | None:
    if not config:
        return None
    value = (config.get("configurable") or {}).get("checkpoint_id")
    return value if isinstance(value, str) and value else None


async def latest_checkpoint(
    checkpointer: BaseCheckpointSaver, thread_id: int
) -> CheckpointTuple | None:
    return await checkpointer.aget_tuple(main_thread_config(thread_id))
