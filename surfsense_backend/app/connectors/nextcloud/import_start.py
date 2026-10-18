"""Start an import: one pending document per planned file, linked where it was asked for.

Rows appear at once so the notebook (or library) shows them while the files
download and parse in the background. A file imported before is recognised by
its account and path: unchanged (same etag) and ready, it is simply linked
again; changed or previously failed, the same document is queued to re-read it.
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass, field
from datetime import UTC, datetime
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document, DocumentStatus
from app.etl_pipeline.etl_document import DocumentParser
from app.services.folder_service import ensure_folder_hierarchy

from .client import NextcloudAccount
from .document_identity import (
    DOCUMENT_TYPE,
    METADATA_KEY,
    identity_hash,
    nextcloud_facts,
)
from .import_selection import ImportPlan, PlannedFile
from .notebook_links import link_document, link_folder


@dataclass
class ImportStarted:
    #: Every document the pick maps to, new or reused, in plan order.
    document_ids: list[int] = field(default_factory=list)
    #: Documents a background job must now download and parse.
    queued_ids: list[int] = field(default_factory=list)
    #: Picked files already imported and unchanged.
    reused: int = 0
    #: The SurfSense folder each picked Nextcloud folder became.
    folder_ids: list[int] = field(default_factory=list)


@dataclass(frozen=True)
class ImportOptions:
    connector_id: int
    workspace_id: int
    user_id: UUID
    parser: DocumentParser
    use_vision_llm: bool
    notebook_id: int | None = None


def _metadata(
    planned: PlannedFile, account: NextcloudAccount, options: ImportOptions
) -> dict:
    entry = planned.entry
    return {
        "FILE_NAME": entry.name,
        "file_size": entry.size,
        "source": "nextcloud",
        METADATA_KEY: {
            "connector_id": options.connector_id,
            "server_url": account.base_url,
            "username": account.username,
            "path": entry.path,
            "etag": entry.etag,
            "modified": entry.modified.isoformat() if entry.modified else None,
            "content_type": entry.content_type,
            "size": entry.size,
            "parser": options.parser.value,
            "use_vision_llm": options.use_vision_llm,
            "notebook_id": options.notebook_id,
            "imported_at": datetime.now(UTC).isoformat(),
        },
    }


def _unchanged(document: Document, planned: PlannedFile) -> bool:
    etag = nextcloud_facts(document.document_metadata).get("etag")
    return bool(etag) and etag == planned.entry.etag


def _in_flight(document: Document) -> bool:
    state = DocumentStatus.get_state(document.status)
    return state in (DocumentStatus.PENDING, DocumentStatus.PROCESSING)


async def _folder_for(
    session: AsyncSession,
    planned: PlannedFile,
    options: ImportOptions,
    cache: dict[tuple[str, ...], int | None],
) -> int | None:
    parts = planned.folder_parts
    if parts not in cache:
        cache[parts] = await ensure_folder_hierarchy(
            session,
            workspace_id=options.workspace_id,
            created_by_id=str(options.user_id),
            folder_parts=list(parts),
        )
    return cache[parts]


def _pending_document(
    planned: PlannedFile,
    account: NextcloudAccount,
    options: ImportOptions,
    uid_hash: str,
    folder_id: int | None,
) -> Document:
    return Document(
        workspace_id=options.workspace_id,
        title=planned.entry.name,
        document_type=DOCUMENT_TYPE,
        content="Pending...",
        content_hash=hashlib.sha256(f"placeholder:{uid_hash}".encode()).hexdigest(),
        unique_identifier_hash=uid_hash,
        document_metadata=_metadata(planned, account, options),
        status=DocumentStatus.pending(),
        embedding=None,
        folder_id=folder_id,
        updated_at=datetime.now(UTC),
        created_by_id=options.user_id,
    )


async def _existing(session: AsyncSession, uid_hash: str) -> Document | None:
    return await session.scalar(
        select(Document).where(Document.unique_identifier_hash == uid_hash)
    )


async def _document_for(
    session: AsyncSession,
    planned: PlannedFile,
    account: NextcloudAccount,
    options: ImportOptions,
    folder_id: int | None,
    started: ImportStarted,
) -> Document:
    uid_hash = identity_hash(account, planned.entry.path, options.workspace_id)
    document = await _existing(session, uid_hash)
    if document is None:
        candidate = _pending_document(planned, account, options, uid_hash, folder_id)
        try:
            async with session.begin_nested():
                session.add(candidate)
                await session.flush()
        except IntegrityError:
            # A concurrent import of the same file won; use its row.
            document = await _existing(session, uid_hash)
            if document is None:
                raise
        else:
            started.queued_ids.append(candidate.id)
            return candidate

    if folder_id is not None:
        document.folder_id = folder_id
    ready = DocumentStatus.is_state(document.status, DocumentStatus.READY)
    if (ready and _unchanged(document, planned)) or _in_flight(document):
        started.reused += 1
        return document
    document.document_metadata = {
        **(document.document_metadata or {}),
        **_metadata(planned, account, options),
    }
    document.status = DocumentStatus.pending()
    document.updated_at = datetime.now(UTC)
    started.queued_ids.append(document.id)
    return document


async def start_import(
    session: AsyncSession,
    plan: ImportPlan,
    account: NextcloudAccount,
    options: ImportOptions,
) -> ImportStarted:
    """Create or reuse the documents and link them; the caller commits and dispatches."""
    started = ImportStarted()
    folders: dict[tuple[str, ...], int | None] = {}

    for name_chain in plan.picked_folders.values():
        folder_id = await ensure_folder_hierarchy(
            session,
            workspace_id=options.workspace_id,
            created_by_id=str(options.user_id),
            folder_parts=list(name_chain),
        )
        folders[name_chain] = folder_id
        if folder_id is not None:
            started.folder_ids.append(folder_id)
            if options.notebook_id is not None:
                await link_folder(session, options.notebook_id, folder_id)

    for planned in plan.files:
        folder_id = await _folder_for(session, planned, options, folders)
        document = await _document_for(
            session, planned, account, options, folder_id, started
        )
        started.document_ids.append(document.id)
        # Files from a picked folder reach the notebook through the folder link.
        if options.notebook_id is not None and planned.picked_folder is None:
            await link_document(session, options.notebook_id, document.id)

    await session.flush()
    return started
