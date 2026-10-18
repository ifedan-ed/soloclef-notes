"""Keep a specialist's older tool results out of its later model calls.

A specialist calls its tools in a loop, and every model call sends the whole
conversation again, so each crawled page or stored-run page went out again
on every later call (thread 117: the web crawler's prompt grew from 4k to
130k tokens over 22 calls, 1.65M tokens for one turn, and each call got
slower). Once a specialist's conversation passes
``SUBAGENT_CONTEXT_BUDGET_TOKENS``, its older tool results are stored
(``tool_output_spills``, kept 7 days) and replaced in what the model sees by
a one-line reference it can read back with ``read_run`` / ``search_run``.
The newest ``SUBAGENT_KEEP_TOOL_RESULTS`` results always stay whole, and the
saved conversation is unchanged: only what is sent to the model shrinks.

Only specialists that have ``read_run`` get this, so a cleared result can
always be read back. Email results are never cleared (``read_email`` /
``search_emails`` are protected, as for the main agent).

``SURFSENSE_SUBAGENT_CONTEXT_BUDGET_TOKENS`` changes the budget (default
40000; 0 turns it off). ``SURFSENSE_ENABLE_CONTEXT_EDITING=false`` turns off
this and the main agent's context editing. Documented in
``custom/docs/chat-turns.md``.
"""

from __future__ import annotations

import os
from collections.abc import Sequence
from typing import Any

from langchain_core.tools import BaseTool

from app.agents.chat.multi_agent_chat.shared.feature_flags import AgentFeatureFlags
from app.agents.chat.multi_agent_chat.shared.middleware.flags import enabled


def _read_budget() -> int:
    raw = os.environ.get("SURFSENSE_SUBAGENT_CONTEXT_BUDGET_TOKENS")
    if raw is None or not raw.strip():
        return 40_000
    try:
        return max(0, int(raw))
    except ValueError:
        return 40_000


SUBAGENT_CONTEXT_BUDGET_TOKENS = _read_budget()
SUBAGENT_KEEP_TOOL_RESULTS = 3


def build_subagent_context_budget_mw(
    *,
    flags: AgentFeatureFlags,
    tools: Sequence[BaseTool],
    workspace_id: int | None,
    budget_tokens: int | None = None,
) -> Any | None:
    """The context-budget middleware for one specialist, or ``None``."""
    budget = SUBAGENT_CONTEXT_BUDGET_TOKENS if budget_tokens is None else budget_tokens
    if budget <= 0 or not enabled(flags, "enable_context_editing"):
        return None
    if not any(tool.name == "read_run" for tool in tools):
        return None

    # Deferred: the main agent's middleware package imports subagent pieces.
    from app.agents.chat.multi_agent_chat.main_agent.context_prune.prune_tool_names import (
        safe_exclude_tools,
    )
    from app.agents.chat.multi_agent_chat.main_agent.middleware.context_editing import (
        SpillingContextEditingMiddleware,
        SpillToBackendEdit,
    )

    return SpillingContextEditingMiddleware(
        edits=[
            SpillToBackendEdit(
                trigger=budget,
                clear_at_least=budget // 2,
                keep=SUBAGENT_KEEP_TOOL_RESULTS,
                exclude_tools=safe_exclude_tools(tools),
            )
        ],
        workspace_id=workspace_id,
    )
