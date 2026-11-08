"""Run a scraper capability for a notebook the way the capability REST door does.

Gate on the workspace owner's credit, execute, then charge per billable unit.
Executors can be swapped by capability name, which is the seam tests use to
stand in for the real scrapers. ``charged_micros`` totals what this runner has
billed, so a research search can report its cost in the runs log.

Runners that share one database session (one per source in a search of several
sources) share ``db_lock`` too: the gate and the charge touch the session, which
takes one statement at a time, while the scrapes themselves run side by side.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
from collections.abc import Mapping
from typing import Any

from sqlalchemy.ext.asyncio import AsyncSession

from app.capabilities.core import (
    BillingUnit,
    Capability,
    CapabilityContext,
    Executor,
    charge_capability,
    gate_capability,
)

logger = logging.getLogger(__name__)


class CapabilityRunner:
    def __init__(
        self,
        session: AsyncSession,
        workspace_id: int,
        executors: Mapping[str, Executor] | None = None,
        *,
        gate: bool = True,
        db_lock: asyncio.Lock | None = None,
    ) -> None:
        self._ctx = CapabilityContext(session=session, workspace_id=workspace_id)
        self._executors = executors or {}
        self._gate = gate
        self.charged_micros = 0
        self._db_lock = db_lock

    def _session_turn(self):
        return self._db_lock if self._db_lock else contextlib.nullcontext()

    async def run(self, capability: Capability, payload: Any) -> Any:
        """Execute ``capability``; raises ``InsufficientCreditsError`` from the gate."""
        if self._gate:
            async with self._session_turn():
                await gate_capability(payload, capability.billing_unit, self._ctx)
        executor = self._executors.get(capability.name, capability.executor)
        output = await executor(payload)
        try:
            async with self._session_turn():
                charged = await charge_capability(
                    output, capability.billing_unit, self._ctx
                )
            self.charged_micros += charged or 0
        except Exception:
            # The result is already in hand; a billing hiccup must not lose it.
            logger.exception(
                "charging %s for workspace %s failed",
                capability.name,
                self._ctx.workspace_id,
            )
        return output

    @property
    def workspace_id(self) -> int:
        return self._ctx.workspace_id

    async def charge_pages(self, pages: int) -> int:
        """Charge ``pages`` successfully read pages on the crawl meter.

        For a page read outside the ``web.crawl`` capability (Browserbase),
        priced exactly like a crawled page. Never raises.
        """

        class _Read:
            billable_units = pages
            captcha_attempts = 0

        try:
            async with self._session_turn():
                charged = await charge_capability(
                    _Read(), BillingUnit.WEB_CRAWL, self._ctx
                )
        except Exception:
            logger.exception(
                "charging %d read page(s) for workspace %s failed",
                pages,
                self._ctx.workspace_id,
            )
            return 0
        self.charged_micros += charged or 0
        return charged or 0
