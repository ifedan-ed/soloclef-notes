"""Shared image-model invocation below standalone-image persistence."""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Any
from urllib.parse import urlparse

from litellm import aimage_generation
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import selectinload

from app.billing.meter import sibling_sessions
from app.db import Model, Workspace
from app.model_role_fallbacks.named import fallback_model
from app.services.auto_model_pin_service import (
    auto_model_candidates,
    choose_auto_model_candidate,
)
from app.services.billable_calls import billable_call
from app.services.image_gen_billing import resolve_billing_for_image_gen
from app.services.image_gen_router_service import (
    IMAGE_GEN_AUTO_MODE_ID,
    is_image_gen_auto_mode,
)
from app.services.llm_service import get_global_connection, get_global_model
from app.services.model_capabilities import has_capability
from app.services.model_resolver import to_litellm
from app.studio_models.fallback import should_fall_back

logger = logging.getLogger(__name__)


@dataclass(frozen=True, slots=True)
class ImageGenerationResult:
    response: dict[str, Any]
    config_id: int
    provider_model: str | None


async def generate_image_response(
    session: AsyncSession,
    *,
    workspace_id: int,
    prompt: str,
    n: int = 1,
    image_gen_model_id_override: int | None = None,
    usage_type: str = "image_generation",
    size: str | None = None,
    quality: str | None = None,
) -> ImageGenerationResult:
    """Resolve, bill, invoke, and normalize one workspace image model call.

    ``size`` ("1536x1024") and ``quality`` are passed to the provider only
    when given, so every other caller keeps the model's defaults.
    """
    workspace_result = await session.execute(
        select(Workspace).filter(Workspace.id == workspace_id)
    )
    workspace = workspace_result.scalars().first()
    if not workspace:
        raise ValueError("Workspace not found")

    config_id = (
        image_gen_model_id_override or IMAGE_GEN_AUTO_MODE_ID
        if image_gen_model_id_override is not None
        else workspace.image_gen_model_id or IMAGE_GEN_AUTO_MODE_ID
    )
    gen_kwargs: dict[str, Any] = {}
    if n > 1:
        gen_kwargs["n"] = n
    if size:
        gen_kwargs["size"] = size
    if quality:
        gen_kwargs["quality"] = quality

    if is_image_gen_auto_mode(config_id):
        candidates = await auto_model_candidates(
            session,
            workspace_id=workspace_id,
            user_id=workspace.user_id,
            capability="image_gen",
        )
        if not candidates:
            raise ValueError(
                "No image generation models available. "
                "Please add an image model in Settings > Image Models."
            )
        config_id = int(choose_auto_model_candidate(candidates, workspace_id)["id"])

    try:
        response, config_id, provider_base_url = await _invoke(
            session, workspace, config_id, prompt, gen_kwargs, usage_type
        )
    except Exception as exc:
        fallback = await _fallback_after(session, workspace, config_id, exc)
        if fallback is None:
            raise
        logger.warning(
            "Image model %s failed (%s); retrying once on %s",
            config_id,
            type(exc).__name__,
            fallback.model_id,
        )
        response, config_id, provider_base_url = await _invoke(
            session, workspace, fallback.id, prompt, gen_kwargs, usage_type
        )

    response_dict = (
        response.model_dump() if hasattr(response, "model_dump") else dict(response)
    )
    for image in response_dict.get("data") or []:
        raw_url = image.get("url") if isinstance(image, dict) else None
        if raw_url and raw_url.startswith("/") and provider_base_url:
            parsed = urlparse(provider_base_url)
            image["url"] = f"{parsed.scheme}://{parsed.netloc}{raw_url}"

    provider_model = getattr(response, "_hidden_params", {}).get("model")
    return ImageGenerationResult(
        response=response_dict,
        config_id=config_id,
        provider_model=provider_model if isinstance(provider_model, str) else None,
    )


async def _resolve(
    session: AsyncSession, workspace: Workspace, config_id: int
) -> tuple[str, dict[str, Any], str]:
    """The litellm model string and kwargs for a role id, and its model id."""
    if config_id < 0:
        global_model = get_global_model(config_id)
        if not global_model or not has_capability(global_model, "image_gen"):
            raise ValueError(f"Image generation model {config_id} not found")
        global_connection = get_global_connection(global_model["connection_id"])
        if not global_connection:
            raise ValueError(
                f"Image generation connection for model {config_id} not found"
            )
        model_string, resolved_kwargs = to_litellm(
            global_connection,
            global_model["model_id"],
        )
        return model_string, resolved_kwargs, global_model["model_id"]
    model_result = await session.execute(
        select(Model)
        .options(selectinload(Model.connection))
        .filter(Model.id == config_id, Model.enabled.is_(True))
    )
    db_model = model_result.scalars().first()
    if not db_model or not db_model.connection or not db_model.connection.enabled:
        raise ValueError(f"Image generation model {config_id} not found")
    connection = db_model.connection
    if connection.workspace_id is not None and connection.workspace_id != workspace.id:
        raise ValueError(f"Image generation model {config_id} not found")
    if connection.user_id is not None and connection.user_id != workspace.user_id:
        raise ValueError(f"Image generation model {config_id} not found")
    if not has_capability(db_model, "image_gen"):
        raise ValueError(f"Model {config_id} is not image-generation capable")
    model_string, resolved_kwargs = to_litellm(connection, db_model.model_id)
    return model_string, resolved_kwargs, db_model.model_id


async def _invoke(
    session: AsyncSession,
    workspace: Workspace,
    config_id: int,
    prompt: str,
    base_kwargs: dict[str, Any],
    usage_type: str,
) -> tuple[Any, int, str | None]:
    model_string, resolved_kwargs, _name = await _resolve(session, workspace, config_id)
    gen_kwargs = {**base_kwargs, **resolved_kwargs}
    billing_tier, base_model, reserve_micros = await resolve_billing_for_image_gen(
        session,
        config_id,
        workspace,
    )
    # A chat's image is an action of its own; an image drawn for a Studio
    # output (an infographic) is part of that output's price.
    async with billable_call(
        user_id=workspace.user_id,
        workspace_id=workspace.id,
        billing_tier=billing_tier,
        base_model=base_model,
        quota_reserve_micros_override=reserve_micros,
        usage_type=usage_type,
        call_details={"model": base_model, "prompt": prompt[:100]},
        action="image_generation" if usage_type == "image_generation" else None,
        billable_session_factory=sibling_sessions(session),
        units=max(1, int(base_kwargs.get("n") or 1)),
    ):
        response = await aimage_generation(
            prompt=prompt,
            model=model_string,
            **gen_kwargs,
        )
    return response, config_id, resolved_kwargs.get("api_base")


async def _fallback_after(
    session: AsyncSession, workspace: Workspace, config_id: int, exc: Exception
):
    """The fallback image model when ``exc`` is the provider failing, else None."""
    if not should_fall_back(exc):
        return None
    try:
        _, _, primary_name = await _resolve(session, workspace, config_id)
    except ValueError:
        primary_name = None
    return await fallback_model(
        session, workspace, "image_gen", primary_model=primary_name
    )


__all__ = ["ImageGenerationResult", "generate_image_response"]
