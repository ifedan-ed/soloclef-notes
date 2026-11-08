"""Fill one pending Nextcloud document: download, parse with the chosen parser, index.

The parse runs through the upload pipeline's extractor, so the Local/Cloud
choice, page credits and the vision option behave exactly as an upload would.
A file whose text the workspace already holds (the same PDF uploaded earlier,
say) is not stored twice: the existing document is linked to the notebook the
import came from and the placeholder is removed.
"""

from __future__ import annotations

import contextlib
import logging
import os
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document, DocumentStatus, SearchSourceConnector
from app.indexing_pipeline.connector_document import ConnectorDocument
from app.indexing_pipeline.document_hashing import compute_content_hash
from app.indexing_pipeline.indexing_pipeline_service import IndexingPipelineService

from .client import NextcloudAccount, NextcloudClient, NextcloudError
from .connector_credentials import InvalidNextcloudSettingsError, account_from_config
from .document_identity import DOCUMENT_TYPE, nextcloud_facts, source_id
from .notebook_links import link_document

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class Extracted:
    markdown: str
    etl_service: str
    billable_pages: int


# (session, file path, filename, workspace id, user id, parser, vision) -> text.
Extractor = Callable[
    [AsyncSession, str, str, int, str, str, bool], Awaitable[Extracted]
]
ClientFactory = Callable[[NextcloudAccount], NextcloudClient]
# (session, document, bytes, filename, mime type) -> None; best effort.
OriginalStore = Callable[
    [AsyncSession, Document, bytes, str, str | None], Awaitable[None]
]
Charger = Callable[[AsyncSession, str, int], Awaitable[None]]


async def extract_like_an_upload(
    session: AsyncSession,
    file_path: str,
    filename: str,
    workspace_id: int,
    user_id: str,
    parser: str,
    use_vision_llm: bool,
) -> Extracted:
    from app.services.task_logging_service import TaskLoggingService
    from app.tasks.document_processors.file_processors import _extract_file_content

    task_logger = TaskLoggingService(session, workspace_id)
    log_entry = await task_logger.log_task_start(
        task_name="nextcloud_import",
        source="nextcloud",
        message=f"Importing {filename} from Nextcloud",
        metadata={"filename": filename, "parser": parser},
    )
    markdown, etl_service, billable = await _extract_file_content(
        file_path,
        filename,
        workspace_id,
        session,
        user_id,
        task_logger,
        log_entry,
        None,
        use_vision_llm=use_vision_llm,
        parser=parser,
    )
    await task_logger.log_task_success(log_entry, f"Imported {filename}", {})
    return Extracted(markdown, etl_service, billable)


async def store_original_once(
    session: AsyncSession,
    document: Document,
    data: bytes,
    filename: str,
    mime_type: str | None,
) -> None:
    """Keep the downloaded file as the document's original, like an upload does."""
    from app.file_storage.service import list_document_files, store_document_file

    if await list_document_files(session, document_id=document.id):
        return
    await store_document_file(
        session,
        document_id=document.id,
        workspace_id=document.workspace_id,
        data=data,
        filename=filename,
        mime_type=mime_type,
        created_by_id=str(document.created_by_id) if document.created_by_id else None,
    )


async def charge_pages(
    session: AsyncSession,
    user_id: str,
    pages: int,
    *,
    parser: str | None = None,
    workspace_id: int | None = None,
    reference: str | None = None,
) -> None:
    if pages <= 0:
        return
    from app.etl_pipeline.parser_selection import upload_parser_from_task
    from app.services.etl_credit_service import EtlCreditService

    await EtlCreditService(session).charge_credits(
        user_id,
        pages,
        parser=upload_parser_from_task(parser or None),
        workspace_id=workspace_id,
        reference=reference,
    )


async def _account_for(
    session: AsyncSession, connector_id: int | None
) -> NextcloudAccount:
    connector = (
        await session.get(SearchSourceConnector, connector_id)
        if connector_id is not None
        else None
    )
    if connector is None:
        raise InvalidNextcloudSettingsError("The Nextcloud connection was removed")
    return account_from_config(connector.config)


async def _fail(session: AsyncSession, document: Document, reason: str) -> str:
    await session.rollback()
    await session.refresh(document)
    document.status = DocumentStatus.failed(reason[:500])
    document.updated_at = datetime.now(UTC)
    await session.commit()
    return "failed"


async def _hand_over_to_duplicate(
    session: AsyncSession, placeholder: Document, duplicate_id: int
) -> str:
    notebook_id = nextcloud_facts(placeholder.document_metadata).get("notebook_id")
    if notebook_id is not None:
        with contextlib.suppress(Exception):
            async with session.begin_nested():
                await link_document(session, int(notebook_id), duplicate_id)
    await session.delete(placeholder)
    await session.commit()
    return "duplicate"


async def import_document(
    session: AsyncSession,
    document_id: int,
    user_id: str,
    *,
    max_bytes: int,
    client_for: ClientFactory = NextcloudClient,
    extract: Extractor = extract_like_an_upload,
    store_original: OriginalStore = store_original_once,
    charge: Charger = charge_pages,
) -> str:
    """Returns "ready", "unchanged", "duplicate", "failed" or "missing"."""
    document = await session.get(Document, document_id)
    if document is None:
        return "missing"
    facts = nextcloud_facts(document.document_metadata)
    path = facts.get("path")
    if not path:
        return await _fail(session, document, "Not a Nextcloud import")

    document.status = DocumentStatus.processing()
    await session.commit()

    temp_path: str | None = None
    try:
        account = await _account_for(session, facts.get("connector_id"))
        temp_path = await client_for(account).download(path, max_bytes=max_bytes)
        filename = document.title or Path(path).name
        data = Path(temp_path).read_bytes()
        try:
            await store_original(
                session, document, data, filename, facts.get("content_type")
            )
            await session.commit()
        except Exception:
            logger.warning(
                "keeping the original of %s failed", document_id, exc_info=True
            )
            await session.rollback()
            await session.refresh(document)
        extracted = await extract(
            session,
            temp_path,
            filename,
            document.workspace_id,
            user_id,
            facts.get("parser") or "",
            bool(facts.get("use_vision_llm")),
        )
    except (NextcloudError, InvalidNextcloudSettingsError) as exc:
        return await _fail(session, document, str(exc))
    except Exception as exc:
        logger.exception("Nextcloud import of document %s failed", document_id)
        return await _fail(session, document, f"Could not read the file: {exc}")
    finally:
        if temp_path:
            with contextlib.suppress(OSError):
                os.unlink(temp_path)

    connector_doc = ConnectorDocument(
        title=document.title,
        source_markdown=extracted.markdown,
        unique_id=source_id(account, path),
        document_type=DOCUMENT_TYPE,
        workspace_id=document.workspace_id,
        created_by_id=str(document.created_by_id or user_id),
        connector_id=None,
        folder_id=document.folder_id,
        metadata={
            **(document.document_metadata or {}),
            "ETL_SERVICE": extracted.etl_service,
        },
    )
    content_hash = compute_content_hash(connector_doc)

    if document.content_hash == content_hash:
        # Re-read after an etag change, but the text is the same.
        document.document_metadata = connector_doc.metadata
        document.status = DocumentStatus.ready()
        await session.commit()
        return "unchanged"

    duplicate_id = await session.scalar(
        select(Document.id).where(
            Document.content_hash == content_hash, Document.id != document.id
        )
    )
    if duplicate_id is not None:
        if document.source_markdown:
            return await _fail(
                session, document, "The file now matches another document"
            )
        return await _hand_over_to_duplicate(session, document, duplicate_id)

    try:
        pipeline = IndexingPipelineService(session)
        prepared = await pipeline.prepare_for_indexing([connector_doc])
        if not prepared:
            return await _fail(session, document, "Could not save the file's text")
        await pipeline.index_unless_store_owns(prepared[0], connector_doc)
        if charge is charge_pages:
            await charge_pages(
                session,
                user_id,
                extracted.billable_pages,
                parser=facts.get("parser") or None,
                workspace_id=document.workspace_id,
                reference=f"document_pages:{document.id}",
            )
        else:
            await charge(session, user_id, extracted.billable_pages)
        await session.commit()
    except Exception as exc:
        logger.exception("indexing Nextcloud document %s failed", document_id)
        return await _fail(session, document, f"Indexing failed: {exc}")
    return "ready"
