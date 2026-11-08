"""Saving a connected Microsoft mailbox on its owner's Email (IMAP) connector row.

The same per-person row as password accounts (``service.py``): account-level,
private to the person who connected it. Connecting a mailbox that is already
there (same Microsoft account) replaces its tokens in place: that is what
"Reconnect" does.
"""

from __future__ import annotations

from typing import Any

from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm.attributes import flag_modified

from .. import credentials as creds
from ..service import _get_or_create, _get_or_none
from .account_record import (
    is_microsoft,
    is_same_mailbox,
    new_mailbox_record,
    reconnected,
)
from .mailbox_identity import Mailbox
from .token_endpoint import TokenGrant


class MicrosoftMailboxStore:
    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def address_of(self, user_id: Any, account_id: str) -> str | None:
        """The address of one of this person's own Microsoft mailboxes."""
        connector = await _get_or_none(self._session, user_id=user_id)
        if connector is None:
            return None
        for account in creds.accounts_from_config(connector.config):
            if account.get("id") == account_id and is_microsoft(account):
                return account.get("username")
        return None

    async def save(
        self,
        user_id: Any,
        mailbox: Mailbox,
        grant: TokenGrant,
        *,
        secret_key: str,
    ) -> dict[str, Any]:
        connector = await _get_or_create(self._session, user_id=user_id)
        # Lock and re-read the row: a token refresh may be saving into it.
        await self._session.refresh(connector, with_for_update=True)
        accounts = [dict(a) for a in creds.accounts_from_config(connector.config)]
        for index, account in enumerate(accounts):
            if is_same_mailbox(account, mailbox):
                record = reconnected(account, mailbox, grant, secret_key=secret_key)
                accounts[index] = record
                break
        else:
            record = new_mailbox_record(mailbox, grant, secret_key=secret_key)
            accounts.append(record)
        connector.config = {**(connector.config or {}), "accounts": accounts}
        flag_modified(connector, "config")
        await self._session.commit()
        return record
