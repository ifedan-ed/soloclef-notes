"""The shared notebook's chat as a visitor sees it, read at request time.

A visitor gets the words of each turn and the things the chat made, nothing
else: no tool calls, searches, reasoning, activity traces or authors. A
citation to one of the notebook's sources becomes ``[citation:N]``, numbered in
order of appearance, with N's source title and page in ``citations``; the
chunk id behind it, and any citation to something outside the notebook's
sources, never leaves. What the chat saved or generated (an artifact, a
podcast, a chat-started video) becomes one ``notebook_artifact`` part, and only
if it is finished and in the notebook's workspace.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.artifacts.persistence import Artifact
from app.db import (
    Chunk,
    DeliverableJob,
    DeliverableJobStatus,
    Document,
    NewChatMessage,
    NewChatThread,
)
from app.podcasts.persistence import Podcast, PodcastStatus

ARTIFACT_TOOL = "notebook_artifact"

_DROPPED = "\x00"
_DROPPED_WITH_SPACE = re.compile(r"[ \t]*\x00")
_CITATION = re.compile(r"[\[\u3010]\u200b?citation:(doc-)?(\d+)\u200b?[\]\u3011]")


@dataclass
class _Citations:
    """Chunk ids to the public numbers they are shown as."""

    in_scope: dict[int, tuple[int, int | None]]  # chunk -> (document, page)
    titles: dict[int, str]
    numbers: dict[int, int] = field(default_factory=dict)
    shown: dict[str, dict[str, Any]] = field(default_factory=dict)

    def rewrite(self, text: str) -> str:
        def replace(match: re.Match[str]) -> str:
            if match.group(1):  # retired product-docs citations
                return _DROPPED
            chunk_id = int(match.group(2))
            place = self.in_scope.get(chunk_id)
            if place is None:
                return _DROPPED
            number = self.numbers.get(chunk_id)
            if number is None:
                number = len(self.numbers) + 1
                self.numbers[chunk_id] = number
                document_id, page = place
                self.shown[str(number)] = {
                    "source_id": document_id,
                    "title": self.titles.get(document_id, ""),
                    "page": page,
                }
            return f"[citation:{number}]"

        # A dropped marker takes the space before it along ("see [x]." -> "see.").
        return _DROPPED_WITH_SPACE.sub("", _CITATION.sub(replace, text))


def _parts(content: Any) -> list[dict]:
    if isinstance(content, str):
        return [{"type": "text", "text": content}]
    if not isinstance(content, list):
        return []
    return [part for part in content if isinstance(part, dict)]


def _texts(messages: list[NewChatMessage]) -> list[str]:
    return [
        part.get("text") or ""
        for message in messages
        for part in _parts(message.content)
        if part.get("type") == "text"
    ]


async def _citations(
    session: AsyncSession, texts: list[str], sources: dict[int, str]
) -> _Citations:
    chunk_ids = {
        int(match.group(2))
        for text in texts
        for match in _CITATION.finditer(text)
        if not match.group(1)
    }
    in_scope: dict[int, tuple[int, int | None]] = {}
    if chunk_ids and sources:
        rows = await session.execute(
            select(Chunk.id, Chunk.document_id, Chunk.page_start).where(
                Chunk.id.in_(chunk_ids), Chunk.document_id.in_(list(sources))
            )
        )
        in_scope = {chunk_id: (doc_id, page) for chunk_id, doc_id, page in rows.all()}
    return _Citations(in_scope=in_scope, titles=sources)


def _made_by(part: dict) -> tuple[str, int] | None:
    """What a finished tool call made: ("artifact"|"podcast"|"job", id)."""
    if part.get("type") != "tool-call":
        return None
    result = part.get("result")
    if not isinstance(result, dict):
        return None
    artifact_id = result.get("artifact_id")
    if isinstance(artifact_id, int) and not isinstance(artifact_id, bool):
        if part.get("toolName") == "save_artifact" and result.get("status") != "saved":
            return None
        return ("artifact", artifact_id)
    podcast_id = result.get("podcast_id")
    if part.get("toolName") == "generate_podcast" and isinstance(podcast_id, int):
        return ("podcast", podcast_id)
    job_id = result.get("job_id")
    if part.get("toolName") == "enqueue_deliverable_job" and isinstance(job_id, int):
        return ("job", job_id)
    return None


async def _made_artifacts(
    session: AsyncSession, workspace_id: int, made: set[tuple[str, int]]
) -> tuple[dict[tuple[str, int], int], dict[int, tuple[str, str]]]:
    """Each reference that resolves to a finished artifact in the workspace,
    and each such artifact's (title, format)."""
    resolved: dict[tuple[str, int], int] = {}
    for kind, ref in made:
        if kind == "artifact":
            resolved[(kind, ref)] = ref
    podcast_ids = [ref for kind, ref in made if kind == "podcast"]
    if podcast_ids:
        for podcast_id, artifact_id in (
            await session.execute(
                select(Podcast.id, Podcast.artifact_id).where(
                    Podcast.id.in_(podcast_ids),
                    Podcast.workspace_id == workspace_id,
                    Podcast.status == PodcastStatus.READY,
                    Podcast.artifact_id.is_not(None),
                )
            )
        ).all():
            resolved[("podcast", podcast_id)] = artifact_id
    job_ids = [ref for kind, ref in made if kind == "job"]
    if job_ids:
        for job_id, artifact_id in (
            await session.execute(
                select(DeliverableJob.id, DeliverableJob.artifact_id).where(
                    DeliverableJob.id.in_(job_ids),
                    DeliverableJob.workspace_id == workspace_id,
                    DeliverableJob.status == DeliverableJobStatus.READY,
                    DeliverableJob.artifact_id.is_not(None),
                )
            )
        ).all():
            resolved[("job", job_id)] = artifact_id
    # Only artifacts that exist, in this workspace, and are not being deleted.
    candidates = set(resolved.values())
    known: dict[int, tuple[str, str]] = {}
    if candidates:
        for artifact_id, title, artifact_format, status in (
            await session.execute(
                select(Artifact.id, Document.title, Artifact.format, Document.status)
                .join(Document, Document.id == Artifact.document_id)
                .where(
                    Artifact.id.in_(candidates),
                    Artifact.workspace_id == workspace_id,
                )
            )
        ).all():
            state = status.get("state") if isinstance(status, dict) else None
            if state != "deleting":
                known[artifact_id] = (title, artifact_format)
    return {key: value for key, value in resolved.items() if value in known}, known


async def _messages(
    session: AsyncSession, thread: NewChatThread
) -> list[NewChatMessage]:
    return list(
        (
            await session.scalars(
                select(NewChatMessage)
                .where(NewChatMessage.thread_id == thread.id)
                .order_by(NewChatMessage.created_at, NewChatMessage.id)
            )
        ).all()
    )


async def chat_artifact_ids(
    session: AsyncSession, thread: NewChatThread | None, workspace_id: int
) -> set[int]:
    """The artifacts the shared chat made, for the file routes' allowlist."""
    if thread is None:
        return set()
    made = {
        ref
        for message in await _messages(session, thread)
        for part in _parts(message.content)
        if (ref := _made_by(part)) is not None
    }
    resolved, _ = await _made_artifacts(session, workspace_id, made)
    return set(resolved.values())


async def public_chat(
    session: AsyncSession,
    thread: NewChatThread | None,
    workspace_id: int,
    sources: dict[int, str],
) -> tuple[list[dict], dict[str, dict], set[int]]:
    """(messages, citations, artifact ids) for the shared chat."""
    if thread is None:
        return [], {}, set()
    messages = await _messages(session, thread)
    citations = await _citations(session, _texts(messages), sources)
    made = {
        ref
        for message in messages
        for part in _parts(message.content)
        if (ref := _made_by(part)) is not None
    }
    artifacts, titles = await _made_artifacts(session, workspace_id, made)

    shown: list[dict] = []
    for message in messages:
        role = getattr(message.role, "value", message.role)
        if role not in ("user", "assistant"):
            continue
        content: list[dict] = []
        for part in _parts(message.content):
            if part.get("type") == "text":
                text = citations.rewrite(part.get("text") or "").strip()
                if text:
                    content.append({"type": "text", "text": text})
                continue
            ref = _made_by(part)
            artifact_id = artifacts.get(ref) if ref else None
            if artifact_id is None:
                continue
            title, artifact_format = titles[artifact_id]
            content.append(
                {
                    "type": "tool-call",
                    "toolName": ARTIFACT_TOOL,
                    "toolCallId": f"artifact-{len(shown)}-{len(content)}",
                    "args": {},
                    "result": {
                        "artifact_id": artifact_id,
                        "title": title,
                        "format": artifact_format,
                    },
                }
            )
        if content:
            shown.append(
                {
                    "role": role,
                    "content": content,
                    "created_at": message.created_at.isoformat()
                    if message.created_at
                    else None,
                }
            )
    return shown, citations.shown, set(artifacts.values())
