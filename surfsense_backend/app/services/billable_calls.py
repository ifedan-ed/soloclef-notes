"""
Per-call billable wrapper for image generation, vision extraction, and any
other short-lived model call that is either an action in its own right or part
of one already charged.

The ``billable_call`` async context manager is the "reserve → execute →
settle / release → record audit row" lifecycle as one primitive. Credit comes
from ``app.billing`` at the admin's flat per-action price; the audit row keeps
the provider cost LiteLLM reported.

KEY DESIGN POINTS:

1. **Session isolation.** ``billable_call`` takes no caller transaction. The
   meter and the audit-row insert run in their own sessions. Route callers use
   ``shielded_async_session()`` by default; Celery callers provide a
   worker-loop-safe session factory, so a hold's commit can never flush or
   roll back rows the caller has staged.

2. **ContextVar safety.** The accumulator is scoped via :func:`scoped_turn`
   (``ContextVar.reset(token)``), so a nested ``billable_call`` inside a chat
   turn cannot corrupt the turn's accumulator.

3. **Everything is audited.** Calls without an ``action`` meter nothing but
   still record a ``TokenUsage`` row with the provider cost.

4. **Refusal raises ``QuotaInsufficientError``** (live billing only), before
   the call runs. Callers translate it into HTTP 402, a failed tool result, or
   a fallback (vision falls back to plain parsing).
"""

from __future__ import annotations

import asyncio
import logging
from collections.abc import AsyncIterator, Callable
from contextlib import AbstractAsyncContextManager, asynccontextmanager, suppress
from typing import Any
from uuid import UUID

from sqlalchemy.ext.asyncio import AsyncSession

from app.config import config
from app.db import shielded_async_session
from app.services.token_tracking_service import (
    TurnTokenAccumulator,
    record_token_usage,
    scoped_turn,
)

logger = logging.getLogger(__name__)

AUDIT_TIMEOUT_SECONDS = 10.0
BACKGROUND_ARTIFACT_USAGE_TYPES = frozenset(
    {"video_presentation_generation", "podcast_generation"}
)
BillableSessionFactory = Callable[[], AbstractAsyncContextManager[AsyncSession]]


class QuotaInsufficientError(Exception):
    """Raised when live billing refuses a billable
    call because the user has exhausted their credit wallet.

    The route handler should catch this and return HTTP 402 Payment
    Required (or the equivalent for the surface area). Outside of the HTTP
    layer (e.g. the ``QuotaCheckedVisionLLM`` wrapper used during indexing)
    callers may catch this and degrade gracefully — e.g. fall back to OCR
    when vision is unavailable.
    """

    def __init__(
        self,
        *,
        usage_type: str,
        balance_micros: int,
        remaining_micros: int,
        code: str = "INSUFFICIENT_CREDITS",
    ) -> None:
        self.usage_type = usage_type
        self.code = code
        self.balance_micros = balance_micros
        self.remaining_micros = remaining_micros
        super().__init__(
            f"Credit exhausted for {usage_type}: "
            f"balance={balance_micros} remaining={remaining_micros} (micro-USD)"
        )


class BillingSettlementError(Exception):
    """Raised when a premium call completed but credit settlement failed."""

    def __init__(self, *, usage_type: str, user_id: UUID, cause: Exception) -> None:
        self.usage_type = usage_type
        self.user_id = user_id
        super().__init__(
            f"Failed to settle premium credit for {usage_type} user={user_id}: {cause}"
        )


async def _rollback_safely(session: AsyncSession) -> None:
    rollback = getattr(session, "rollback", None)
    if rollback is not None:
        with suppress(Exception):
            await rollback()


async def _record_audit_best_effort(
    *,
    session_factory: BillableSessionFactory,
    usage_type: str,
    workspace_id: int,
    user_id: UUID,
    prompt_tokens: int,
    completion_tokens: int,
    total_tokens: int,
    cost_micros: int,
    model_breakdown: dict[str, Any],
    call_details: dict[str, Any] | None,
    thread_id: int | None,
    message_id: int | None,
    audit_label: str,
    timeout_seconds: float = AUDIT_TIMEOUT_SECONDS,
) -> None:
    """Persist a TokenUsage row without letting audit failure block callers.

    Premium settlement is mandatory, but TokenUsage is an audit trail. If the
    audit insert or commit hangs, user-facing artifacts such as videos and
    podcasts must still be able to transition to READY after settlement.
    """
    audit_thread_id = (
        None if usage_type in BACKGROUND_ARTIFACT_USAGE_TYPES else thread_id
    )

    async def _persist() -> None:
        logger.info(
            "[billable_call] audit start label=%s usage_type=%s user=%s thread=%s "
            "total_tokens=%d cost_micros=%d",
            audit_label,
            usage_type,
            user_id,
            audit_thread_id,
            total_tokens,
            cost_micros,
        )
        async with session_factory() as audit_session:
            try:
                await record_token_usage(
                    audit_session,
                    usage_type=usage_type,
                    workspace_id=workspace_id,
                    user_id=user_id,
                    prompt_tokens=prompt_tokens,
                    completion_tokens=completion_tokens,
                    total_tokens=total_tokens,
                    cost_micros=cost_micros,
                    model_breakdown=model_breakdown,
                    call_details=call_details,
                    thread_id=audit_thread_id,
                    message_id=message_id,
                )
                logger.info(
                    "[billable_call] audit row staged label=%s usage_type=%s user=%s thread=%s",
                    audit_label,
                    usage_type,
                    user_id,
                    audit_thread_id,
                )
                await audit_session.commit()
                logger.info(
                    "[billable_call] audit commit OK label=%s usage_type=%s user=%s thread=%s",
                    audit_label,
                    usage_type,
                    user_id,
                    audit_thread_id,
                )
            except BaseException:
                await _rollback_safely(audit_session)
                raise

    try:
        await asyncio.wait_for(_persist(), timeout=timeout_seconds)
    except TimeoutError:
        logger.warning(
            "[billable_call] audit timed out label=%s usage_type=%s user=%s thread=%s "
            "timeout=%.1fs total_tokens=%d cost_micros=%d",
            audit_label,
            usage_type,
            user_id,
            audit_thread_id,
            timeout_seconds,
            total_tokens,
            cost_micros,
        )
    except Exception:
        logger.exception(
            "[billable_call] audit failed label=%s usage_type=%s user=%s thread=%s "
            "total_tokens=%d cost_micros=%d",
            audit_label,
            usage_type,
            user_id,
            audit_thread_id,
            total_tokens,
            cost_micros,
        )


@asynccontextmanager
async def billable_call(
    *,
    user_id: UUID,
    workspace_id: int,
    billing_tier: str = "free",
    base_model: str = "",
    quota_reserve_tokens: int | None = None,
    quota_reserve_micros_override: int | None = None,
    usage_type: str,
    thread_id: int | None = None,
    message_id: int | None = None,
    call_details: dict[str, Any] | None = None,
    billable_session_factory: BillableSessionFactory | None = None,
    audit_timeout_seconds: float = AUDIT_TIMEOUT_SECONDS,
    action: str | None = None,
    units: int = 1,
    need: Any = None,
    notebook_id: int | None = None,
) -> AsyncIterator[TurnTokenAccumulator]:
    """Wrap a single billable LLM/image call.

    Credit is metered per *action* at the admin's flat credit price
    (``app.billing``), not at provider cost:

    - ``action`` set (``"image_generation"``, ``"vision_image"``): the price
      is held before the call (``meter.begin``), settled after it succeeds
      and released if it raises. In live billing mode a refusal raises
      :class:`QuotaInsufficientError` before the call runs, carrying the
      billing ``code``.
    - ``action`` ``None``: the call is part of something already charged (a
      Studio output held its price when it started), so nothing is metered.

    Either way a ``TokenUsage`` audit row records the provider cost LiteLLM
    reported, so cost attribution stays complete.

    ``billing_tier``, ``base_model``, ``quota_reserve_tokens`` and
    ``quota_reserve_micros_override`` are accepted for existing callers and no
    longer affect the charge.
    """
    from app.billing import meter
    from app.billing.errors import BillingDenied

    session_factory = billable_session_factory or shielded_async_session

    async with scoped_turn() as acc:
        charge = None
        if action is not None:
            try:
                charge = await meter.begin(
                    user_id,
                    action,
                    units=units,
                    need=need,
                    workspace_id=workspace_id,
                    notebook_id=notebook_id,
                    thread_id=thread_id,
                    details={"usage_type": usage_type, "model": base_model or ""},
                    session_factory=billable_session_factory,
                )
            except BillingDenied as exc:
                raise QuotaInsufficientError(
                    usage_type=usage_type,
                    balance_micros=exc.available_micros,
                    remaining_micros=exc.available_micros,
                    code=exc.code,
                ) from exc
        try:
            yield acc
        except BaseException:
            if charge is not None:
                try:
                    await meter.release(
                        charge,
                        reason=f"{usage_type} failed",
                        session_factory=billable_session_factory,
                    )
                except Exception:
                    logger.exception(
                        "[billable_call] release failed user=%s usage_type=%s",
                        user_id,
                        usage_type,
                    )
            raise
        finally:
            await _record_audit_best_effort(
                session_factory=session_factory,
                usage_type=usage_type,
                workspace_id=workspace_id,
                user_id=user_id,
                prompt_tokens=acc.total_prompt_tokens,
                completion_tokens=acc.total_completion_tokens,
                total_tokens=acc.grand_total,
                cost_micros=acc.total_cost_micros,
                model_breakdown=acc.per_message_summary(),
                call_details=call_details,
                thread_id=thread_id,
                message_id=message_id,
                audit_label=action or "included",
                timeout_seconds=audit_timeout_seconds,
            )
        if charge is not None:
            try:
                await meter.settle(
                    charge,
                    details={"provider_cost_micros": int(acc.total_cost_micros or 0)},
                    session_factory=billable_session_factory,
                )
            except Exception as settle_exc:
                logger.exception(
                    "[billable_call] settle failed user=%s usage_type=%s",
                    user_id,
                    usage_type,
                )
                raise BillingSettlementError(
                    usage_type=usage_type, user_id=user_id, cause=settle_exc
                ) from settle_exc


async def _resolve_agent_billing_for_workspace(
    session: AsyncSession,
    workspace_id: int,
    *,
    thread_id: int | None = None,
) -> tuple[UUID, str, str]:
    """Resolve ``(owner_user_id, billing_tier, base_model)`` for the workspace
    chat model.

    Used by Celery tasks (podcast generation, video presentation) to bill the
    workspace owner's premium credit pool when the chat model is premium.

    Resolution rules mirror the chat model role resolver:

    - Workspace not found / no ``chat_model_id``: raise ``ValueError``.
    - **Auto mode** (``id == AUTO_MODE_ID == 0``):
        * ``thread_id`` is set: delegate to
          ``resolve_or_get_pinned_llm_config_id`` (the same call chat uses) and
          recurse into the resolved id. Reuses chat's existing pin if present
          so the same model bills for chat + downstream podcast/video. If the
          user is not premium-eligible, the pin service auto-restricts to free
          deployments — denial only happens later in
          ``billable_call.credit_reserve`` if the pin really is premium and
          credit ran out mid-flow.
        * ``thread_id`` is None: fallback to ``("free", "auto")``. Forward-compat
          for any future direct-API path; today both Celery tasks always pass
          ``thread_id``.
    - **Negative id** (global YAML / OpenRouter): ``cfg["billing_tier"]``
      (defaults to ``"free"`` via ``app/config/__init__.py:52`` setdefault),
      ``base_model = litellm_params.get("base_model") or model_name`` —
      NOT provider-prefixed, matching chat's cost-map lookup convention.
    - **Positive id** (user BYOK ``Model``): always free; ``base_model`` from
      the model catalog override or the upstream ``model_id``.

    Note on imports: ``llm_service``, ``auto_model_pin_service``, and
    ``llm_router_service`` are imported lazily inside the function body to
    avoid hoisting litellm side-effects (``litellm.callbacks =
    [token_tracker]``, ``litellm.drop_params``, etc.) into
    ``billable_calls.py``'s module load path.
    """
    from sqlalchemy import select
    from sqlalchemy.orm import selectinload

    from app.db import Model, Workspace

    result = await session.execute(
        select(Workspace).where(Workspace.id == workspace_id)
    )
    workspace = result.scalars().first()
    if workspace is None:
        raise ValueError(f"Workspace {workspace_id} not found")

    chat_model_id = workspace.chat_model_id
    if chat_model_id is None:
        raise ValueError(f"Workspace {workspace_id} has no chat_model_id configured")

    owner_user_id: UUID = workspace.user_id

    from app.services.auto_model_pin_service import (
        AUTO_MODE_ID,
        resolve_or_get_pinned_llm_config_id,
    )

    if chat_model_id == AUTO_MODE_ID:
        if thread_id is None:
            return owner_user_id, "free", "auto"
        try:
            resolution = await resolve_or_get_pinned_llm_config_id(
                session,
                thread_id=thread_id,
                workspace_id=workspace_id,
                user_id=str(owner_user_id),
                selected_llm_config_id=AUTO_MODE_ID,
            )
        except ValueError:
            logger.warning(
                "[agent_billing] Auto-mode pin resolution failed for "
                "workspace=%s thread=%s; falling back to free",
                workspace_id,
                thread_id,
                exc_info=True,
            )
            return owner_user_id, "free", "auto"
        chat_model_id = resolution.resolved_llm_config_id

    if chat_model_id < 0:
        from app.services.llm_service import get_global_llm_config

        cfg = get_global_llm_config(chat_model_id) or {}
        billing_tier = str(cfg.get("billing_tier", "free")).lower()
        litellm_params = cfg.get("litellm_params") or {}
        base_model = litellm_params.get("base_model") or cfg.get("model_name") or ""
        return owner_user_id, billing_tier, base_model

    model_result = await session.execute(
        select(Model)
        .options(selectinload(Model.connection))
        .where(Model.id == chat_model_id, Model.enabled.is_(True))
    )
    model = model_result.scalars().first()
    base_model = ""
    if (
        model is not None
        and model.connection is not None
        and model.connection.enabled
        and (
            model.connection.workspace_id in (None, workspace_id)
            and model.connection.user_id in (None, owner_user_id)
        )
    ):
        catalog = model.catalog or {}
        base_model = catalog.get("base_model") or model.model_id or ""
    return owner_user_id, "free", base_model


__all__ = [
    "BillingSettlementError",
    "QuotaInsufficientError",
    "_resolve_agent_billing_for_workspace",
    "billable_call",
]


# Re-export the config knob so callers don't have to import config just for
# the default image reserve.
DEFAULT_IMAGE_RESERVE_MICROS = config.QUOTA_DEFAULT_IMAGE_RESERVE_MICROS
