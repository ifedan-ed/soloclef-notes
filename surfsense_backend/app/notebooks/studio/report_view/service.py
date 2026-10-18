"""The report reading view: suggestions per section, Add, dismiss.

Suggestions are made once per report version by the quick-tasks model and
kept in the report artifact's metadata (see ``store``). Adding one starts an
ordinary Studio generation (``start.start_generation``) focused on that
section and built from the report's own sources, so it also shows in the
Studio list; the suggestion keeps the new output's id so the viewer can show
its progress in place and embed it once it is ready.
"""

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Literal
from uuid import UUID

from pydantic import BaseModel, TypeAdapter
from sqlalchemy import or_, select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import selectinload
from sqlalchemy.orm.attributes import flag_modified

from app.artifacts.persistence import Artifact, ArtifactFileRole
from app.db import Document
from app.quick_tasks.language import (
    LANGUAGE_NAMES,
    detect_language,
    language_code,
    language_name,
)

from ...persistence import Notebook, NotebookOutput, NotebookSource
from .. import start
from ..listing import StudioOutputRead, list_outputs
from ..options import StudioRequest
from ..source_material import SourceDocument, resolve_source_documents
from . import store
from .generate import generate_suggestions
from .schemas import SUGGESTION_KINDS, ReportSuggestion
from .sections import ReportOutline, split_report

logger = logging.getLogger(__name__)

ReportFormat = Literal["document", "interactive"]
_FOCUS_EXCERPT_CHARS = 900
_MAX_FOCUS_CHARS = 2000
_parse_request = TypeAdapter(StudioRequest).validate_python
_LANGUAGE_BY_NAME = {name.casefold(): code for code, name in LANGUAGE_NAMES.items()}


class ReportNotFoundError(LookupError):
    """No ready report with that id in this notebook."""


class SuggestionRead(BaseModel):
    id: str
    section_index: int
    section_heading: str
    kind: str
    title: str
    focus: str
    state: Literal["open", "added", "dismissed"]
    output_id: int | None = None


class SourceRef(BaseModel):
    id: int
    title: str
    document_type: str | None = None


class ReportViewRead(BaseModel):
    output_id: int
    artifact_id: int
    generation: int
    format: ReportFormat
    language: str
    # "ready" once suggestions were made (possibly none); "unavailable" when
    # they could not be (no quick model, a failed call, an interactive report).
    suggestions_status: Literal["ready", "unavailable"]
    suggestions: list[SuggestionRead]
    sources: list[SourceRef]


@dataclass
class LoadedReport:
    output: NotebookOutput
    listed: StudioOutputRead
    artifact: Artifact
    outline: ReportOutline
    format: ReportFormat
    language: str


def report_language(options: dict | None, markdown: str) -> str:
    """The report's language code: the one it was asked in, else detected."""
    asked = str((options or {}).get("language") or "").strip()
    if asked:
        code = _LANGUAGE_BY_NAME.get(asked.casefold()) or language_code(asked)
        if code:
            return code
    return detect_language(markdown) or "en"


def _report_format(output: NotebookOutput, artifact: Artifact) -> ReportFormat:
    if (output.options or {}).get("format") == "interactive":
        return "interactive"
    primary = next(
        (f for f in artifact.files if f.role is ArtifactFileRole.PRIMARY), None
    )
    if primary is not None and (primary.mime_type or "").startswith("text/html"):
        return "interactive"
    return "document"


async def load_report(
    session: AsyncSession, notebook: Notebook, output_id: int
) -> LoadedReport:
    output = await session.scalar(
        select(NotebookOutput).where(
            NotebookOutput.id == output_id,
            NotebookOutput.notebook_id == notebook.id,
            NotebookOutput.workspace_id == notebook.workspace_id,
        )
    )
    if output is None or output.kind != "report":
        raise ReportNotFoundError
    listed = (await list_outputs(session, notebook, output_id=output.id))[0]
    if listed.status != "ready" or not listed.artifact_id:
        raise ReportNotFoundError
    artifact = await session.scalar(
        select(Artifact)
        .options(selectinload(Artifact.files), selectinload(Artifact.document))
        .where(
            Artifact.id == listed.artifact_id,
            Artifact.workspace_id == notebook.workspace_id,
        )
    )
    if artifact is None:
        raise ReportNotFoundError
    markdown = artifact.document.source_markdown or artifact.document.content or ""
    return LoadedReport(
        output=output,
        listed=listed,
        artifact=artifact,
        outline=split_report(markdown),
        format=_report_format(output, artifact),
        language=report_language(output.options, markdown),
    )


async def _save_entry(session: AsyncSession, artifact_id: int, build) -> dict:
    """Write the report view's entry under a row lock; ``build(current)``.

    The artifact's ``updated_at`` means "content changed", so it is kept.
    """
    artifact = await session.scalar(
        select(Artifact)
        .where(Artifact.id == artifact_id)
        .with_for_update()
        .execution_options(populate_existing=True)
    )
    current = store.entry(artifact.artifact_metadata, artifact.generation)
    value = build(current or store.empty_entry(artifact.generation))
    content_updated_at = artifact.updated_at
    artifact.artifact_metadata = store.with_entry(artifact.artifact_metadata, value)
    artifact.updated_at = content_updated_at
    flag_modified(artifact, "artifact_metadata")
    flag_modified(artifact, "updated_at")
    await session.commit()
    return value


async def _sources(session: AsyncSession, notebook: Notebook, ids) -> list[SourceRef]:
    ids = [i for i in (ids or []) if isinstance(i, int)]
    if not ids:
        return []
    rows = (
        await session.execute(
            select(Document.id, Document.title, Document.document_type)
            .where(Document.id.in_(ids), Document.workspace_id == notebook.workspace_id)
            .order_by(Document.title, Document.id)
        )
    ).all()
    return [
        SourceRef(
            id=row.id,
            title=row.title or "",
            document_type=getattr(row.document_type, "value", row.document_type),
        )
        for row in rows
    ]


async def _existing_outputs(
    session: AsyncSession, notebook: Notebook, ids: list[int]
) -> set[int]:
    if not ids:
        return set()
    return set(
        (
            await session.scalars(
                select(NotebookOutput.id).where(
                    NotebookOutput.id.in_(ids),
                    NotebookOutput.notebook_id == notebook.id,
                )
            )
        ).all()
    )


async def _read(
    session: AsyncSession,
    notebook: Notebook,
    report: LoadedReport,
    value: dict | None,
    status: Literal["ready", "unavailable"],
) -> ReportViewRead:
    items = (value or {}).get("suggestions") or []
    linked = [i["output_id"] for i in items if isinstance(i.get("output_id"), int)]
    alive = await _existing_outputs(session, notebook, linked)
    suggestions = []
    for item in items:
        state = item.get("state") if item.get("state") in store.STATES else "open"
        output_id = item.get("output_id")
        if state == "added" and output_id not in alive:
            # The output was deleted from the Studio; offer the idea again.
            state, output_id = "open", None
        suggestions.append(
            SuggestionRead(
                id=item["id"],
                section_index=item["section"],
                section_heading=item.get("heading") or "",
                kind=item["kind"],
                title=item["title"],
                focus=item.get("focus") or "",
                state=state,
                output_id=output_id if state == "added" else None,
            )
        )
    return ReportViewRead(
        output_id=report.output.id,
        artifact_id=report.artifact.id,
        generation=report.artifact.generation,
        format=report.format,
        language=report.language,
        suggestions_status=status,
        suggestions=suggestions,
        sources=await _sources(session, notebook, report.output.source_document_ids),
    )


Generate = Callable[..., Awaitable[list[ReportSuggestion]]]


async def report_view(
    session: AsyncSession,
    notebook: Notebook,
    report: LoadedReport,
    *,
    kinds: list[str],
    generate: Generate | None = None,
) -> ReportViewRead:
    """The view, making the suggestions first if this version has none yet."""
    artifact = report.artifact
    value = store.entry(artifact.artifact_metadata, artifact.generation)
    if value is not None and value.get("ready"):
        return await _read(session, notebook, report, value, "ready")
    if report.format != "document" or not report.outline.sections:
        return await _read(session, notebook, report, value, "unavailable")
    allowed = [kind for kind in SUGGESTION_KINDS if kind in kinds]
    try:
        made = await (generate or generate_suggestions)(
            session,
            notebook.workspace_id,
            report.outline,
            language=report.language,
            kinds=allowed,
        )
    except Exception:
        logger.warning(
            "Report suggestions failed for output %s", report.output.id, exc_info=True
        )
        return await _read(session, notebook, report, value, "unavailable")

    def build(current: dict) -> dict:
        if current.get("ready"):
            return current  # another request stored them first
        return store.suggestions_entry(
            artifact.generation,
            report.language,
            report.outline,
            made,
            # A rating given before ratings were removed stays as it was.
            feedback=current.get("feedback"),
        )

    saved = await _save_entry(session, artifact.id, build)
    return await _read(session, notebook, report, saved, "ready")


async def report_documents(
    session: AsyncSession, notebook: Notebook, document_ids
) -> list[SourceDocument]:
    """The report's sources still in this notebook, else its enabled sources."""
    ids = [i for i in (document_ids or []) if isinstance(i, int)]
    rows = (
        await session.scalars(
            select(NotebookSource).where(NotebookSource.notebook_id == notebook.id)
        )
    ).all()
    direct = [r.document_id for r in rows if r.document_id is not None]
    folders = [r.folder_id for r in rows if r.folder_id is not None]
    documents = []
    if ids and (direct or folders):
        documents = (
            await session.scalars(
                select(Document)
                .where(
                    Document.id.in_(ids),
                    Document.workspace_id == notebook.workspace_id,
                    or_(Document.id.in_(direct), Document.folder_id.in_(folders)),
                )
                .order_by(Document.id)
            )
        ).all()
    if not documents:
        return await resolve_source_documents(session, notebook, None)
    return [
        SourceDocument(
            id=document.id,
            title=document.title,
            text=(document.source_markdown or document.content or "").strip(),
        )
        for document in documents
    ]


def suggestion_focus(report: LoadedReport, item: dict) -> str:
    section = next(
        (s for s in report.outline.sections if s.index == item["section"]), None
    )
    heading = item.get("heading") or (section.heading if section else "")
    report_title = report.outline.title or report.output.title
    parts = [
        f"{item['title']}: {item.get('focus') or ''}".strip(" :"),
        f'Base it on the section "{heading}" of the report "{report_title}" '
        "and cover that section's content in depth.",
    ]
    if section is not None:
        body = section.text.split("\n", 1)[1].strip() if "\n" in section.text else ""
        if body:
            excerpt = body[:_FOCUS_EXCERPT_CHARS].rstrip()
            parts.append(f"The section reads:\n{excerpt}")
    return "\n\n".join(parts)[:_MAX_FOCUS_CHARS]


def suggestion_request(report: LoadedReport, item: dict, *, attempt: int):
    """The Studio request an added suggestion starts."""
    kind = item["kind"]
    if kind not in SUGGESTION_KINDS:
        raise ValueError(f"not a suggestible output: {kind}")
    language = report.language if kind == "audio" else language_name(report.language)
    return _parse_request(
        {
            "kind": kind,
            "title": item["title"][:200],
            "focus": suggestion_focus(report, item),
            "language": language[:40],
            # Stable per attempt, so a double click starts one generation.
            "request_id": f"report{report.output.id}{item['id']}a{attempt}",
        }
    )


class SuggestionNotFoundError(LookupError):
    pass


async def add_suggestion(
    session: AsyncSession,
    notebook: Notebook,
    report: LoadedReport,
    suggestion_id: str,
    *,
    user_id: UUID,
    dispatchers: start.Dispatchers | None = None,
    authorize_request: Callable[[object], Awaitable[None]] | None = None,
) -> ReportViewRead:
    artifact = report.artifact
    value = store.entry(artifact.artifact_metadata, artifact.generation)
    item = store.find(value, suggestion_id) if value else None
    if item is None:
        raise SuggestionNotFoundError
    output_id = item.get("output_id")
    if isinstance(output_id, int):
        existing = await list_outputs(session, notebook, output_id=output_id)
        if existing and existing[0].status not in ("failed", "cancelled"):
            # Already started (another tab, a double click): nothing new.
            if item.get("state") != "added":
                value = await _save_entry(
                    session,
                    artifact.id,
                    lambda current: store.changed(
                        current, suggestion_id, state="added"
                    ),
                )
            return await _read(session, notebook, report, value, "ready")
    attempts = int(item.get("attempts") or 0)
    request = suggestion_request(report, item, attempt=attempts)
    if authorize_request is not None:
        await authorize_request(request)
    documents = await report_documents(
        session, notebook, report.output.source_document_ids
    )
    output = await start.start_generation(
        session,
        notebook=notebook,
        request=request,
        title=request.title or item["title"],
        documents=documents,
        user_id=user_id,
        dispatchers=dispatchers,
    )
    new_id = output.id
    saved = await _save_entry(
        session,
        artifact.id,
        lambda current: store.changed(
            current,
            suggestion_id,
            state="added",
            output_id=new_id,
            attempts=attempts + 1,
        ),
    )
    return await _read(session, notebook, report, saved, "ready")


async def dismiss_suggestion(
    session: AsyncSession, notebook: Notebook, report: LoadedReport, suggestion_id: str
) -> ReportViewRead:
    artifact = report.artifact
    value = store.entry(artifact.artifact_metadata, artifact.generation)
    if value is None or store.find(value, suggestion_id) is None:
        raise SuggestionNotFoundError
    saved = await _save_entry(
        session,
        artifact.id,
        lambda current: store.changed(current, suggestion_id, state="dismissed"),
    )
    return await _read(session, notebook, report, saved, "ready")
