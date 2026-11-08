"""What the public notebook page and its viewers are sent.

Built from the notebook as it is at request time. Ids a visitor sees are the
ones the public routes take back (a source's document id, an output's artifact
id, a file's id), each re-checked against this notebook on the way in; account
ids, emails, avatars, storage keys, paths, prompts and chat settings are never
part of any payload here.
"""

from __future__ import annotations

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import selectinload

from app.artifacts.persistence import Artifact, ArtifactFile, ArtifactFileRole
from app.db import Document
from app.file_storage.persistence.enums import DocumentFileKind
from app.file_storage.persistence.models import DocumentFile
from app.notebooks.persistence import Notebook
from app.routes.document_files_routes import _canonical_original_mime

from .chat import chat_artifact_ids, public_chat
from .scope import (
    LiveLink,
    finished_outputs,
    gone,
    shared_thread,
    source_document,
    source_documents,
)

API = "/api/v1/public-notebooks"


def artifact_file_url(token: str, artifact_id: int, file_id: int) -> str:
    return f"{API}/{token}/artifacts/{artifact_id}/files/{file_id}/content"


def source_file_url(token: str, document_id: int, file_id: int) -> str:
    return f"{API}/{token}/sources/{document_id}/files/{file_id}/content"


async def _originals(
    session: AsyncSession, documents: list[Document]
) -> dict[int, DocumentFile]:
    if not documents:
        return {}
    rows = (
        await session.scalars(
            select(DocumentFile)
            .where(
                DocumentFile.document_id.in_([d.id for d in documents]),
                DocumentFile.kind == DocumentFileKind.ORIGINAL,
            )
            .order_by(DocumentFile.id)
        )
    ).all()
    originals: dict[int, DocumentFile] = {}
    for row in rows:
        originals.setdefault(row.document_id, row)
    return originals


def _document_type(document: Document) -> str:
    value = document.document_type
    return value.value if hasattr(value, "value") else str(value)


async def page(session: AsyncSession, live: LiveLink) -> dict:
    notebook = live.notebook
    documents = await source_documents(session, notebook)
    originals = await _originals(session, documents)
    titles = {document.id: document.title for document in documents}
    thread = await shared_thread(session, live.link, notebook)
    messages, citations, _ = await public_chat(
        session, thread, notebook.workspace_id, titles
    )
    outputs = await finished_outputs(session, notebook)
    return {
        "notebook": {"title": notebook.title, "icon": notebook.icon},
        "expires_at": live.link.expires_at.isoformat()
        if live.link.expires_at
        else None,
        "messages": messages,
        "citations": citations,
        "outputs": [
            {
                "id": output.id,
                "kind": output.kind,
                "title": output.title,
                "format": output.artifact_format,
                "artifact_id": output.artifact_id,
                "duration_seconds": output.duration_seconds,
                "created_at": output.created_at.isoformat(),
            }
            for output in outputs
        ],
        "sources": [
            {
                "id": document.id,
                "title": document.title,
                "document_type": _document_type(document),
                "has_file": document.id in originals,
            }
            for document in documents
        ],
    }


async def shared_artifact(
    session: AsyncSession, live: LiveLink, artifact_id: int
) -> tuple[Artifact, Document]:
    """An artifact this link shows: a finished output, or something its chat made."""
    notebook: Notebook = live.notebook
    allowed = {
        output.artifact_id for output in await finished_outputs(session, notebook)
    }
    if artifact_id not in allowed:
        thread = await shared_thread(session, live.link, notebook)
        allowed = await chat_artifact_ids(session, thread, notebook.workspace_id)
    if artifact_id not in allowed:
        raise gone()
    row = (
        await session.execute(
            select(Artifact, Document)
            .join(Document, Document.id == Artifact.document_id)
            .options(selectinload(Artifact.files))
            .where(
                Artifact.id == artifact_id,
                Artifact.workspace_id == notebook.workspace_id,
            )
        )
    ).first()
    if row is None:
        raise gone()
    return row[0], row[1]


def artifact_manifest(token: str, artifact: Artifact, document: Document) -> dict:
    """The in-app manifest's shape, so the in-app viewers render it, with
    this link's file URLs and no study state (a visitor's answers stay in
    their browser)."""
    files = sorted(
        artifact.files,
        key=lambda item: (item.role is not ArtifactFileRole.PRIMARY, item.id),
    )
    return {
        "artifact_id": artifact.id,
        "document_id": 0,
        "title": document.title,
        "format": artifact.format,
        "generation": artifact.generation,
        "markdown_representation": document.source_markdown or document.content or "",
        "files": [
            {
                "file_id": file.id,
                "role": file.role.value,
                "filename": file.original_filename,
                "mime_type": file.mime_type or "application/octet-stream",
                "size_bytes": file.size_bytes,
                "content_url": artifact_file_url(token, artifact.id, file.id),
            }
            for file in files
        ],
        "updated_at": artifact.updated_at.isoformat() if artifact.updated_at else None,
    }


def artifact_file(artifact: Artifact, file_id: int) -> ArtifactFile:
    for file in artifact.files:
        if file.id == file_id:
            return file
    raise gone()


async def source_view(session: AsyncSession, live: LiveLink, document_id: int) -> dict:
    """How a source opens: its original file when a viewer can show it, else
    its text (with the original, if any, to download)."""
    document = await source_document(session, live.notebook, document_id)
    original = (await _originals(session, [document])).get(document.id)
    token = live.link.share_token
    file = None
    presentation = "text"
    if original is not None:
        mime = _canonical_original_mime(original.original_filename, original.mime_type)
        file = {
            "file_id": original.id,
            "filename": original.original_filename,
            "mime_type": mime or original.mime_type or "application/octet-stream",
            "size_bytes": original.size_bytes,
            "content_url": source_file_url(token, document.id, original.id),
        }
        if mime is not None:
            presentation = "original"
    return {
        "id": document.id,
        "title": document.title,
        "document_type": _document_type(document),
        "presentation": presentation,
        "file": file,
        "markdown": ""
        if presentation == "original"
        else (document.source_markdown or document.content or ""),
    }


async def source_file(
    session: AsyncSession, live: LiveLink, document_id: int, file_id: int
) -> DocumentFile:
    """A source's original file; derived copies (redacted, filled) stay private."""
    document = await source_document(session, live.notebook, document_id)
    record = await session.scalar(
        select(DocumentFile).where(
            DocumentFile.id == file_id,
            DocumentFile.document_id == document.id,
            DocumentFile.workspace_id == live.notebook.workspace_id,
            DocumentFile.kind == DocumentFileKind.ORIGINAL,
        )
    )
    if record is None:
        raise gone()
    return record
