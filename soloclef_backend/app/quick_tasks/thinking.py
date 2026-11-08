"""Turning a thinking model's reasoning off for a quick call.

DeepSeek v4.1 Flash thinks by default, and on a three-chip suggestion over
half its output tokens were reasoning. Through the LiteLLM proxy, a
``thinking: {"type": "disabled"}`` body field reaches DeepSeek and stops it
(measured 2026-09-23: 5.4 s and ~1,200 reasoning tokens down to under 1 s and
none). It must travel in ``extra_body``: as a top-level ChatLiteLLM kwarg the
OpenAI-compatible client drops it, and ``reasoning_effort`` does not survive
that client either. Only model families known to accept the field get it, so
a native OpenAI deployment is never sent a body field it would refuse.
"""

from __future__ import annotations

from typing import Any

_THINKING_OFF_FAMILIES = ("deepseek",)


def thinking_off_kwargs(model_id: str) -> dict[str, Any]:
    """ChatLiteLLM kwargs that switch reasoning off for ``model_id``, if known."""
    name = model_id.lower()
    if any(family in name for family in _THINKING_OFF_FAMILIES):
        return {"model_kwargs": {"extra_body": {"thinking": {"type": "disabled"}}}}
    return {}
