"""HTTP door of the Nextcloud picker: browse, search, preview and import a pick.

Every route acts for the connector's owner only (the app password is theirs)
within a workspace they belong to; importing also needs permission to create
documents, and linking to a notebook permission to edit it.
"""

from __future__ import annotations

from collections.abc import Callable
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth.context import AuthContext
from app.db import (
    Document,
    DocumentStatus,
    Permission,
    SearchSourceConnector,
    SearchSourceConnectorType,
    get_async_session,
)
from app.etl_pipeline.etl_document import DocumentParser
from app.users import get_auth_context
from app.utils.rbac import check_permission, check_workspace_access

from .client import (
    NextcloudAccount,
    NextcloudAuthError,
    NextcloudClient,
    NextcloudError,
    NextcloudNotFoundError,
)
from .connector_credentials import InvalidNextcloudSettingsError, account_from_config
from .document_identity import nextcloud_facts
from .import_selection import (
    MAX_IMPORT_FILES,
    ImportPlan,
    PickedItem,
    SelectionError,
    plan_import,
)
from .import_start import ImportOptions, start_import
from .refreshable_documents import refreshable_documents
from .remote_paths import InvalidRemotePathError, normalize_remote_path
from .schemas import (
    FolderListing,
    ImportPreview,
    ImportRequest,
    ImportResult,
    RefreshableDocument,
    RefreshableDocuments,
    RefreshResult,
    RemoteEntryRead,
    SearchResults,
)

router = APIRouter()

_BASE = "/connectors/{connector_id}/nextcloud"

ClientFactory = Callable[[NextcloudAccount], NextcloudClient]
# (document ids, user id, max bytes) -> queues their download and parse.
ImportDispatcher = Callable[[list[int], UUID, int], None]


def get_client_factory() -> ClientFactory:
    return NextcloudClient


def dispatch_with_celery(
    document_ids: list[int], user_id: UUID, max_bytes: int
) -> None:
    from .import_task import import_nextcloud_documents_task

    import_nextcloud_documents_task.delay(document_ids, str(user_id), max_bytes)


def get_import_dispatcher() -> ImportDispatcher:
    return dispatch_with_celery


def max_file_bytes() -> int:
    """The upload size limit (MAX_FILE_SIZE_MB) applies to imports too."""
    from app.routes.documents_routes import MAX_FILE_SIZE_BYTES

    return MAX_FILE_SIZE_BYTES


async def _owned_connector(
    session: AsyncSession, auth: AuthContext, connector_id: int
) -> SearchSourceConnector:
    connector = await session.scalar(
        select(SearchSourceConnector).where(
            SearchSourceConnector.id == connector_id,
            SearchSourceConnector.user_id == auth.user.id,
            SearchSourceConnector.connector_type
            == SearchSourceConnectorType.NEXTCLOUD_CONNECTOR,
        )
    )
    if connector is None:
        raise HTTPException(status_code=404, detail="Nextcloud connection not found")
    # user_id == auth.user.id above already confirms ownership; account-level
    # connector, no single workspace to check access against here.
    return connector


def _account(connector: SearchSourceConnector) -> NextcloudAccount:
    try:
        return account_from_config(connector.config)
    except (InvalidNextcloudSettingsError, ValueError) as exc:
        raise HTTPException(
            status_code=400,
            detail={"code": "nextcloud_reconnect", "message": str(exc)},
        ) from exc


def _http_error(exc: Exception) -> HTTPException:
    """Map client failures; auth is a 400, never a 401 that would log the user out."""
    if isinstance(exc, NextcloudAuthError):
        return HTTPException(
            status_code=400,
            detail={"code": "nextcloud_auth", "message": str(exc)},
        )
    if isinstance(exc, NextcloudNotFoundError):
        return HTTPException(status_code=404, detail=str(exc))
    if isinstance(exc, InvalidRemotePathError | SelectionError):
        return HTTPException(status_code=400, detail=str(exc))
    return HTTPException(status_code=502, detail=str(exc))


def _parser_or_400(value: str | None) -> DocumentParser:
    from app.etl_pipeline.parser_selection import (
        ParserUnavailableError,
        resolve_upload_parser,
    )

    try:
        return resolve_upload_parser(value, None)
    except ParserUnavailableError as exc:
        raise HTTPException(
            status_code=400,
            detail={
                "code": "parser_unavailable",
                "parser": exc.capability.parser.value,
                "reason": exc.capability.unavailable_reason,
            },
        ) from exc


async def _plan(
    client: NextcloudClient, body: ImportRequest, parser: DocumentParser
) -> ImportPlan:
    try:
        return await plan_import(
            client,
            [PickedItem(path=item.path, kind=item.kind) for item in body.items],
            parser=parser,
            vision=body.use_vision_llm,
            max_bytes=max_file_bytes(),
        )
    except (NextcloudError, InvalidRemotePathError, SelectionError) as exc:
        raise _http_error(exc) from exc


@router.get(_BASE + "/browse", response_model=FolderListing)
async def browse_folder(
    connector_id: int,
    path: str = Query("/", max_length=4096),
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    client_for: ClientFactory = Depends(get_client_factory),
):
    connector = await _owned_connector(session, auth, connector_id)
    try:
        folder = normalize_remote_path(path)
        entries = await client_for(_account(connector)).list_folder(folder)
    except (NextcloudError, InvalidRemotePathError) as exc:
        raise _http_error(exc) from exc
    return FolderListing(path=folder, entries=[RemoteEntryRead.of(e) for e in entries])


@router.get(_BASE + "/search", response_model=SearchResults)
async def search_files(
    connector_id: int,
    q: str = Query(..., min_length=2, max_length=200),
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    client_for: ClientFactory = Depends(get_client_factory),
):
    connector = await _owned_connector(session, auth, connector_id)
    try:
        entries = await client_for(_account(connector)).search(q)
    except NextcloudError as exc:
        raise _http_error(exc) from exc
    return SearchResults(query=q, entries=[RemoteEntryRead.of(e) for e in entries])


@router.post(_BASE + "/import/preview", response_model=ImportPreview)
async def preview_import(
    connector_id: int,
    body: ImportRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    client_for: ClientFactory = Depends(get_client_factory),
):
    """What an import of this pick would bring in, for the confirm step."""
    connector = await _owned_connector(session, auth, connector_id)
    plan = await _plan(
        client_for(_account(connector)), body, _parser_or_400(body.parser)
    )
    return ImportPreview(
        file_count=len(plan.files),
        total_bytes=plan.total_bytes,
        unsupported_count=len(plan.unsupported),
        too_large_count=len(plan.too_large),
        over_limit=plan.over_limit,
        max_files=MAX_IMPORT_FILES,
    )


@router.post(_BASE + "/import", response_model=ImportResult)
async def import_pick(
    connector_id: int,
    body: ImportRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    client_for: ClientFactory = Depends(get_client_factory),
    dispatch: ImportDispatcher = Depends(get_import_dispatcher),
):
    connector = await _owned_connector(session, auth, connector_id)
    workspace_id = body.workspace_id
    await check_workspace_access(session, auth, workspace_id)
    await check_permission(
        session,
        auth,
        workspace_id,
        Permission.DOCUMENTS_CREATE.value,
        "You don't have permission to create documents in this workspace",
    )
    if body.notebook_id is not None:
        from app.notebooks.access import authorized_notebook, touch

        notebook = await authorized_notebook(
            session,
            auth,
            workspace_id,
            body.notebook_id,
            Permission.CHATS_UPDATE,
            Permission.DOCUMENTS_READ,
        )
        touch(notebook)
    parser = _parser_or_400(body.parser)
    account = _account(connector)
    plan = await _plan(client_for(account), body, parser)
    if plan.over_limit:
        raise HTTPException(
            status_code=422,
            detail={
                "code": "too_many_files",
                "max_files": MAX_IMPORT_FILES,
                "message": f"Pick at most {MAX_IMPORT_FILES} files at a time",
            },
        )
    started = await start_import(
        session,
        plan,
        account,
        ImportOptions(
            connector_id=connector.id,
            workspace_id=workspace_id,
            user_id=auth.user.id,
            parser=parser,
            use_vision_llm=body.use_vision_llm,
            notebook_id=body.notebook_id,
        ),
    )
    await session.commit()
    if started.queued_ids:
        dispatch(started.queued_ids, auth.user.id, max_file_bytes())
    return ImportResult(
        document_ids=started.document_ids,
        folder_ids=started.folder_ids,
        queued=len(started.queued_ids),
        reused=started.reused,
        unsupported=plan.unsupported,
        too_large=plan.too_large,
    )


@router.get(
    "/workspaces/{workspace_id}/nextcloud/refreshable-documents",
    response_model=RefreshableDocuments,
)
async def list_refreshable_documents(
    workspace_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """The documents this user can refresh through one of their connections."""
    await check_workspace_access(session, auth, workspace_id)
    found = await refreshable_documents(
        session, workspace_id=workspace_id, user_id=auth.user.id
    )
    return RefreshableDocuments(
        documents=[
            RefreshableDocument(document_id=document_id, connector_id=connector_id)
            for document_id, connector_id in sorted(found.items())
        ]
    )


@router.post(_BASE + "/documents/{document_id}/refresh", response_model=RefreshResult)
async def refresh_document(
    connector_id: int,
    document_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    client_for: ClientFactory = Depends(get_client_factory),
    dispatch: ImportDispatcher = Depends(get_import_dispatcher),
):
    """Re-read one imported file if it changed in Nextcloud since (by etag)."""
    connector = await _owned_connector(session, auth, connector_id)
    account = _account(connector)
    document = await session.get(Document, document_id)
    facts = nextcloud_facts(document.document_metadata) if document else {}
    if (
        document is None
        or facts.get("connector_id") != connector.id
        or facts.get("username") != account.username
        or facts.get("server_url") != account.base_url
        or not facts.get("path")
    ):
        raise HTTPException(status_code=404, detail="Imported Nextcloud file not found")
    # The document's own workspace, not the connector's (account-level, no
    # single workspace) — same workspace it was imported into originally.
    await check_permission(
        session,
        auth,
        document.workspace_id,
        Permission.DOCUMENTS_UPDATE.value,
        "You don't have permission to update documents in this workspace",
    )
    try:
        entry = await client_for(account).stat(facts["path"])
    except (NextcloudError, InvalidRemotePathError) as exc:
        raise _http_error(exc) from exc
    ready = DocumentStatus.is_state(document.status, DocumentStatus.READY)
    if ready and entry.etag and entry.etag == facts.get("etag"):
        return RefreshResult(status="unchanged")
    document.document_metadata = {
        **(document.document_metadata or {}),
        "nextcloud": {
            **facts,
            "connector_id": connector.id,
            "etag": entry.etag,
            "size": entry.size,
            "modified": entry.modified.isoformat() if entry.modified else None,
        },
    }
    document.status = DocumentStatus.pending()
    await session.commit()
    dispatch([document.id], auth.user.id, max_file_bytes())
    return RefreshResult(status="queued")
