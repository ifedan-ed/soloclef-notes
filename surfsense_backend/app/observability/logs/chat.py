"""Chat-turn log helpers: bind turn ids, report failed tool calls.

A tool that fails inside a chat turn usually does not fail the turn — the
agent reads the error and carries on — so until now the failure only existed
in the stored message parts. These helpers put one WARNING line per failed
tool call in the backend log: tool name, error type and a short, single-line,
redacted excerpt of the error. Never the tool's input or its full output.
"""

from __future__ import annotations

import logging
from typing import Any

from .context import bind_log_context, get_log_context

logger = logging.getLogger("surfsense.chat.tools")

_MAX_ERROR_CHARS = 300


def bind_chat_turn(config: dict[str, Any] | None) -> None:
    """Bind ``thread_id`` / ``turn_id`` from a LangGraph run config."""
    configurable = (config or {}).get("configurable") or {}
    if not isinstance(configurable, dict):
        return
    bind_log_context(
        thread_id=configurable.get("thread_id"),
        turn_id=configurable.get("turn_id"),
    )


def _thread_suffix(config: dict[str, Any] | None) -> str:
    """`` thread=<id>`` unless the log context already prints it."""
    if get_log_context().get("thread_id"):
        return ""
    configurable = (config or {}).get("configurable") or {}
    thread = configurable.get("thread_id") if isinstance(configurable, dict) else None
    return f" thread={thread}" if thread else ""


def _one_line(text: str) -> str:
    flat = " ".join(str(text).split())
    if len(flat) > _MAX_ERROR_CHARS:
        flat = flat[: _MAX_ERROR_CHARS - 1] + "…"
    return flat


def summarize_tool_error(tool_output: Any, raw_output: Any = None) -> tuple[str, str]:
    """Return ``(error_type, short_message)`` for a failed tool result."""
    error_type = "error"
    message: Any = ""
    if isinstance(tool_output, dict):
        for key in ("error_type", "error_code", "code", "failure_code"):
            value = tool_output.get(key)
            if isinstance(value, str) and value:
                error_type = value
                break
        err = tool_output.get("error")
        if isinstance(err, dict):
            error_type = str(err.get("type") or err.get("code") or error_type)
            message = err.get("message") or err
        elif err:
            message = err
        else:
            message = (
                tool_output.get("message")
                or tool_output.get("detail")
                or tool_output.get("result")
                or ""
            )
    elif tool_output:
        message = tool_output
    if not message and raw_output is not None:
        message = getattr(raw_output, "content", "") or ""
    return error_type, _one_line(message) or "(no message)"


def log_tool_failure(
    *,
    tool_name: str,
    tool_output: Any,
    raw_output: Any = None,
    config: dict[str, Any] | None = None,
    subagent: str | None = None,
) -> None:
    error_type, message = summarize_tool_error(tool_output, raw_output)
    logger.warning(
        "tool failed: tool=%s%s type=%s%s error=%s",
        tool_name,
        f" subagent={subagent}" if subagent else "",
        error_type,
        _thread_suffix(config),
        message,
    )


def log_tool_exception(
    *,
    tool_name: str,
    error: BaseException | Any,
    config: dict[str, Any] | None = None,
) -> None:
    logger.warning(
        "tool raised: tool=%s type=%s%s error=%s",
        tool_name,
        type(error).__name__ if isinstance(error, BaseException) else "error",
        _thread_suffix(config),
        _one_line(str(error)),
    )
