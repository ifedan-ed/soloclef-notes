"""Which model a workspace's quick tasks run on.

The owner's choice wins while it is still usable. Otherwise the default: the
enabled ``ds-deepseek-v4.1-flash`` on a connection this workspace can use
(its own first, then its owner's), then the shared (global) model of that
name. With none, ``None``: the caller runs the task on the chat model,
exactly as before this setting existed.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Any

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.config import config
from app.db import Connection, Model, Workspace
from app.services.llm_service import (
    _get_db_model,
    get_global_connection,
    get_global_model,
)
from app.services.model_capabilities import has_capability

from .model import WorkspaceQuickModel

logger = logging.getLogger(__name__)

# Approved by the owner on 2026-09-23 ("pin to ds deepseek 4.1").
DEFAULT_QUICK_MODEL_ID = "ds-deepseek-v4.1-flash"
# A stored 0 means "use the chat model", mirroring Auto's 0 in the role columns.
SAME_AS_CHAT = 0


@dataclass(frozen=True)
class QuickModel:
    """A resolved model: its role id, provider model id and connection."""

    id: int
    model_id: str
    connection: Any
    source: str  # "setting" or "default"


async def stored_quick_model_id(session: AsyncSession, workspace_id: int) -> int | None:
    return await session.scalar(
        select(WorkspaceQuickModel.model_id).where(
            WorkspaceQuickModel.workspace_id == workspace_id
        )
    )


async def default_quick_model(
    session: AsyncSession, workspace: Workspace
) -> QuickModel | None:
    candidates = (
        await session.execute(
            select(Model.id, Connection.workspace_id)
            .join(Connection, Connection.id == Model.connection_id)
            .where(
                Model.model_id == DEFAULT_QUICK_MODEL_ID,
                Model.enabled.is_(True),
                Connection.enabled.is_(True),
            )
            # The workspace's own connection before its owner's.
            .order_by(Connection.workspace_id.is_(None), Model.id)
        )
    ).all()
    for model_id, _ in candidates:
        resolved = await _db_quick_model(session, workspace, model_id, "default")
        if resolved is not None:
            return resolved
    # Then the shared (global) model of that name, which every account has.
    for model in config.GLOBAL_MODELS or []:
        if model.get("model_id") == DEFAULT_QUICK_MODEL_ID:
            resolved = _global_quick_model(int(model.get("id") or 0), "default")
            if resolved is not None:
                return resolved
    return None


async def _db_quick_model(
    session: AsyncSession, workspace: Workspace, model_id: int, source: str
) -> QuickModel | None:
    model = await _get_db_model(session, model_id, workspace)
    if model is None or not has_capability(model, "chat"):
        return None
    return QuickModel(
        id=model.id, model_id=model.model_id, connection=model.connection, source=source
    )


def _global_quick_model(model_id: int, source: str = "setting") -> QuickModel | None:
    model = get_global_model(model_id)
    if not model or not has_capability(model, "chat"):
        return None
    connection = get_global_connection(model["connection_id"])
    if not connection:
        return None
    return QuickModel(
        id=model_id, model_id=model["model_id"], connection=connection, source=source
    )


async def resolve_quick_model(
    session: AsyncSession, workspace: Workspace
) -> QuickModel | None:
    """The quick-tasks model, or ``None`` to run on the chat model."""
    chosen = await stored_quick_model_id(session, workspace.id)
    if chosen == SAME_AS_CHAT:
        return None
    if chosen is not None:
        resolved = (
            _global_quick_model(chosen)
            if chosen < 0
            else await _db_quick_model(session, workspace, chosen, "setting")
        )
        if resolved is not None:
            return resolved
        logger.info(
            "Quick-tasks model %s for workspace %s is gone; using the default",
            chosen,
            workspace.id,
        )
    return await default_quick_model(session, workspace)
