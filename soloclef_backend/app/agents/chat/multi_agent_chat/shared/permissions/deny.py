"""Synthesise a ``ToolMessage`` for a denied tool call.

The denied call is replaced with this message so the model sees a typed
``permission_denied`` error in ``ToolMessage.additional_kwargs["error"]``
and can adjust its plan without retrying the same forbidden call.
"""

from __future__ import annotations

from typing import Any

from langchain_core.messages import ToolMessage

from app.agents.chat.multi_agent_chat.shared.permissions.model import Rule
from app.agents.chat.runtime.errors import StreamingError


def build_deny_message(tool_call: dict[str, Any], rule: Rule) -> ToolMessage:
    err = StreamingError(
        code="permission_denied",
        retryable=False,
        suggestion=rule.reason
        or (
            f"rule permission={rule.permission!r} pattern={rule.pattern!r} "
            f"blocked this call"
        ),
    )
    # A rule with a reason (e.g. the turn's retrieval scope) explains itself;
    # a bare "Permission denied" reads to the model like an access problem it
    # can work around, and it retries.
    content = (
        f"Tool {tool_call.get('name')!r} is unavailable: {rule.reason}"
        if rule.reason
        else (
            f"Permission denied: rule {rule.permission}/{rule.pattern} "
            f"blocked tool {tool_call.get('name')!r}."
        )
    )
    return ToolMessage(
        content=content,
        tool_call_id=tool_call.get("id") or "",
        name=tool_call.get("name"),
        status="error",
        additional_kwargs={"error": err.model_dump()},
    )


def build_reject_message(tool_call: dict[str, Any]) -> ToolMessage:
    """Reject without feedback: model must stop retrying this call and ask the user."""
    err = StreamingError(
        code="permission_denied",
        retryable=False,
        suggestion="Do not retry this call; ask the user how to proceed.",
    )
    return ToolMessage(
        content=(
            f"The user rejected tool {tool_call.get('name')!r}. Do not retry the "
            "same call; ask the user how they would like to proceed."
        ),
        tool_call_id=tool_call.get("id") or "",
        name=tool_call.get("name"),
        status="error",
        additional_kwargs={"error": err.model_dump()},
    )


def build_correction_message(tool_call: dict[str, Any], feedback: str) -> ToolMessage:
    """Reject with feedback: surface the correction so the model can retry differently."""
    err = StreamingError(
        code="permission_denied",
        retryable=True,
        suggestion="Adjust the call per the user's feedback and try again.",
    )
    return ToolMessage(
        content=(
            f"The user rejected tool {tool_call.get('name')!r} with feedback: "
            f"{feedback}"
        ),
        tool_call_id=tool_call.get("id") or "",
        name=tool_call.get("name"),
        status="error",
        additional_kwargs={"error": err.model_dump()},
    )


__all__ = [
    "build_correction_message",
    "build_deny_message",
    "build_reject_message",
]
