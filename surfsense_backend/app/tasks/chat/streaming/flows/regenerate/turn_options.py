"""What a turn was asked with, recorded on its checkpoints for Regenerate.

The composer's Tools menu (``preferred_tool``) applies to one message, and the
page forgets the pick once that message is sent, so Regenerate and Edit cannot
take it from the page. ``stream_new_chat`` puts it in the run's
``configurable``; LangGraph copies string values from there into the metadata
of every checkpoint the run writes, the same way ``turn_id`` gets there.
Regenerate reads it back from the replaced turn's first checkpoint and asks
again with the same tool.
"""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

from app.agents.chat.tool_preference import ToolPreference

PREFERRED_TOOL_KEY = "turn_preferred_tool"


def turn_options_configurable(
    preferred_tool: ToolPreference | None,
) -> dict[str, str]:
    """The ``configurable`` entries that record this turn's options."""
    if preferred_tool is None:
        return {}
    return {PREFERRED_TOOL_KEY: preferred_tool.value}


def preferred_tool_of_turn(
    metadata: Mapping[str, Any] | None,
) -> ToolPreference | None:
    """The tool a turn was asked with, from one of its checkpoints' metadata."""
    if not isinstance(metadata, Mapping):
        return None
    raw = metadata.get(PREFERRED_TOOL_KEY)
    if not isinstance(raw, str):
        return None
    try:
        return ToolPreference(raw)
    except ValueError:
        # A tool that no longer exists: ask again without one.
        return None
