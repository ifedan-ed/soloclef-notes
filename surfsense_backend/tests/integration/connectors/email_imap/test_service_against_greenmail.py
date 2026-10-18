"""Service-layer tests against a real GreenMail IMAP server.

Covers: add -> test-connection -> search -> read against real IMAP wire
protocol, the read-only guarantee (``\\Seen`` is never set as a side effect of
our own read path), and access control (one user's account is invisible to
another user, even in the same workspace).

Every delivered test message carries a random token in its subject, because
the GreenMail mailbox is shared and persists messages across test runs.
"""

from __future__ import annotations

import uuid

import pytest

from app.connectors.email_imap import service
from app.connectors.email_imap.service import EmailAccountNotFoundError

from .conftest import ALICE, deliver_message, raw_imap_connection, requires_greenmail

pytestmark = [pytest.mark.integration, requires_greenmail]


async def _add_alice_account(db_session, owner, imap_settings, *, label="Alice"):
    return await service.add_account(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        label=label,
        host=imap_settings["host"],
        port=imap_settings["port"],
        use_tls=imap_settings["use_tls"],
        username=ALICE.username,
        app_password=ALICE.password,
    )


async def test_add_then_test_connection_succeeds_against_a_real_server(
    db_session, make_owner, imap_settings
):
    owner = await make_owner()
    account = await _add_alice_account(db_session, owner, imap_settings)

    result = await service.test_connection(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
    )
    assert result["status"] == "ok"
    assert result["folder_count"] >= 1


async def test_test_connection_reports_a_bad_password(db_session, make_owner, imap_settings):
    owner = await make_owner()
    account = await service.add_account(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        label="Bad",
        host=imap_settings["host"],
        port=imap_settings["port"],
        use_tls=imap_settings["use_tls"],
        username=ALICE.username,
        app_password="wrong-password",
    )
    result = await service.test_connection(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
    )
    assert result["status"] == "error"


async def test_search_finds_a_delivered_message_across_the_real_server(
    db_session, make_owner, imap_settings
):
    owner = await make_owner()
    account = await _add_alice_account(db_session, owner, imap_settings)

    # A unique token in the subject keeps this test correct even though the
    # shared GreenMail mailbox accumulates messages from other test runs.
    token = uuid.uuid4().hex[:12]
    deliver_message(
        ALICE.email,
        subject=f"Your Prudential life insurance policy renewal {token}",
        body="Policy PRU-12345 renews on 2026-01-01.",
        from_="prudential@example.test",
    )

    results = await service.search_across_accounts(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=None,
        folder="INBOX",
        text=None,
        from_="prudential",
        to=None,
        subject=token,
        since=None,
        before=None,
        limit=20,
    )
    assert len(results) == 1
    assert "Prudential" in results[0]["subject"]
    assert results[0]["account_id"] == account["id"]


async def test_read_one_message_returns_body_and_no_attachments(
    db_session, make_owner, imap_settings
):
    owner = await make_owner()
    account = await _add_alice_account(db_session, owner, imap_settings)
    token = uuid.uuid4().hex[:12]
    deliver_message(ALICE.email, subject=f"Hello {token}", body="Just saying hi.")

    results = await service.search_across_accounts(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
        folder="INBOX",
        text=None,
        from_=None,
        to=None,
        subject=token,
        since=None,
        before=None,
        limit=5,
    )
    assert results, "expected the delivered message to be found"
    uid = results[0]["uid"]

    message = await service.read_one_message(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
        folder="INBOX",
        uid=uid,
    )
    assert "Just saying hi." in message["body"]
    assert message["attachments"] == []


async def test_reading_a_message_never_sets_seen_on_the_server(
    db_session, make_owner, imap_settings
):
    """The read-only guarantee: BODY.PEEK + EXAMINE never flips \\Seen."""
    owner = await make_owner()
    account = await _add_alice_account(db_session, owner, imap_settings)
    token = uuid.uuid4().hex[:12]
    deliver_message(ALICE.email, subject=f"Unread check {token}", body="Do not mark me read.")

    results = await service.search_across_accounts(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
        folder="INBOX",
        text=None,
        from_=None,
        to=None,
        subject=token,
        since=None,
        before=None,
        limit=5,
    )
    uid = results[0]["uid"]

    await service.read_one_message(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
        folder="INBOX",
        uid=uid,
    )

    # Independently verify server-side state over a raw IMAP connection.
    conn = raw_imap_connection()
    try:
        conn.login(ALICE.username, ALICE.password)
        conn.select("INBOX")  # fine for this verification probe
        typ, data = conn.fetch(uid.encode(), "(FLAGS)")
        assert typ == "OK"
        flags = data[0].decode() if data and data[0] else ""
        assert "\\Seen" not in flags, f"message was marked Seen: {flags}"
    finally:
        conn.logout()


def test_not_every_imap_server_enforces_examine_readonly():
    """Documents why the read-only guarantee can't rely on EXAMINE alone.

    GreenMail (used here) accepts a STORE against a mailbox opened with
    EXAMINE/readonly=True instead of rejecting it per RFC 3501 — verified
    directly against it, independent of our own client. That is exactly why
    :class:`ReadOnlyImapClient` has no STORE/EXPUNGE/COPY/APPEND method at
    all (see ``test_client_exposes_no_write_methods`` in the unit suite) and
    always fetches with BODY.PEEK: the guarantee is "our code never issues a
    write", not "the server would stop us if it did".
    """
    conn = raw_imap_connection()
    try:
        conn.login(ALICE.username, ALICE.password)
        conn.select("INBOX", readonly=True)
        typ, _data = conn.store("1", "+FLAGS", "\\Flagged")
        assert typ == "OK", (
            "If this ever starts failing, GreenMail began enforcing EXAMINE "
            "read-only — harmless, just update this test's docstring."
        )
    finally:
        conn.logout()


async def test_search_rejects_a_folder_the_account_never_listed(
    db_session, make_owner, imap_settings
):
    """A folder not among the account's real ``LIST`` folders — whether a
    typo, a stale value, or an LLM tool-call argument — is refused as a
    per-account error rather than sent to EXAMINE/SELECT."""
    owner = await make_owner()
    account = await _add_alice_account(db_session, owner, imap_settings)

    results = await service.search_across_accounts(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
        folder="NoSuchFolder",
        text=None,
        from_=None,
        to=None,
        subject=None,
        since=None,
        before=None,
        limit=5,
    )
    assert len(results) == 1
    assert "error" in results[0]
    assert results[0]["account_id"] == account["id"]


async def test_search_rejects_a_folder_name_carrying_a_crlf_injection_attempt(
    db_session, make_owner, imap_settings
):
    """A folder argument containing CRLF (an attempt to smuggle a second
    IMAP command onto the wire) is refused before anything is sent, and the
    connection is still usable afterward — a crafted argument cannot corrupt
    or hijack the session."""
    owner = await make_owner()
    account = await _add_alice_account(db_session, owner, imap_settings)
    token = uuid.uuid4().hex[:12]
    deliver_message(ALICE.email, subject=f"Injection probe {token}", body="hi")

    injected_folder = 'INBOX"\r\nA999 LOGIN attacker attacker\r\nA998 EXAMINE "INBOX'
    results = await service.search_across_accounts(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
        folder=injected_folder,
        text=None,
        from_=None,
        to=None,
        subject=token,
        since=None,
        before=None,
        limit=5,
    )
    assert len(results) == 1
    assert "error" in results[0]

    # The account is still perfectly usable afterward against a real folder.
    clean_results = await service.search_across_accounts(
        db_session,
        workspace_id=owner.workspace.id,
        user_id=owner.user.id,
        account_id=account["id"],
        folder="INBOX",
        text=None,
        from_=None,
        to=None,
        subject=token,
        since=None,
        before=None,
        limit=5,
    )
    assert len(clean_results) == 1
    assert "error" not in clean_results[0]


async def test_user_b_cannot_see_user_a_account(db_session, make_owner, imap_settings):
    """Isolation: accounts are private to the user, not shared workspace-wide."""
    alice_owner = await make_owner()
    bob_owner = await make_owner()

    account = await _add_alice_account(db_session, alice_owner, imap_settings)

    # Bob, in his own workspace, sees no accounts at all.
    bob_accounts = await service.list_accounts(
        db_session, workspace_id=bob_owner.workspace.id, user_id=bob_owner.user.id
    )
    assert bob_accounts == []

    # Bob querying Alice's own workspace (e.g. if he somehow had access to it)
    # still cannot see or use her account, because the row is scoped by user_id.
    bob_in_alice_workspace_accounts = await service.list_accounts(
        db_session, workspace_id=alice_owner.workspace.id, user_id=bob_owner.user.id
    )
    assert bob_in_alice_workspace_accounts == []

    with pytest.raises(EmailAccountNotFoundError):
        await service.test_connection(
            db_session,
            workspace_id=alice_owner.workspace.id,
            user_id=bob_owner.user.id,
            account_id=account["id"],
        )

    with pytest.raises(EmailAccountNotFoundError):
        await service.delete_account(
            db_session,
            workspace_id=alice_owner.workspace.id,
            user_id=bob_owner.user.id,
            account_id=account["id"],
        )

    with pytest.raises(EmailAccountNotFoundError):
        await service.search_across_accounts(
            db_session,
            workspace_id=alice_owner.workspace.id,
            user_id=bob_owner.user.id,
            account_id=account["id"],
            folder="INBOX",
            text=None,
            from_=None,
            to=None,
            subject=None,
            since=None,
            before=None,
            limit=5,
        )

    # Alice herself still can.
    mine = await service.list_accounts(
        db_session, workspace_id=alice_owner.workspace.id, user_id=alice_owner.user.id
    )
    assert len(mine) == 1
