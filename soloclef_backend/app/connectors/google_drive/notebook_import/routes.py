"""HTTP door of importing picked Drive files, for the connection's owner.

Importing needs permission to create documents in the workspace, and adding
to a notebook permission to edit it. The download, parse (the upload
pipeline's ETL) and billing happen in the Drive indexer's Celery task.
"""

from __future__ import annotations

from collections.abc import Callable
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm.attributes import flag_modified

from app.auth.context import AuthContext
from app.db import Permission, get_async_session
from app.users import get_auth_context
from app.utils.rbac import check_permission, check_workspace_access

from ..connect.connection_record import owned_drive_connector
from .import_start import ImportTarget, start_import
from .picked_files import is_importable, remember_picks
from .schemas import DriveImportRequest, DriveImportResult, PickedDriveFile

router = APIRouter()

# (connector id, workspace id, user id, picks) -> queues their download and parse.
ImportDispatcher = Callable[[int, int, UUID, list[PickedDriveFile]], None]


def dispatch_with_celery(
    connector_id: int, workspace_id: int, user_id: UUID, picks: list[PickedDriveFile]
) -> None:
    from app.tasks.celery_tasks.connector_tasks import index_google_drive_files_task

    index_google_drive_files_task.delay(
        connector_id,
        workspace_id,
        str(user_id),
        {"folders": [], "files": [{"id": p.id, "name": p.name} for p in picks]},
    )


def get_import_dispatcher() -> ImportDispatcher:
    return dispatch_with_celery


@router.post(
    "/connectors/{connector_id}/google-drive/import", response_model=DriveImportResult
)
async def import_picked_files(
    connector_id: int,
    body: DriveImportRequest,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
    dispatch: ImportDispatcher = Depends(get_import_dispatcher),
):
    connector = await owned_drive_connector(session, auth.user.id, connector_id)
    if connector is None:
        raise HTTPException(status_code=404, detail="Google Drive connection not found")
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
    if (connector.config or {}).get("auth_expired"):
        raise HTTPException(
            status_code=400,
            detail={
                "code": "google_drive_reconnect",
                "message": "Google no longer accepts this connection; connect again",
            },
        )

    unique = list({p.id: p for p in body.files}.values())
    importable = [p for p in unique if is_importable(p)]
    unsupported = [p.name for p in unique if not is_importable(p)]
    document_ids: list[int] = []
    if importable:
        connector.config = remember_picks(connector.config, importable)
        flag_modified(connector, "config")
        document_ids = await start_import(
            session,
            importable,
            ImportTarget(
                connector_id=connector.id,
                workspace_id=workspace_id,
                user_id=auth.user.id,
                notebook_id=body.notebook_id,
            ),
        )
    await session.commit()
    if importable:
        dispatch(connector.id, workspace_id, auth.user.id, importable)
    return DriveImportResult(
        document_ids=document_ids, queued=len(importable), unsupported=unsupported
    )
