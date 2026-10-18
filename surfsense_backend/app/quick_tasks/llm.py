"""A ready-to-call LLM for a quick task, on the same billed path as chat."""

from __future__ import annotations

from typing import Any

from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Workspace
from app.services.llm_service import _chat_litellm_from_resolved, get_agent_llm

from .resolution import resolve_quick_model
from .thinking import thinking_off_kwargs


def _merge(kwargs: dict[str, Any], extra: dict[str, Any]) -> dict[str, Any]:
    merged = dict(kwargs)
    for key, value in extra.items():
        if isinstance(value, dict) and isinstance(merged.get(key), dict):
            merged[key] = _merge(merged[key], value)
        else:
            merged[key] = value
    return merged


async def quick_llm(session: AsyncSession, workspace_id: int):
    """The quick-tasks model with thinking off; ``None`` means use the chat model.

    Built the way ``get_workspace_llm_instance`` builds the chat model, so the
    token tracker and every other LiteLLM callback see it the same way.
    """
    workspace = await session.get(Workspace, workspace_id)
    if workspace is None:
        return None
    quick = await resolve_quick_model(session, workspace)
    if quick is None:
        return None

    from app.agents.chat.runtime.llm_config import SanitizedChatLiteLLM

    _, kwargs = _chat_litellm_from_resolved(
        conn=quick.connection, model_id=quick.model_id, disable_streaming=True
    )
    return SanitizedChatLiteLLM(**_merge(kwargs, thinking_off_kwargs(quick.model_id)))


async def get_quick_llm(session: AsyncSession, workspace_id: int):
    """The quick-tasks model, falling back to the workspace's chat model."""
    return await quick_llm(session, workspace_id) or await get_agent_llm(
        session, workspace_id, disable_streaming=True
    )
