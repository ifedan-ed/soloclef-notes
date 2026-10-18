"""One quick-model call that proposes a report's section suggestions."""

from __future__ import annotations

from langchain_core.messages import HumanMessage, SystemMessage
from sqlalchemy.ext.asyncio import AsyncSession

from app.quick_tasks.language import in_language
from app.quick_tasks.llm import get_quick_llm
from app.utils.structured_output import invoke_json

from .prompt import build_material, build_prompt
from .schemas import ReportSuggestion, ReportSuggestionsReply
from .sections import ReportOutline

_ATTEMPT_TIMEOUT_SECONDS = 30


class QuickModelUnavailableError(RuntimeError):
    """No quick-tasks model is configured for the workspace."""


async def generate_suggestions(
    session: AsyncSession,
    workspace_id: int,
    outline: ReportOutline,
    *,
    language: str,
    kinds: list[str],
) -> list[ReportSuggestion]:
    if not outline.sections or not kinds:
        return []
    llm = await get_quick_llm(session, workspace_id)
    if llm is None:
        raise QuickModelUnavailableError
    prompt = build_prompt(outline, language=language, kinds=kinds)
    material = build_material(outline)
    context = {"sections": len(outline.sections), "kinds": kinds}

    async def ask(extra: str | None) -> ReportSuggestionsReply:
        return await invoke_json(
            llm,
            [
                SystemMessage(content=f"{prompt}\n\n{extra}" if extra else prompt),
                HumanMessage(content=material),
            ],
            ReportSuggestionsReply,
            validation_context=context,
            attempt_timeout=_ATTEMPT_TIMEOUT_SECONDS,
        )

    reply = await in_language(
        ask,
        lambda r: " ".join(f"{s.title} {s.focus}" for s in r.suggestions()),
        language,
    )
    return reply.suggestions()
