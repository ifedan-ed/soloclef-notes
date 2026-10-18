"""Which of a workspace's documents this user can refresh from Nextcloud.

A picked file can be re-read only through the connection of the account it
came from, and only that connection's owner can use it (the app password is
theirs). The web client asks once per workspace so it can offer "Refresh from
Nextcloud" on exactly those documents.
"""

from __future__ import annotations

from uuid import UUID

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import Document, SearchSourceConnector, SearchSourceConnectorType

from .connector_credentials import InvalidNextcloudSettingsError, account_from_config
from .document_identity import METADATA_KEY, nextcloud_facts


async def refreshable_documents(
    session: AsyncSession, *, workspace_id: int, user_id: UUID
) -> dict[int, int]:
    """Document id -> the id of the user's connection that can refresh it."""
    # Account-level connector now (custom/ACCOUNT_CONNECTIONS.md): one row
    # per user regardless of workspace; only the documents below are scoped
    # to workspace_id.
    connectors = (
        await session.scalars(
            select(SearchSourceConnector).where(
                SearchSourceConnector.user_id == user_id,
                SearchSourceConnector.connector_type
                == SearchSourceConnectorType.NEXTCLOUD_CONNECTOR,
            )
        )
    ).all()
    by_account: dict[tuple[str, str], int] = {}
    for connector in connectors:
        try:
            account = account_from_config(connector.config)
        except InvalidNextcloudSettingsError:
            continue
        by_account.setdefault((account.username, account.base_url), connector.id)
    if not by_account:
        return {}

    rows = await session.execute(
        select(Document.id, Document.document_metadata[METADATA_KEY]).where(
            Document.workspace_id == workspace_id,
            Document.document_metadata[METADATA_KEY].is_not(None),
        )
    )
    refreshable: dict[int, int] = {}
    for document_id, block in rows:
        facts = nextcloud_facts({METADATA_KEY: block})
        connector_id = by_account.get((facts.get("username"), facts.get("server_url")))
        if connector_id is not None and facts.get("path"):
            refreshable[document_id] = connector_id
    return refreshable
