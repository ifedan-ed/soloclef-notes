"""DB-aware operations for the Email (IMAP) connector.

The connector row is owned by one account (``user_id``). Two lookups read
it: management calls (behind the settings routes) always use the caller's
own row only, account-wide; usage calls from the chat tools
(``for_workspace_use=True``) read every such connector enabled in the given
workspace, any owner, per ``app/services/connector_enablement.py``.
"""

from __future__ import annotations

import logging
import uuid as uuid_mod
from typing import Any

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm.attributes import flag_modified

from app.db import SearchSourceConnector, SearchSourceConnectorType
from app.services.connector_enablement import enabled_connectors_in_workspace

from . import credentials as creds
from .account_sign_in import imap_settings_for
from .attachments import extract_attachment_text
from .client import ImapAccountSettings, ImapConnectionError, ReadOnlyImapClient
from .message_parsing import summarize_message
from .search_results import newest_first_within_limit

logger = logging.getLogger(__name__)

CONNECTOR_NAME = "Email (IMAP)"


class EmailAccountNotFoundError(Exception):
    pass


def _assert_known_folder(client: ReadOnlyImapClient, folder: str) -> None:
    """Refuse a folder (an LLM tool argument) the account's own ``LIST``
    response never mentioned, before it reaches ``EXAMINE`` on the wire.

    ``client.examine`` already rejects CR/LF/NUL and quotes the mailbox
    argument, which stops command injection; this adds the complementary
    check that the name is one the server actually advertises, rather than
    an arbitrary string the model constructed.
    """
    if folder not in client.list_folders():
        raise ImapConnectionError(f"Folder '{folder}' was not found on this account")


async def _get_or_none(
    session: AsyncSession, *, user_id: Any
) -> SearchSourceConnector | None:
    """The caller's own connector, account-wide (not scoped to a workspace)."""
    result = await session.execute(
        select(SearchSourceConnector).where(
            SearchSourceConnector.user_id == user_id,
            SearchSourceConnector.connector_type
            == SearchSourceConnectorType.EMAIL_IMAP_CONNECTOR,
        )
    )
    return result.scalars().first()


async def _get_or_create(
    session: AsyncSession, *, user_id: Any
) -> SearchSourceConnector:
    connector = await _get_or_none(session, user_id=user_id)
    if connector is not None:
        return connector
    connector = SearchSourceConnector(
        name=CONNECTOR_NAME,
        connector_type=SearchSourceConnectorType.EMAIL_IMAP_CONNECTOR,
        is_indexable=False,
        config={"accounts": []},
        workspace_id=None,
        user_id=user_id,
    )
    session.add(connector)
    await session.flush()
    return connector


async def _readable_connectors(
    session: AsyncSession, *, workspace_id: int, user_id: Any, for_workspace_use: bool
) -> list[SearchSourceConnector]:
    """Which connector row(s) a call should read.

    ``for_workspace_use=True`` (the chat tools): every connector of this
    type enabled in ``workspace_id``, any owner. Otherwise (the settings
    routes): only the caller's own, as a single-item list (or empty).
    """
    if for_workspace_use:
        return await enabled_connectors_in_workspace(
            session, workspace_id, SearchSourceConnectorType.EMAIL_IMAP_CONNECTOR
        )
    own = await _get_or_none(session, user_id=user_id)
    return [own] if own is not None else []


async def list_accounts(
    session: AsyncSession,
    *,
    workspace_id: int,
    user_id: Any,
    for_workspace_use: bool = False,
) -> list[dict[str, Any]]:
    connectors = await _readable_connectors(
        session,
        workspace_id=workspace_id,
        user_id=user_id,
        for_workspace_use=for_workspace_use,
    )
    out: list[dict[str, Any]] = []
    for connector in connectors:
        out.extend(
            creds.public_account(a)
            for a in creds.accounts_from_config(connector.config)
        )
    return out


async def add_account(
    session: AsyncSession,
    *,
    workspace_id: int,  # unused: kept so routes.py's call site doesn't change
    user_id: Any,
    label: str,
    host: str,
    port: int,
    use_tls: bool,
    username: str,
    app_password: str,
) -> dict[str, Any]:
    del workspace_id
    connector = await _get_or_create(session, user_id=user_id)
    record = creds.new_account_record(
        label=label,
        host=host,
        port=port,
        use_tls=use_tls,
        username=username,
        app_password=app_password,
    )
    accounts = creds.accounts_from_config(connector.config)
    accounts.append(record)
    connector.config = {**connector.config, "accounts": accounts}
    flag_modified(connector, "config")
    await session.commit()
    return creds.public_account(record)


async def delete_account(
    session: AsyncSession,
    *,
    workspace_id: int,  # unused: kept so routes.py's call site doesn't change
    user_id: Any,
    account_id: str,
) -> None:
    del workspace_id
    connector = await _get_or_none(session, user_id=user_id)
    if connector is None:
        raise EmailAccountNotFoundError(account_id)
    accounts = creds.accounts_from_config(connector.config)
    remaining = [a for a in accounts if a.get("id") != account_id]
    if len(remaining) == len(accounts):
        raise EmailAccountNotFoundError(account_id)
    connector.config = {**connector.config, "accounts": remaining}
    flag_modified(connector, "config")
    await session.commit()


async def _account_settings(
    session: AsyncSession,
    *,
    workspace_id: int,
    user_id: Any,
    account_id: str,
    for_workspace_use: bool = False,
) -> tuple[dict[str, Any], ImapAccountSettings]:
    connectors = await _readable_connectors(
        session,
        workspace_id=workspace_id,
        user_id=user_id,
        for_workspace_use=for_workspace_use,
    )
    account = None
    for connector in connectors:
        account = next(
            (
                a
                for a in creds.accounts_from_config(connector.config)
                if a.get("id") == account_id
            ),
            None,
        )
        if account is not None:
            break
    if account is None:
        raise EmailAccountNotFoundError(account_id)
    settings = await imap_settings_for(
        session, connector_id=connector.id, account=account
    )
    return account, settings


async def test_raw_connection(
    *, host: str, port: int, use_tls: bool, username: str, app_password: str
) -> dict[str, Any]:
    """Test connection settings before an account is saved — nothing is persisted."""
    settings = ImapAccountSettings(
        host=host, port=port, use_tls=use_tls, username=username, password=app_password
    )
    try:
        with ReadOnlyImapClient(settings) as client:
            folders = client.list_folders()
        return {"status": "ok", "folder_count": len(folders)}
    except ImapConnectionError as exc:
        return {"status": "error", "message": str(exc)}


async def test_connection(
    session: AsyncSession, *, workspace_id: int, user_id: Any, account_id: str
) -> dict[str, Any]:
    try:
        _account, settings = await _account_settings(
            session, workspace_id=workspace_id, user_id=user_id, account_id=account_id
        )
        with ReadOnlyImapClient(settings) as client:
            folders = client.list_folders()
        return {"status": "ok", "folder_count": len(folders)}
    except ImapConnectionError as exc:
        return {"status": "error", "message": str(exc)}


async def list_folders_for_account(
    session: AsyncSession,
    *,
    workspace_id: int,
    user_id: Any,
    account_id: str,
    for_workspace_use: bool = False,
) -> list[str]:
    _account, settings = await _account_settings(
        session,
        workspace_id=workspace_id,
        user_id=user_id,
        account_id=account_id,
        for_workspace_use=for_workspace_use,
    )
    with ReadOnlyImapClient(settings) as client:
        return client.list_folders()


async def _all_account_rows(
    session: AsyncSession,
    *,
    workspace_id: int,
    user_id: Any,
    for_workspace_use: bool = False,
) -> list[tuple[int, dict[str, Any]]]:
    """Every readable account, with the id of the connector row it lives on."""
    connectors = await _readable_connectors(
        session,
        workspace_id=workspace_id,
        user_id=user_id,
        for_workspace_use=for_workspace_use,
    )
    out: list[tuple[int, dict[str, Any]]] = []
    for connector in connectors:
        out.extend(
            (connector.id, account)
            for account in creds.accounts_from_config(connector.config)
        )
    return out


async def search_across_accounts(
    session: AsyncSession,
    *,
    workspace_id: int,
    user_id: Any,
    account_id: str | None,
    folder: str,
    text: str | None,
    from_: str | None,
    to: str | None,
    subject: str | None,
    since,
    before,
    limit: int,
    for_workspace_use: bool = False,
    unread_only: bool = False,
) -> list[dict[str, Any]]:
    """Every account's hits, newest first across accounts, at most ``limit``
    of them, plus one error row per account that could not be searched."""
    accounts = await _all_account_rows(
        session,
        workspace_id=workspace_id,
        user_id=user_id,
        for_workspace_use=for_workspace_use,
    )
    if account_id is not None:
        accounts = [(c, a) for c, a in accounts if a.get("id") == account_id]
        if not accounts:
            raise EmailAccountNotFoundError(account_id)

    results: list[dict[str, Any]] = []
    for connector_id, account in accounts:
        try:
            settings = await imap_settings_for(
                session, connector_id=connector_id, account=account
            )
        except (ImapConnectionError, creds.InvalidEmailAccountError) as exc:
            # One account that cannot sign in (a Microsoft mailbox to
            # reconnect, a password that no longer decrypts) is reported in
            # the results; the other accounts are still searched.
            results.append(
                {
                    "account_id": account.get("id"),
                    "account_label": account.get("label"),
                    "error": str(exc),
                }
            )
            continue
        try:
            with ReadOnlyImapClient(settings) as client:
                _assert_known_folder(client, folder)
                client.examine(folder)
                uids = client.search_uids(
                    text=text,
                    from_=from_,
                    to=to,
                    subject=subject,
                    since=since,
                    before=before,
                    unseen=unread_only,
                    limit=limit,
                )
                for uid in uids:
                    summary = client.fetch_summary(uid)
                    summary["account_id"] = account.get("id")
                    summary["account_label"] = account.get("label")
                    summary["folder"] = folder
                    results.append(summary)
        except ImapConnectionError as exc:
            logger.info(
                "email_imap search skipped account %s: %s", account.get("id"), exc
            )
            results.append(
                {
                    "account_id": account.get("id"),
                    "account_label": account.get("label"),
                    "error": str(exc),
                }
            )
    return newest_first_within_limit(results, limit)


async def read_one_message(
    session: AsyncSession,
    *,
    workspace_id: int,
    user_id: Any,
    account_id: str,
    folder: str,
    uid: str,
    for_workspace_use: bool = False,
) -> dict[str, Any]:
    _account, settings = await _account_settings(
        session,
        workspace_id=workspace_id,
        user_id=user_id,
        account_id=account_id,
        for_workspace_use=for_workspace_use,
    )
    with ReadOnlyImapClient(settings) as client:
        _assert_known_folder(client, folder)
        client.examine(folder)
        msg = client.fetch_message(uid.encode())
    return summarize_message(msg, uid=uid)


async def read_attachment_text(
    session: AsyncSession,
    *,
    workspace_id: int,
    user_id: Any,
    account_id: str,
    folder: str,
    uid: str,
    filename: str,
    for_workspace_use: bool = False,
) -> str | None:
    """Best-effort plain text for one named attachment of a message."""
    _account, settings = await _account_settings(
        session,
        workspace_id=workspace_id,
        user_id=user_id,
        account_id=account_id,
        for_workspace_use=for_workspace_use,
    )
    with ReadOnlyImapClient(settings) as client:
        _assert_known_folder(client, folder)
        client.examine(folder)
        msg = client.fetch_message(uid.encode())
    return await extract_attachment_text(
        msg,
        filename,
        session=session,
        workspace_id=workspace_id,
        user_id=user_id,
    )


def normalize_uuid_or_none(value: str | None) -> str | None:
    if value is None:
        return None
    # Account ids are plain uuid4 strings; just validate shape, don't coerce type.
    uuid_mod.UUID(value)
    return value
