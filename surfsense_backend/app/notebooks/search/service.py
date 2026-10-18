"""Search inside every notebook of a workspace at once.

One hybrid query covers the union of all notebooks' in-scope documents: the
same BGE-M3 vector ranking and full-text ranking the chat retriever uses,
fused with reciprocal rank fusion, then (when the configured reranker is on)
reordered by it. Notes are not embedded, so they are matched by keyword only
(full text with word prefixes, plus a plain substring match) and join the
same ranking. Each passage is then placed under every notebook it belongs to.

A purely semantic neighbour is only kept when it is genuinely close: the
nearest chunks of an unrelated notebook are still "nearest", and a search box
that lists every notebook for every query is noise.
"""

from __future__ import annotations

import asyncio
import logging
import time
from collections.abc import Callable
from dataclasses import dataclass, field, replace
from typing import Any

from sqlalchemy import Float, func, literal, null, or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Chunk, Document

from ..persistence import Notebook, NotebookNote
from .grouping import (
    MAX_NOTEBOOKS,
    MAX_PASSAGES,
    MAX_PER_NOTEBOOK,
    Candidate,
    Group,
    group_passages,
)
from .scope import notebook_document_links
from .text import has_literal_match, make_snippet, prefix_tsquery, query_terms

logger = logging.getLogger(__name__)

MIN_QUERY_CHARS = 2
MAX_QUERY_CHARS = 200

# Reciprocal rank fusion constant, as in the chat retriever.
_RRF_K = 60
# Chunks each arm of the hybrid query contributes before fusion.
_ARM_LIMIT = 60
# A chunk found only by vector similarity must be at least this close
# (cosine similarity, BGE-M3) to count as a match.
MIN_SEMANTIC_SIMILARITY = 0.55
# Passages handed to the reranker, and the score below which a passage with no
# literal match is dropped after reranking.
_RERANK_POOL = 10
_RERANK_TEXT_CHARS = 400
MIN_RERANK_SCORE = 0.2
_NOTE_LIMIT = 30
_EMBED_TIMEOUT_S = 3.0

Embedder = Callable[[str], Any]


@dataclass
class SearchOutcome:
    query: str
    terms: list[str]
    groups: list[Group]
    notebooks: dict[int, Notebook]
    took_ms: int
    reranked: bool
    semantic: bool
    stats: dict[str, Any] = field(default_factory=dict)


def _default_embedder() -> Embedder:
    from app.embedding_roles.query_embedding import embed_query

    return embed_query


def _default_reranker():
    from app.services.reranker_service import RerankerService

    return RerankerService.get_reranker_instance()


async def _embed(embed: Embedder, query: str) -> Any | None:
    try:
        return await asyncio.wait_for(
            asyncio.to_thread(embed, query), timeout=_EMBED_TIMEOUT_S
        )
    except Exception as exc:  # the keyword half still answers
        logger.warning("notebook search: query embedding failed: %s", exc)
        return None


def _rrf(rank: int | None) -> float:
    return 0.0 if rank is None else 1.0 / (_RRF_K + rank)


async def _note_candidates(
    session: AsyncSession, workspace_id: int, query: str, tsquery: str | None
) -> list[Candidate]:
    document = (
        func.coalesce(NotebookNote.title, "")
        + literal(" ")
        + func.coalesce(NotebookNote.content, "")
    )
    vector = func.to_tsvector("english", document)
    needle = f"%{_escape_like(query.strip())}%"
    substring = or_(
        NotebookNote.title.ilike(needle, escape="\\"),
        NotebookNote.content.ilike(needle, escape="\\"),
    )
    if tsquery:
        ts = func.to_tsquery("english", tsquery)
        matches = or_(vector.op("@@")(ts), substring)
        rank = func.ts_rank_cd(vector, ts)
    else:
        matches, rank = substring, literal(0.0)
    rows = await session.execute(
        select(
            NotebookNote.id,
            NotebookNote.notebook_id,
            NotebookNote.title,
            NotebookNote.content,
        )
        .join(Notebook, Notebook.id == NotebookNote.notebook_id)
        .where(Notebook.workspace_id == workspace_id, matches)
        .order_by(rank.desc(), NotebookNote.updated_at.desc(), NotebookNote.id.desc())
        .limit(_NOTE_LIMIT)
    )
    return [
        Candidate(
            kind="note",
            item_id=note_id,
            title=title,
            content=f"{title}\n\n{content or ''}",
            score=_rrf(position),
            note_notebook_id=notebook_id,
        )
        for position, (note_id, notebook_id, title, content) in enumerate(
            rows.all(), start=1
        )
    ]


def _escape_like(value: str) -> str:
    return value.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")


async def _chunk_candidates(
    session: AsyncSession,
    document_ids: list[int],
    tsquery: str | None,
    embedding: Any | None,
) -> list[Candidate]:
    """Fused chunk hits, best first."""
    if not document_ids or (tsquery is None and embedding is None):
        return []
    in_scope = Chunk.document_id.in_(document_ids)
    semantic = keyword = None
    if embedding is not None:
        distance = Chunk.embedding.op("<=>", return_type=Float)(embedding)
        semantic = (
            select(
                Chunk.id.label("id"),
                func.rank().over(order_by=distance).label("rank"),
                (1 - distance).label("similarity"),
            )
            .where(in_scope, Chunk.embedding.is_not(None))
            .order_by(distance)
            .limit(_ARM_LIMIT)
            .cte("semantic")
        )
    if tsquery is not None:
        vector = func.to_tsvector("english", Chunk.content)
        ts = func.to_tsquery("english", tsquery)
        keyword = (
            select(
                Chunk.id.label("id"),
                func.rank()
                .over(order_by=func.ts_rank_cd(vector, ts).desc())
                .label("rank"),
            )
            .where(in_scope, vector.op("@@")(ts))
            .order_by(func.ts_rank_cd(vector, ts).desc())
            .limit(_ARM_LIMIT)
            .cte("keyword")
        )

    if semantic is not None and keyword is not None:
        joined = semantic.outerjoin(keyword, semantic.c.id == keyword.c.id, full=True)
        chunk_id = func.coalesce(semantic.c.id, keyword.c.id)
        sem_rank, kw_rank, similarity = (
            semantic.c.rank,
            keyword.c.rank,
            semantic.c.similarity,
        )
    elif semantic is not None:
        joined, chunk_id = semantic, semantic.c.id
        sem_rank, kw_rank, similarity = (
            semantic.c.rank,
            null(),
            semantic.c.similarity,
        )
    else:
        assert keyword is not None
        joined, chunk_id = keyword, keyword.c.id
        sem_rank, kw_rank, similarity = null(), keyword.c.rank, null()

    rows = await session.execute(
        select(
            Chunk.id,
            Chunk.document_id,
            Chunk.content,
            Document.title,
            Document.document_type,
            sem_rank.label("sem_rank"),
            kw_rank.label("kw_rank"),
            similarity.label("similarity"),
        )
        .select_from(joined)
        .join(Chunk, Chunk.id == chunk_id)
        .join(Document, Document.id == Chunk.document_id)
    )
    candidates: list[Candidate] = []
    for row in rows.all():
        kw = int(row.kw_rank) if row.kw_rank is not None else None
        sem = int(row.sem_rank) if row.sem_rank is not None else None
        close = row.similarity is not None and float(row.similarity) >= (
            MIN_SEMANTIC_SIMILARITY
        )
        if kw is None and not close:
            continue
        candidates.append(
            Candidate(
                kind="source",
                item_id=row.document_id,
                title=row.title,
                content=row.content,
                score=_rrf(kw) + (_rrf(sem) if close else 0.0),
                document_type=getattr(row.document_type, "value", row.document_type),
            )
        )
    candidates.sort(key=lambda c: -c.score)
    return candidates


def _rerank(
    reranker, query: str, terms: list[str], ranked: list[Candidate]
) -> list[Candidate] | None:
    """``ranked`` with its head reordered by the cross-encoder, or ``None``.

    Only the first ``_RERANK_POOL`` passages are scored (the model costs tens
    of milliseconds a passage on CPU); the rest follow in their hybrid order.
    A passage without a literal match is dropped unless the model scores it
    relevant.
    When the model finds nothing relevant at all (typically a half-typed word
    such as "asth"), its order means nothing and the hybrid order stands.
    """
    pool = ranked[:_RERANK_POOL]
    if len(pool) < 2:
        return None
    documents = [
        {
            "document_id": str(index),
            "content": f"{c.title}\n{c.content[:_RERANK_TEXT_CHARS]}",
            "score": c.score,
            "document": {"id": index, "title": c.title},
        }
        for index, c in enumerate(pool)
    ]
    reordered = reranker.rerank_documents(query, documents)
    if reordered is documents or len(reordered) != len(pool):
        return None
    scores = [float(doc.get("score", 0.0)) for doc in reordered]
    if max(scores, default=0.0) < MIN_RERANK_SCORE:
        return None
    result: list[Candidate] = []
    for doc, score in zip(reordered, scores, strict=True):
        original = pool[int(doc["document_id"])]
        if score < MIN_RERANK_SCORE and not has_literal_match(original.content, terms):
            continue
        result.append(replace(original, score=1.0 + score))
    # Unscored passages rank below every scored one, in their hybrid order;
    # without a score, only a literal match vouches for one.
    tail = [c for c in ranked[_RERANK_POOL:] if has_literal_match(c.content, terms)]
    return result + tail


async def search_notebooks(
    session: AsyncSession,
    workspace_id: int,
    query: str,
    *,
    include_documents: bool = True,
    max_notebooks: int = MAX_NOTEBOOKS,
    per_notebook: int = MAX_PER_NOTEBOOK,
    max_passages: int = MAX_PASSAGES,
    embed: Embedder | None = None,
    reranker: Any = "default",
) -> SearchOutcome:
    """Passages from the workspace's notebooks that match ``query``, grouped."""
    started = time.perf_counter()
    query = query.strip()[:MAX_QUERY_CHARS]
    terms = query_terms(query)
    tsquery = prefix_tsquery(terms)
    empty = SearchOutcome(query, terms, [], {}, 0, False, False)
    if len(query) < MIN_QUERY_CHARS:
        return empty

    embedding_task = None
    if include_documents:
        embedding_task = asyncio.create_task(
            _embed(embed or _default_embedder(), query)
        )
    try:
        links = (
            await notebook_document_links(session, workspace_id)
            if include_documents
            else {}
        )
        notes = await _note_candidates(session, workspace_id, query, tsquery)
    except BaseException:
        if embedding_task is not None:
            embedding_task.cancel()
        raise
    embedding = await embedding_task if embedding_task is not None else None
    t_retrieve = time.perf_counter()
    chunks = await _chunk_candidates(session, sorted(links), tsquery, embedding)
    t_chunks = time.perf_counter()

    ranked = sorted([*chunks, *notes], key=lambda c: -c.score)
    reranked = False
    if reranker == "default":
        reranker = _default_reranker()
    if reranker is not None and ranked:
        result = await asyncio.to_thread(_rerank, reranker, query, terms, ranked)
        if result is not None:
            ranked, reranked = result, True
    t_rerank = time.perf_counter()

    groups = group_passages(
        ranked,
        links,
        max_notebooks=max_notebooks,
        per_notebook=per_notebook,
        max_passages=max_passages,
    )
    notebooks: dict[int, Notebook] = {}
    if groups:
        notebooks = {
            nb.id: nb
            for nb in (
                await session.scalars(
                    select(Notebook).where(
                        Notebook.workspace_id == workspace_id,
                        Notebook.id.in_([g.notebook_id for g in groups]),
                    )
                )
            ).all()
        }
        groups = [g for g in groups if g.notebook_id in notebooks]
    took_ms = int((time.perf_counter() - started) * 1000)
    stats = {
        "scope_documents": len(links),
        "chunk_candidates": len(chunks),
        "note_candidates": len(notes),
        "retrieve_ms": int((t_retrieve - started) * 1000),
        "chunks_ms": int((t_chunks - t_retrieve) * 1000),
        "rerank_ms": int((t_rerank - t_chunks) * 1000),
    }
    logger.info("notebook search ws=%s took=%sms %s", workspace_id, took_ms, stats)
    return SearchOutcome(
        query=query,
        terms=terms,
        groups=groups,
        notebooks=notebooks,
        took_ms=took_ms,
        reranked=reranked,
        semantic=embedding is not None,
        stats=stats,
    )


def passage_payload(placed, terms: list[str]) -> dict[str, Any]:
    """The JSON-ready shape of one placed passage."""
    candidate = placed.candidate
    snippet, highlights = make_snippet(
        candidate.content.split("\n\n", 1)[1]
        if candidate.kind == "note" and "\n\n" in candidate.content
        else candidate.content,
        terms,
    )
    if not snippet and candidate.kind == "note":
        snippet, highlights = make_snippet(candidate.title, terms)
    link = placed.link
    return {
        "kind": candidate.kind,
        "title": candidate.title,
        "snippet": snippet,
        "highlights": [list(r) for r in highlights],
        "document_id": candidate.item_id if candidate.kind == "source" else None,
        "note_id": candidate.item_id if candidate.kind == "note" else None,
        "source_id": link.source_id if link else None,
        "folder_name": link.folder_name if link else None,
        "document_type": candidate.document_type,
        "score": round(candidate.score, 4),
    }
