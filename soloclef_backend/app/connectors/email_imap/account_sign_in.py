"""How to sign in to one stored mailbox: its app password, or a Microsoft token.

Password accounts decrypt their app password. Microsoft accounts get a
working access token (``microsoft/imap_sign_in.py``), and whatever that
changes on the record (a refreshed token, "needs reconnect") is saved back on
the connector row the account lives on.
"""

from __future__ import annotations

from typing import Any

from sqlalchemy.ext.asyncio import AsyncSession

from . import credentials as creds
from .account_updates import update_account_fields
from .client import ImapAccountSettings
from .microsoft.account_record import is_microsoft
from .microsoft.imap_sign_in import microsoft_imap_settings


async def imap_settings_for(
    session: AsyncSession, *, connector_id: int, account: dict[str, Any]
) -> ImapAccountSettings:
    """Raises ``ImapConnectionError`` when a Microsoft mailbox cannot sign in,
    ``InvalidEmailAccountError`` when a password no longer decrypts."""
    if is_microsoft(account):

        async def save(fields: dict[str, Any]) -> None:
            await update_account_fields(
                session,
                connector_id=connector_id,
                account_id=str(account.get("id")),
                fields=fields,
            )

        return await microsoft_imap_settings(account, save=save)
    return ImapAccountSettings(
        host=account["host"],
        port=int(account["port"]),
        use_tls=bool(account.get("use_tls", True)),
        username=account["username"],
        password=creds.decrypt_password(account),
    )
