"""One model call that fills a suggestion pool, on the quick-tasks model."""

from __future__ import annotations

from dataclasses import dataclass, field

from langchain_core.messages import HumanMessage, SystemMessage
from sqlalchemy.ext.asyncio import AsyncSession

from app.quick_tasks.language import in_language
from app.quick_tasks.llm import get_quick_llm
from app.utils.structured_output import invoke_json

from .kinds import KINDS
from .pool import SuggestionPool
from .prompt import build_prompt

# A quick model answers in a few seconds; this bounds a stuck provider, and
# holds for the one retry separately.
_ATTEMPT_TIMEOUT_SECONDS = 30


@dataclass(frozen=True)
class PoolRequest:
    kind: str
    variant: str | None = None
    language: str | None = None
    # Ideas already shown, when the user asked for more.
    avoid: list[str] = field(default_factory=list)
    # The language code every idea is written in (see app.quick_tasks.language).
    target_language: str = "en"


async def generate_pool(
    session: AsyncSession, workspace_id: int, material: str, request: PoolRequest
) -> SuggestionPool:
    llm = await get_quick_llm(session, workspace_id)
    if llm is None:
        return SuggestionPool()
    kind = KINDS[request.kind]
    prompt = build_prompt(
        kind,
        variant=request.variant,
        language=request.target_language,
        avoid=request.avoid,
    )

    async def ask(extra: str | None):
        return await invoke_json(
            llm,
            [
                SystemMessage(content=f"{prompt}\n\n{extra}" if extra else prompt),
                HumanMessage(content=material),
            ],
            kind.schema,
            attempt_timeout=_ATTEMPT_TIMEOUT_SECONDS,
        )

    reply = await in_language(
        ask,
        lambda r: " ".join([*r.ideas(), *r.examples]),
        request.target_language,
    )
    return SuggestionPool(ideas=reply.ideas(), examples=list(reply.examples))
