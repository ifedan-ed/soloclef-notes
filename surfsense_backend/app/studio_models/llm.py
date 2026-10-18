"""Build the LLM for a resolved Studio model, as the chat model is built.

``load_llm_bundle`` is what the queued-deliverable worker already loads the
chat model with, so the same token tracking and billing metadata apply to
the Studio model, its fallback and Auto alike.
"""

from __future__ import annotations

from typing import Any

from sqlalchemy.ext.asyncio import AsyncSession

from .catalog import StudioModel
from .fallback import StudioModelUnavailableError


async def studio_llm_bundle(
    session: AsyncSession, workspace_id: int, model: StudioModel
) -> tuple[Any, Any]:
    """``(llm, agent_config)`` for ``model``; raises if it cannot be loaded."""
    from app.tasks.chat.streaming.flows.shared.llm_bundle import load_llm_bundle

    llm, agent_config, error = await load_llm_bundle(
        session, config_id=model.id, workspace_id=workspace_id
    )
    if llm is None or agent_config is None or error is not None:
        raise StudioModelUnavailableError(
            f"Studio model {model.model_id} could not be loaded: {error}"
        )
    return llm, agent_config
