"""Regenerate and Edit: ask a turn again as if its answer had never been given.

``POST /threads/{id}/regenerate`` works out which saved messages are replaced
(``replaced_turn``) and where the agent's memory stood before them
(``rewind_point``), runs the question again through ``stream_new_chat`` from
there with the tool it was asked with (``turn_options``), and deletes the
replaced messages once the agent's memory has moved (``replaced_messages``).
Behaviour and limits: ``custom/docs/chat-turns.md``.
"""

from __future__ import annotations

from .replaced_messages import remove_replaced_messages
from .replaced_turn import FromMessageNotInThreadError, ReplacedTurn, find_replaced_turn
from .rewind_point import RewindPoint, find_rewind_point
from .turn_options import preferred_tool_of_turn, turn_options_configurable

__all__ = [
    "FromMessageNotInThreadError",
    "ReplacedTurn",
    "RewindPoint",
    "find_replaced_turn",
    "find_rewind_point",
    "preferred_tool_of_turn",
    "remove_replaced_messages",
    "turn_options_configurable",
]
