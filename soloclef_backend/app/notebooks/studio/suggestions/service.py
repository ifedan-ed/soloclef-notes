"""Focus suggestions for a Studio dialog: a cached pool, shown three at a time."""

from __future__ import annotations

import logging
from dataclasses import replace

from sqlalchemy.ext.asyncio import AsyncSession

from app.quick_tasks.language import reply_in_language, target_language

from ...persistence import Notebook
from ..source_material import SourceDocument, compile_material
from .generate import PoolRequest, generate_pool
from .kinds import known_variant, suggestion_kind
from .pool import (
    StudioSuggestions,
    cached_pool,
    pool_key,
    store_pool,
    suggestions_key,
    window,
)
from .prompt import language_tag

logger = logging.getLogger(__name__)

_MATERIAL_CHARS = 8_000


def _in(pool, language: str) -> bool:
    return reply_in_language(" ".join([*pool.ideas, *pool.examples]), language)


async def focus_suggestions(
    session: AsyncSession,
    notebook: Notebook,
    documents: list[SourceDocument],
    *,
    kind: str | None = None,
    variant: str | None = None,
    language: str | None = None,
    locale: str | None = None,
    refresh: bool = False,
    generate=None,
) -> StudioSuggestions:
    """The cached pool for this dialog, generated once per source set.

    ``refresh`` replaces the pool with ideas unlike the current ones (the
    dialog's "More ideas" once it has shown them all). A failed call keeps
    whatever pool there was, so a refresh never empties the dialog.
    """
    if not documents:
        return StudioSuggestions()
    name, spec = suggestion_kind(kind)
    tag = language_tag(language)
    request = PoolRequest(
        kind=name,
        variant=known_variant(spec, variant),
        language=tag,
        # The dialog's output language, else the UI locale, else the sources'.
        target_language=target_language(
            locale=tag or locale,
            material="\n\n".join(d.text[:2_000] for d in documents[:3]),
        ),
    )
    sources = suggestions_key([document.id for document in documents])
    key = pool_key(request.kind, request.variant, request.language)
    cached = cached_pool(notebook, sources, key)
    if cached is not None and not _in(cached, request.target_language):
        # A pool written in another language (the quick model drifting into
        # Chinese, say) is replaced rather than shown or avoided.
        cached = None
    if cached is not None and not refresh:
        return window(cached)
    if cached is not None:
        request = replace(request, avoid=cached.ideas)
    try:
        pool = await (generate or generate_pool)(
            session,
            notebook.workspace_id,
            compile_material(documents, _MATERIAL_CHARS),
            request,
        )
    except Exception:
        logger.warning(
            "Focus suggestions failed for notebook %s", notebook.id, exc_info=True
        )
        return window(cached) if cached is not None else StudioSuggestions()
    if not pool.ideas:
        return window(cached) if cached is not None else StudioSuggestions()
    store_pool(notebook, sources, key, pool)
    await session.commit()
    return window(pool)
