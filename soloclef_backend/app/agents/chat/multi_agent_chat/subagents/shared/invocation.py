"""Subagent-invocation contract shared by the orchestrator and nested subagents.

Both the main-agent ``task`` middleware (``checkpointed_subagent_middleware``)
and subagents that themselves invoke another subagent (e.g.
``ask_knowledge_base``) need the same two things when spawning a child run:

- a ``RunnableConfig`` that raises the recursion limit and isolates the child's
  ``thread_id`` so each invocation lands in its own checkpoint slot
  (``subagent_invoke_config``), and
- the set of parent state keys that must *not* be forwarded into / merged back
  from the child (``EXCLUDED_STATE_KEYS``).

Keeping this here (rather than inside the main-agent middleware) lets subagents
reuse the contract without importing main-agent internals.
"""

from __future__ import annotations

import hashlib
from typing import Any

from langchain.tools import ToolRuntime

# Mirror of deepagents.middleware.subagents._EXCLUDED_STATE_KEYS.
EXCLUDED_STATE_KEYS = frozenset(
    {
        "messages",
        "todos",
        "structured_response",
        "skills_metadata",
        "memory_contents",
    }
)

# Match the parent graph's budget; the LangGraph default of 25 trips on
# multi-step subagent runs.
DEFAULT_SUBAGENT_RECURSION_LIMIT = 10_000

# Length of the derived call key below (hex chars of a sha256 digest). 96 bits
# of entropy is far more than enough to keep sibling/resume calls collision-free
# within one parent thread's lifetime.
_CALL_KEY_LENGTH = 24


def _short_call_key(tool_call_id: str) -> str:
    """Stable, short, checkpoint-safe key derived from a LangChain tool-call id.

    Gemini's ``tool_call.id`` carries a thought signature and routinely runs
    500-3000+ chars. Embedding it verbatim in the subagent's ``thread_id`` (see
    below) pushes LangGraph's own checkpoint tables' composite index
    (``thread_id``, ``checkpoint_ns``, ``channel``, ``version``) past
    Postgres's ~2.7 kB btree entry cap, aborting the turn with
    ``ProgramLimitExceeded: index row size ... exceeds btree version 4
    maximum 2704 for index "checkpoint_blobs_pkey"`` (observed 2026-10-01,
    thread 117). We don't own those tables, so the id is hashed down to a
    fixed-width key instead of truncated — truncation would collide on ids
    that differ only in their (also very long) tail, which is exactly where
    Gemini's thought signatures vary.

    The original ``tool_call_id`` is untouched everywhere else (ToolMessage
    correlation, ``agent_action_log``, deliverable jobs, artifacts, ...); only
    the checkpoint namespace below needs this.
    """
    digest = hashlib.sha256(tool_call_id.encode("utf-8")).hexdigest()
    return digest[:_CALL_KEY_LENGTH]


def subagent_invoke_config(runtime: ToolRuntime) -> dict[str, Any]:
    """RunnableConfig for the nested invoke; raises ``recursion_limit`` and isolates ``thread_id``.

    Each parallel subagent invocation lands in its own checkpoint slot keyed
    by an extended ``thread_id`` of the form
    ``{parent_thread}::task:{short_call_key}``, where ``short_call_key`` is a
    short hash of ``tool_call_id`` (see :func:`_short_call_key`). The same
    call across the resume cycle keeps reading from the same snapshot
    (``tool_call_id`` — and therefore its hash — is stable per LLM-emitted
    call).

    We namespace via ``thread_id`` rather than ``checkpoint_ns`` because
    langgraph's ``aget_state`` interprets a non-empty ``checkpoint_ns`` as a
    subgraph path and raises ``ValueError("Subgraph X not found")``.
    """
    merged: dict[str, Any] = dict(runtime.config) if runtime.config else {}
    current_limit = merged.get("recursion_limit")
    try:
        current_int = int(current_limit) if current_limit is not None else 0
    except (TypeError, ValueError):
        current_int = 0
    if current_int < DEFAULT_SUBAGENT_RECURSION_LIMIT:
        merged["recursion_limit"] = DEFAULT_SUBAGENT_RECURSION_LIMIT

    configurable: dict[str, Any] = dict(merged.get("configurable") or {})
    parent_thread_id = configurable.get("thread_id")
    per_call_suffix = f"task:{_short_call_key(runtime.tool_call_id)}"
    configurable["thread_id"] = (
        f"{parent_thread_id}::{per_call_suffix}"
        if parent_thread_id
        else per_call_suffix
    )
    merged["configurable"] = configurable
    return merged
