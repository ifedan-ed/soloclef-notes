"""Repair tool-call sequences before each agent turn and each model call.

deepagents' ``PatchToolCallsMiddleware`` fixes *dangling* tool calls (an
assistant message whose tool call has no result). The opposite break also
happens: compaction or context editing can drop the assistant message that
made a call while its ``ToolMessage`` stays behind. Providers such as
DeepSeek and OpenAI then refuse the whole request ("Messages with role
'tool' must be a response to a preceding message with 'tool_calls'"), and
every later turn in that thread fails. ``drop_orphan_tool_messages`` removes
such results from what is sent to the model; the stored thread is left as
it is, so nothing is lost and the thread works again.
"""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable, Sequence
from typing import Any

from deepagents.middleware.patch_tool_calls import PatchToolCallsMiddleware
from langchain_core.messages import AIMessage, BaseMessage, ToolMessage

logger = logging.getLogger(__name__)


def drop_orphan_tool_messages(messages: Sequence[BaseMessage]) -> list[BaseMessage]:
    """Return ``messages`` without tool results whose call is not in an earlier assistant message.

    A result counts as answered only by a call made before it, as providers
    require; a result whose id appears in no earlier ``tool_calls`` is dropped.
    """
    seen_calls: set[str] = set()
    kept: list[BaseMessage] = []
    dropped = 0
    for m in messages:
        if isinstance(m, AIMessage):
            for call in m.tool_calls or []:
                call_id = call.get("id")
                if call_id:
                    seen_calls.add(call_id)
        elif isinstance(m, ToolMessage) and m.tool_call_id not in seen_calls:
            dropped += 1
            continue
        kept.append(m)
    if dropped:
        logger.warning("dropped %d tool result(s) whose tool call is no longer in the conversation", dropped)
    return kept


class SequenceRepairMiddleware(PatchToolCallsMiddleware):
    """Patch dangling calls (deepagents) and drop orphan results before every model call."""

    def wrap_model_call(self, request: Any, handler: Callable[[Any], Any]) -> Any:
        messages = list(request.messages or [])
        fixed = drop_orphan_tool_messages(messages)
        if len(fixed) != len(messages):
            request = request.override(messages=fixed)
        return handler(request)

    async def awrap_model_call(self, request: Any, handler: Callable[[Any], Awaitable[Any]]) -> Any:
        messages = list(request.messages or [])
        fixed = drop_orphan_tool_messages(messages)
        if len(fixed) != len(messages):
            request = request.override(messages=fixed)
        return await handler(request)


def build_patch_tool_calls_mw() -> PatchToolCallsMiddleware:
    return SequenceRepairMiddleware()
