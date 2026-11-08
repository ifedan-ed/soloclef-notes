"""Where the agent's memory stood before the turn a Regenerate replaces.

LangGraph keeps a chat as a chain of checkpoints, each pointing at its parent,
and a turn's run stamps its ``turn_id`` on every checkpoint it writes. The
rewind point is the parent of the first checkpoint of the first replaced turn
that reached the agent. The re-run starts there, so the agent sees the chat as
it was before the replaced question; the old answer, and any later turns an
Edit replaces, stay on a branch nothing reads.

The turn is found by its own id, not by position: after one Regenerate the
abandoned branch is older than the new turn but is not its parent, so a second
Regenerate still lands before the question.

Rows saved before turn ids existed have none. For the latest question the
chain is walked back from the newest checkpoint to the first one that no
longer holds it. An Edit of an older such question re-runs on the latest
state, as before.

A rewind point that holds no messages is the start of the chat's memory: the
chat's first run, or a re-run of its first question, which forks from that
same start. A copied chat's first run took its earlier messages from the
database as history, so every re-run from there is given them again, not
only the first Regenerate (a second one, or an Edit after one, lands on the
same empty start through the first re-run's parent link).
"""

from __future__ import annotations

import logging
from dataclasses import dataclass

from langchain_core.messages import HumanMessage
from langgraph.checkpoint.base import BaseCheckpointSaver, CheckpointTuple

from app.agents.chat.tool_preference import ToolPreference

from .agent_memory import checkpoint_id_of, latest_checkpoint, main_thread_config
from .replaced_turn import ReplacedTurn
from .turn_options import preferred_tool_of_turn

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class RewindPoint:
    checkpoint_id: str | None
    """The checkpoint the re-run starts from; None: the chat's latest state."""
    rebuild_history: bool
    """The re-run starts from an empty memory (before the chat's first run),
    so the history that run took from the database (a copied chat's
    messages) has to be given again."""
    preferred_tool: ToolPreference | None
    """The tool the replaced question was asked with."""
    latest_checkpoint_id: str | None
    """The chat's newest checkpoint before the re-run."""


async def find_rewind_point(
    checkpointer: BaseCheckpointSaver, *, thread_id: int, replaced: ReplacedTurn
) -> RewindPoint:
    latest = await latest_checkpoint(checkpointer, thread_id)
    if latest is None:
        return RewindPoint(None, False, None, None)
    latest_id = checkpoint_id_of(latest.config)

    if replaced.turn_id is not None:
        for turn_id in replaced.turn_ids:
            first = await _first_checkpoint_of_turn(checkpointer, thread_id, turn_id)
            if first is None:
                continue
            tool = (
                preferred_tool_of_turn(first.metadata)
                if turn_id == replaced.turn_id
                else None
            )
            return await _start_before(checkpointer, first, tool, latest_id)
        # None of the replaced turns reached the agent (they failed before
        # it started), so its memory is already as it was before them.
        return RewindPoint(None, False, None, latest_id)

    if replaced.is_latest_turn:
        before = await _before_latest_question(checkpointer, latest)
        if before is not None:
            return RewindPoint(
                checkpoint_id_of(before.config),
                _holds_no_messages(before),
                None,
                latest_id,
            )
    logger.warning(
        "[regenerate] thread=%s message=%s has no turn id and is not the latest "
        "question found in the agent's memory; re-running on the latest state",
        thread_id,
        replaced.user_message_id,
    )
    return RewindPoint(None, False, None, latest_id)


async def _first_checkpoint_of_turn(
    checkpointer: BaseCheckpointSaver, thread_id: int, turn_id: str
) -> CheckpointTuple | None:
    first: CheckpointTuple | None = None
    async for saved in checkpointer.alist(
        main_thread_config(thread_id), filter={"turn_id": turn_id}
    ):
        saved_id = checkpoint_id_of(saved.config)
        if saved_id is None:
            continue
        if first is None or saved_id < (checkpoint_id_of(first.config) or ""):
            first = saved
    return first


async def _start_before(
    checkpointer: BaseCheckpointSaver,
    first: CheckpointTuple,
    tool: ToolPreference | None,
    latest_id: str | None,
) -> RewindPoint:
    parent_id = checkpoint_id_of(first.parent_config)
    if parent_id is None:
        # The turn was the chat's first run. Its first checkpoint was written
        # before its input was applied, and LangGraph drops that input when
        # it is given a new one, so the re-run starts there.
        return RewindPoint(checkpoint_id_of(first.config), True, tool, latest_id)
    # A re-run of the chat's first question forked from that first
    # checkpoint, so it is this turn's parent: the memory there is still
    # empty and the copied history has to be given again.
    parent = await checkpointer.aget_tuple(first.parent_config)
    empty = parent is not None and _holds_no_messages(parent)
    return RewindPoint(parent_id, empty, tool, latest_id)


async def _before_latest_question(
    checkpointer: BaseCheckpointSaver, latest: CheckpointTuple
) -> CheckpointTuple | None:
    question = next(
        (m for m in reversed(_messages(latest)) if isinstance(m, HumanMessage)), None
    )
    if question is None or not question.id:
        return None
    current: CheckpointTuple | None = latest
    while current is not None:
        if all(getattr(m, "id", None) != question.id for m in _messages(current)):
            return current
        if current.parent_config is None:
            return None
        current = await checkpointer.aget_tuple(current.parent_config)
    return None


def _holds_no_messages(saved: CheckpointTuple) -> bool:
    return not _messages(saved)


def _messages(saved: CheckpointTuple) -> list:
    values = (saved.checkpoint or {}).get("channel_values") or {}
    messages = values.get("messages")
    return list(messages) if isinstance(messages, list) else []
