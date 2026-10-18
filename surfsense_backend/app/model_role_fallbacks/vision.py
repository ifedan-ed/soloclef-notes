"""The vision role's model with a second one behind it.

A vision reply that errors at the provider (a refused image, a rate limit,
an outage) is asked once more of ``VISION_FALLBACK_MODEL``. Everything that
reads images (document pictures, artifact verification, video still review)
gets its model from ``get_vision_llm``, so they all inherit the fallback.
"""

from __future__ import annotations

import logging
from typing import Any

from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Workspace

from .named import fallback_model

logger = logging.getLogger(__name__)


def _served_model(llm: Any) -> str:
    inner = getattr(llm, "_inner", None) or getattr(llm, "inner", None) or llm
    return str(getattr(inner, "model", "") or getattr(llm, "model", "") or "")


async def with_vision_fallback(
    session: AsyncSession, workspace_id: int, llm: Any
) -> Any:
    """``llm`` that falls back once to the configured vision fallback model."""
    if llm is None or not hasattr(llm, "with_fallbacks"):
        # None, or a premium quota wrapper that is not a runnable.
        return llm
    try:
        workspace = await session.get(Workspace, workspace_id)
        if workspace is None:
            return llm
        primary = _served_model(llm)
        model = await fallback_model(
            session,
            workspace,
            "vision",
            primary_model=primary.rsplit("/", 1)[-1] or None,
        )
        if model is None:
            return llm
        from app.agents.chat.runtime.llm_config import SanitizedChatLiteLLM
        from app.services.llm_service import _chat_litellm_from_resolved

        _, kwargs = _chat_litellm_from_resolved(
            conn=model.connection, model_id=model.model_id
        )
        fallback = SanitizedChatLiteLLM(**kwargs)
    except Exception:
        logger.warning("Could not set up the vision fallback model", exc_info=True)
        return llm
    return llm.with_fallbacks([fallback])
