import logging
from dataclasses import dataclass
from datetime import UTC, datetime

from sqlalchemy import and_, func
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.future import select
from sqlalchemy.orm.attributes import flag_modified

from app.connectors.google_drive.client import GoogleDriveClient
from app.db import (
    Document,
    DocumentType,
    SearchSourceConnector,
    SearchSourceConnectorType,
)
from app.services.connector_enablement import (
    enabled_connectors_in_workspace,
    is_enabled_in_workspace,
)

logger = logging.getLogger(__name__)


@dataclass
class GoogleDriveAccount:
    id: int
    name: str

    @classmethod
    def from_connector(cls, connector: SearchSourceConnector) -> "GoogleDriveAccount":
        return cls(id=connector.id, name=connector.name)

    def to_dict(self) -> dict:
        return {"id": self.id, "name": self.name}


@dataclass
class GoogleDriveFile:
    file_id: str
    name: str
    mime_type: str
    web_view_link: str
    connector_id: int
    document_id: int

    @classmethod
    def from_document(cls, document: Document) -> "GoogleDriveFile":
        meta = document.document_metadata or {}
        return cls(
            file_id=meta.get("google_drive_file_id", ""),
            name=meta.get("google_drive_file_name", document.title),
            mime_type=meta.get("google_drive_mime_type", ""),
            web_view_link=meta.get("web_view_link", ""),
            connector_id=document.connector_id,
            document_id=document.id,
        )

    def to_dict(self) -> dict:
        return {
            "file_id": self.file_id,
            "name": self.name,
            "mime_type": self.mime_type,
            "web_view_link": self.web_view_link,
            "connector_id": self.connector_id,
            "document_id": self.document_id,
        }


class GoogleDriveToolMetadataService:
    def __init__(self, db_session: AsyncSession):
        self._db_session = db_session

    async def get_creation_context(self, workspace_id: int, user_id: str) -> dict:
        accounts = await self._get_google_drive_accounts(workspace_id, user_id)

        if not accounts:
            return {
                "accounts": [],
                "supported_types": [],
                "parent_folders": {},
                "error": "No Google Drive account connected",
            }

        accounts_with_status = []
        for acc in accounts:
            acc_dict = acc.to_dict()
            auth_expired = await self._check_account_health(acc.id)
            acc_dict["auth_expired"] = auth_expired
            if auth_expired:
                await self._persist_auth_expired(acc.id)
            accounts_with_status.append(acc_dict)

        parent_folders = await self._get_parent_folders_by_account(accounts_with_status)

        return {
            "accounts": accounts_with_status,
            "supported_types": ["google_doc", "google_sheet"],
            "parent_folders": parent_folders,
        }

    async def get_trash_context(
        self, workspace_id: int, user_id: str, file_name: str
    ) -> dict:
        """``user_id`` is unused for filtering (kept for the call sites' shape):
        the file is an ordinary workspace document, visible to every member
        the same as any other document; the connector that can act on it only
        needs to still be enabled here, any owner.
        """
        del user_id
        result = await self._db_session.execute(
            select(Document)
            .filter(
                and_(
                    Document.workspace_id == workspace_id,
                    Document.document_type == DocumentType.GOOGLE_DRIVE_FILE,
                    func.lower(Document.title) == func.lower(file_name),
                )
            )
            .order_by(Document.updated_at.desc().nullslast())
            .limit(1)
        )
        document = result.scalars().first()

        if not document:
            return {
                "error": (
                    f"File '{file_name}' not found in your indexed Google Drive files. "
                    "This could mean: (1) the file doesn't exist, (2) it hasn't been indexed yet, "
                    "or (3) the file name is different."
                )
            }

        if not document.connector_id:
            return {"error": "Document has no associated connector"}

        result = await self._db_session.execute(
            select(SearchSourceConnector).filter(
                and_(
                    SearchSourceConnector.id == document.connector_id,
                    SearchSourceConnector.connector_type
                    == SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR,
                )
            )
        )
        connector = result.scalars().first()

        if not connector or not await is_enabled_in_workspace(
            self._db_session, connector, workspace_id
        ):
            return {"error": "Connector not found or access denied"}

        account = GoogleDriveAccount.from_connector(connector)
        file = GoogleDriveFile.from_document(document)

        acc_dict = account.to_dict()
        auth_expired = await self._check_account_health(connector.id)
        acc_dict["auth_expired"] = auth_expired
        if auth_expired:
            await self._persist_auth_expired(connector.id)

        return {
            "account": acc_dict,
            "file": file.to_dict(),
        }

    async def _get_google_drive_accounts(
        self, workspace_id: int, user_id: str
    ) -> list[GoogleDriveAccount]:
        """Every Google Drive connector enabled in this workspace, any owner.

        ``user_id`` is unused for filtering (kept for the call sites' shape):
        once a connection is enabled in a workspace, every member can use it
        through the agent's tools, same as any other enabled connection —
        see custom/ACCOUNT_CONNECTIONS.md.
        """
        del user_id
        connectors = await enabled_connectors_in_workspace(
            self._db_session,
            workspace_id,
            SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR,
        )
        connectors = sorted(
            connectors,
            key=lambda c: c.last_indexed_at or datetime.min.replace(tzinfo=UTC),
            reverse=True,
        )
        return [GoogleDriveAccount.from_connector(c) for c in connectors]

    async def _check_account_health(self, connector_id: int) -> bool:
        """Check if a Google Drive connector's credentials are still valid.

        Uses a lightweight ``files.list(pageSize=1)`` call to verify access.

        Returns True if the credentials are expired/invalid, False if healthy.
        """
        try:
            result = await self._db_session.execute(
                select(SearchSourceConnector).where(
                    SearchSourceConnector.id == connector_id
                )
            )
            connector = result.scalar_one_or_none()
            if not connector:
                return True

            client = GoogleDriveClient(
                session=self._db_session,
                connector_id=connector_id,
            )
            await client.list_files(
                query="trashed = false", page_size=1, fields="files(id)"
            )
            return False
        except Exception as e:
            logger.warning(
                "Google Drive connector %s health check failed: %s",
                connector_id,
                e,
            )
            return True

    async def _persist_auth_expired(self, connector_id: int) -> None:
        """Persist ``auth_expired: True`` to the connector config if not already set."""
        try:
            result = await self._db_session.execute(
                select(SearchSourceConnector).where(
                    SearchSourceConnector.id == connector_id
                )
            )
            db_connector = result.scalar_one_or_none()
            if db_connector and not db_connector.config.get("auth_expired"):
                db_connector.config = {**db_connector.config, "auth_expired": True}
                flag_modified(db_connector, "config")
                await self._db_session.commit()
                await self._db_session.refresh(db_connector)
        except Exception:
            logger.warning(
                "Failed to persist auth_expired for connector %s",
                connector_id,
                exc_info=True,
            )

    async def _get_parent_folders_by_account(
        self, accounts_with_status: list[dict]
    ) -> dict[int, list[dict]]:
        """Fetch root-level folders for each healthy account.

        Skips accounts where ``auth_expired`` is True so we don't waste an API
        call that will fail anyway.
        """
        parent_folders: dict[int, list[dict]] = {}

        for acc in accounts_with_status:
            connector_id = acc["id"]
            if acc.get("auth_expired"):
                parent_folders[connector_id] = []
                continue

            try:
                result = await self._db_session.execute(
                    select(SearchSourceConnector).where(
                        SearchSourceConnector.id == connector_id
                    )
                )
                connector = result.scalar_one_or_none()
                if not connector:
                    parent_folders[connector_id] = []
                    continue

                client = GoogleDriveClient(
                    session=self._db_session,
                    connector_id=connector_id,
                )

                folders, _, error = await client.list_files(
                    query="mimeType = 'application/vnd.google-apps.folder' and trashed = false and 'root' in parents",
                    fields="files(id, name)",
                    page_size=50,
                )

                if error:
                    logger.warning(
                        "Failed to list folders for connector %s: %s",
                        connector_id,
                        error,
                    )
                    parent_folders[connector_id] = []
                else:
                    parent_folders[connector_id] = [
                        {"folder_id": f["id"], "name": f["name"]}
                        for f in folders
                        if f.get("id") and f.get("name")
                    ]
            except Exception:
                logger.warning(
                    "Error fetching folders for connector %s",
                    connector_id,
                    exc_info=True,
                )
                parent_folders[connector_id] = []

        return parent_folders
