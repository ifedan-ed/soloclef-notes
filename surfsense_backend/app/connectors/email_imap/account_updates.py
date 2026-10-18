"""Changing a few fields of one stored mailbox without losing anyone else's change.

Used while mail is being read (a refreshed Microsoft token, or "needs
reconnect"), possibly at the same moment as the owner adds or removes a
mailbox. The connector row is locked and re-read first, and only the named
account's fields are merged in, so neither write undoes the other. A mailbox
removed in the meantime is simply not written back.
"""

from __future__ import annotations

from typing import Any

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm.attributes import flag_modified

from app.db import SearchSourceConnector

from .credentials import accounts_from_config


async def update_account_fields(
    session: AsyncSession,
    *,
    connector_id: int,
    account_id: str,
    fields: dict[str, Any],
) -> bool:
    """Merge ``fields`` into one account and commit; False if it is gone."""
    try:
        connector = await session.scalar(
            select(SearchSourceConnector)
            .where(SearchSourceConnector.id == connector_id)
            .with_for_update()
            .execution_options(populate_existing=True)
        )
        accounts = [
            dict(a)
            for a in accounts_from_config(connector.config if connector else None)
        ]
        target = next((a for a in accounts if a.get("id") == account_id), None)
        if connector is not None and target is not None:
            target.update(fields)
            connector.config = {**(connector.config or {}), "accounts": accounts}
            flag_modified(connector, "config")
        # Committing either way ends the transaction, releasing the row lock.
        await session.commit()
    except Exception:
        # Leave the caller's session usable (the chat turn goes on with it).
        await session.rollback()
        raise
    return connector is not None and target is not None
