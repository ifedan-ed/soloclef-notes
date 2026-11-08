"""Watch a deliverables subagent run through LangChain callbacks.

Passed in the run config, so the agent, its tools and their sessions behave
exactly as without it. Model calls made from inside a tool (image or
infographic generation, verification helpers) belong to that tool's phase and
only nudge it.
"""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable
from typing import Any
from uuid import UUID

from langchain_core.callbacks import AsyncCallbackHandler

from .phases import ArtifactProgressTracker, ProgressUpdate

logger = logging.getLogger(__name__)

ProgressReporter = Callable[[ProgressUpdate], Awaitable[None]]


class ArtifactProgressObserver(AsyncCallbackHandler):
    def __init__(
        self, tracker: ArtifactProgressTracker, report: ProgressReporter
    ) -> None:
        self._tracker = tracker
        self._report = report
        self._active_tools: set[UUID] = set()

    async def emit(self, update: ProgressUpdate | None) -> None:
        """Report ``update`` if there is one; reporting never raises."""
        if update is None:
            return
        try:
            await self._report(update)
        except Exception:
            # Progress is cosmetic; it must never fail the run it describes.
            logger.warning("Could not report artifact progress", exc_info=True)

    async def on_chat_model_start(
        self, serialized: dict[str, Any], messages: Any, *, run_id: UUID, **kwargs: Any
    ) -> None:
        await self._model_started()

    async def on_llm_start(
        self, serialized: dict[str, Any], prompts: Any, *, run_id: UUID, **kwargs: Any
    ) -> None:
        await self._model_started()

    async def _model_started(self) -> None:
        if self._active_tools:
            await self.emit(self._tracker.nudge())
        else:
            await self.emit(self._tracker.model_step())

    async def on_llm_new_token(self, token: str, **kwargs: Any) -> None:
        await self.emit(self._tracker.model_tokens())

    async def on_tool_start(
        self,
        serialized: dict[str, Any],
        input_str: str,
        *,
        run_id: UUID,
        **kwargs: Any,
    ) -> None:
        name = (serialized or {}).get("name") or kwargs.get("name") or ""
        self._active_tools.add(run_id)
        await self.emit(self._tracker.tool_started(name))

    async def on_tool_end(self, output: Any, *, run_id: UUID, **kwargs: Any) -> None:
        await self._tool_done(run_id)

    async def on_tool_error(
        self, error: BaseException, *, run_id: UUID, **kwargs: Any
    ) -> None:
        await self._tool_done(run_id)

    async def _tool_done(self, run_id: UUID) -> None:
        if run_id not in self._active_tools:
            return
        self._active_tools.discard(run_id)
        await self.emit(self._tracker.tool_finished())
