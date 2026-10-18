"""
Vision LLM proxy that meters every ``ainvoke`` as one ``vision_image`` action.

Used by :func:`app.services.llm_service.get_vision_llm` so callers in the
indexing pipeline (file processors, connector indexers, etl pipeline) can
keep invoking the LLM exactly the way they do today — ``await llm.ainvoke(...)``
— without threading ``user_id`` through every parser. The wrapper looks like
a chat model from the outside; on the inside it routes each call through
``billable_call`` so the user's premium credit pool is reserved → finalized
or released, and a ``TokenUsage`` audit row is written.

``get_vision_llm`` wraps whatever model it resolves for document extraction
(``usage_type="vision_extraction"``); vision used inside an already-charged
Studio output (video still review, artifact checks) is not wrapped.

Why a wrapper instead of plumbing ``user_id`` through every caller:

* The indexer ecosystem has several entry points (Google Drive,
  file-processor, ETL pipeline) each calling
  ``parse_with_vision_llm(...)``. Adding a ``user_id`` argument to each is
  invasive, error-prone, and easy for a future indexer to forget.
* Per the design (issue M), we always debit the *workspace owner*, not
  the triggering user, so ``user_id`` is fully derivable from the search
  space the caller is already operating on. The wrapper captures it once
  at construction time.
* ``langchain_litellm.ChatLiteLLM`` has no public hook for "before each
  call run this coroutine"; subclassing isn't safe across versions because
  it derives from ``BaseChatModel`` which expects specific Pydantic shapes.
  Composition via attribute proxying (``__getattr__``) is robust to
  upstream changes — every method other than ``ainvoke`` falls through to
  the inner LLM unchanged.
"""

from __future__ import annotations

import logging
from typing import Any
from uuid import UUID

from app.services.billable_calls import QuotaInsufficientError, billable_call

logger = logging.getLogger(__name__)


class QuotaCheckedVisionLLM:
    """Composition wrapper around a langchain chat model that meters every
    ``ainvoke`` as one ``vision_image`` action (``app.billing``).

    Anything other than ``ainvoke`` is forwarded to the inner model so
    ``invoke`` (sync), ``astream``, ``with_structured_output``, etc. all
    still work — they simply bypass metering, which is fine because the
    indexing pipeline only ever calls ``ainvoke``.
    """

    def __init__(
        self,
        inner_llm: Any,
        *,
        user_id: UUID,
        workspace_id: int,
        billing_tier: str = "free",
        base_model: str = "",
        quota_reserve_tokens: int | None = None,
        usage_type: str = "vision_extraction",
        action: str | None = "vision_image",
    ) -> None:
        self._inner = inner_llm
        self._user_id = user_id
        self._workspace_id = workspace_id
        self._billing_tier = billing_tier
        self._base_model = base_model
        self._quota_reserve_tokens = quota_reserve_tokens
        self._usage_type = usage_type
        self._action = action

    async def ainvoke(self, input: Any, *args: Any, **kwargs: Any) -> Any:
        """Proxied async invoke that runs the underlying call inside
        ``billable_call``.

        Raises:
            QuotaInsufficientError: when live billing refuses the image (plan
                without vision, or out of credit). The caller
                (``etl_pipeline_service._extract_image``) falls back to the
                document parser.
        """
        from app.billing.entitlements import Requirement

        async with billable_call(
            user_id=self._user_id,
            workspace_id=self._workspace_id,
            billing_tier=self._billing_tier,
            base_model=self._base_model,
            quota_reserve_tokens=self._quota_reserve_tokens,
            usage_type=self._usage_type,
            call_details={"model": self._base_model},
            action=self._action,
            need=Requirement(vision=True) if self._action else None,
        ):
            return await self._inner.ainvoke(input, *args, **kwargs)

    def __getattr__(self, name: str) -> Any:
        """Forward everything else (``invoke``, ``astream``, ``bind``,
        ``with_structured_output``, …) to the inner model.

        ``__getattr__`` is only consulted when the attribute is *not*
        already found on the proxy, which is exactly the contract we
        want — methods we override stay on the proxy, the rest fall
        through.
        """
        return getattr(self._inner, name)


__all__ = ["QuotaCheckedVisionLLM", "QuotaInsufficientError"]
