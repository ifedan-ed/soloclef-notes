"""Which model a role falls back to: named by provider model id in the environment.

``IMAGE_GEN_FALLBACK_MODEL`` and ``VISION_FALLBACK_MODEL`` name a model id
(``openai-gpt-image-2.5-sunburst``, ``google-gemini-3.8-flash``). It is looked
up among the models the workspace can reach, with the same rules as its role
models (the workspace's own connection first, then its owner's, then the
shared models every account has), and must have the role's capability. Unset, unreachable, or the same model as the
primary means no fallback, and nothing changes.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from typing import Any, Literal

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.config import config
from app.db import Connection, Model, Workspace
from app.services.llm_service import _get_db_model, get_global_connection
from app.services.model_capabilities import has_capability

Role = Literal["image_gen", "vision"]


@dataclass(frozen=True)
class SharedModel:
    """A shared (global) model, shaped like the ``Model`` rows callers use."""

    id: int
    model_id: str
    connection: dict[str, Any]


ENV_BY_ROLE: dict[str, str] = {
    "image_gen": "IMAGE_GEN_FALLBACK_MODEL",
    "vision": "VISION_FALLBACK_MODEL",
}


def fallback_model_name(role: Role) -> str | None:
    return (os.environ.get(ENV_BY_ROLE[role]) or "").strip() or None


async def fallback_model(
    session: AsyncSession,
    workspace: Workspace,
    role: Role,
    *,
    primary_model: str | None,
) -> Model | SharedModel | None:
    """The workspace's usable fallback for ``role``, or None.

    ``primary_model`` is the provider model id the role ran on; falling back
    to the same model would only repeat the failure.
    """
    name = fallback_model_name(role)
    if name is None or name == primary_model:
        return None
    candidates = (
        await session.scalars(
            select(Model.id)
            .join(Connection, Connection.id == Model.connection_id)
            .where(
                Model.model_id == name,
                Model.enabled.is_(True),
                Connection.enabled.is_(True),
            )
            .order_by(Connection.workspace_id.is_(None), Model.id)
        )
    ).all()
    for model_id in candidates:
        model = await _get_db_model(session, model_id, workspace)
        if model is not None and has_capability(model, role):
            return model
    return shared_fallback_model(name, role)


def shared_fallback_model(name: str, role: Role) -> SharedModel | None:
    """The shared (global) model named ``name``, when it can play ``role``."""
    for model in config.GLOBAL_MODELS or []:
        if model.get("model_id") != name or not has_capability(model, role):
            continue
        connection = get_global_connection(model.get("connection_id"))
        if connection is not None:
            return SharedModel(
                id=int(model["id"]), model_id=name, connection=connection
            )
    return None
