"""Which chat models a workspace can run Studio on, by role id or by name.

The same reachability rules as the chat model: an enabled model on an enabled
connection that is the workspace's own or its owner's, or a global model.
"""

from __future__ import annotations

from dataclasses import dataclass

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


@dataclass(frozen=True)
class StudioModel:
    """A usable model: its role id and the provider model id it serves as."""

    id: int
    model_id: str


async def usable_model(
    session: AsyncSession, workspace: Workspace, role_id: int | None
) -> StudioModel | None:
    if not role_id:
        return None
    if role_id < 0:
        model = get_global_model(role_id)
        if (
            not model
            or not has_capability(model, "chat")
            or not get_global_connection(model["connection_id"])
        ):
            return None
        return StudioModel(id=role_id, model_id=model["model_id"])
    model = await _get_db_model(session, role_id, workspace)
    if model is None or not has_capability(model, "chat"):
        return None
    return StudioModel(id=model.id, model_id=model.model_id)


async def named_model(
    session: AsyncSession, workspace: Workspace, model_id: str
) -> StudioModel | None:
    """The workspace's own ``model_id`` first, then its owner's, then a global one."""
    candidates = (
        await session.scalars(
            select(Model.id)
            .join(Connection, Connection.id == Model.connection_id)
            .where(
                Model.model_id == model_id,
                Model.enabled.is_(True),
                Connection.enabled.is_(True),
            )
            .order_by(Connection.workspace_id.is_(None), Model.id)
        )
    ).all()
    for role_id in candidates:
        resolved = await usable_model(session, workspace, role_id)
        if resolved is not None:
            return resolved
    for model in config.GLOBAL_MODELS:
        if model.get("model_id") == model_id:
            resolved = await usable_model(session, workspace, model.get("id"))
            if resolved is not None:
                return resolved
    return None
