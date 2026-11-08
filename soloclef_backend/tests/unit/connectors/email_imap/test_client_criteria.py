"""IMAP SEARCH criteria building, in isolation from any real connection."""

from __future__ import annotations

from datetime import date
from unittest.mock import MagicMock

import pytest

from app.connectors.email_imap.client import (
    MAX_SEARCH_RESULTS,
    ImapAccountSettings,
    ReadOnlyImapClient,
)

pytestmark = pytest.mark.unit


def _client_with_fake_conn(uid_return=("OK", [b"1 2 3"])):
    client = ReadOnlyImapClient(
        ImapAccountSettings(
            host="x", port=993, use_tls=True, username="u", password="p"
        )
    )
    fake = MagicMock()
    fake.uid.return_value = uid_return
    client._imap = fake
    return client, fake


def test_search_uids_builds_all_when_no_criteria_given():
    client, fake = _client_with_fake_conn()
    client.search_uids()
    args = fake.uid.call_args[0]
    assert args[:2] == ("search", None)
    assert args[2:] == ("ALL",)


def test_search_uids_combines_criteria():
    client, fake = _client_with_fake_conn()
    client.search_uids(
        text="invoice",
        from_="billing@example.com",
        subject="Receipt",
        since=date(2026, 1, 1),
        before=date(2026, 2, 1),
    )
    criteria = fake.uid.call_args[0][2:]
    assert "TEXT" in criteria
    assert '"invoice"' in criteria
    assert "HEADER" in criteria
    assert "FROM" in criteria
    assert '"billing@example.com"' in criteria
    assert "SUBJECT" in criteria
    assert '"Receipt"' in criteria
    assert "SINCE" in criteria
    assert "01-Jan-2026" in criteria
    assert "BEFORE" in criteria
    assert "01-Feb-2026" in criteria


def test_search_uids_escapes_quotes_and_backslashes():
    client, fake = _client_with_fake_conn()
    client.search_uids(subject='He said "hi"\\there')
    criteria = fake.uid.call_args[0][2:]
    assert '"He said \\"hi\\"\\\\there"' in criteria


def test_search_uids_returns_most_recent_first_and_caps_at_limit():
    client, _fake = _client_with_fake_conn(
        uid_return=("OK", [b" ".join(str(i).encode() for i in range(1, 11))])
    )
    uids = client.search_uids(limit=3)
    assert uids == [b"10", b"9", b"8"]


def test_search_uids_never_exceeds_hard_cap_even_if_a_huge_limit_is_requested():
    many = b" ".join(str(i).encode() for i in range(1, 200))
    client, _fake = _client_with_fake_conn(uid_return=("OK", [many]))
    uids = client.search_uids(limit=10_000)
    assert len(uids) == MAX_SEARCH_RESULTS


def test_examine_is_always_readonly():
    client, fake = _client_with_fake_conn()
    fake.select.return_value = ("OK", [b"5"])
    count = client.examine("INBOX")
    # Quoted: imaplib's own select()/examine() send the mailbox argument
    # as-is, so this wrapper quotes it itself (see _quote_imap_string).
    fake.select.assert_called_once_with('"INBOX"', readonly=True)
    assert count == 5


def test_examine_quotes_a_folder_name_containing_special_characters():
    client, fake = _client_with_fake_conn()
    fake.select.return_value = ("OK", [b"0"])
    client.examine('Weird "Folder"\\Name')
    fake.select.assert_called_once_with(r'"Weird \"Folder\"\\Name"', readonly=True)


@pytest.mark.parametrize(
    "bad_folder", ["INBOX\r\nA2 LOGIN evil evil", "a\nb", "a\x00b"]
)
def test_examine_rejects_cr_lf_and_nul_in_folder_name(bad_folder):
    from app.connectors.email_imap.client import InvalidImapArgumentError

    client, fake = _client_with_fake_conn()
    with pytest.raises(InvalidImapArgumentError):
        client.examine(bad_folder)
    fake.select.assert_not_called()


@pytest.mark.parametrize(
    "kwargs",
    [
        {"text": "a\r\nA2 LOGIN evil evil"},
        {"from_": "a\nb"},
        {"to": "a\x00b"},
        {"subject": "a\rb"},
    ],
)
def test_search_uids_rejects_cr_lf_and_nul_in_criteria(kwargs):
    from app.connectors.email_imap.client import InvalidImapArgumentError

    client, fake = _client_with_fake_conn()
    with pytest.raises(InvalidImapArgumentError):
        client.search_uids(**kwargs)
    fake.uid.assert_not_called()


@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        ('() "." "INBOX"', "INBOX"),
        ('(\\HasNoChildren) "/" "Sent Items"', "Sent Items"),
        ('(\\Noselect) "." INBOX', "INBOX"),
        ('() "." "Weird \\"Folder\\""', 'Weird "Folder"'),
        ('(\\HasChildren) "." "Top/Sub"', "Top/Sub"),
    ],
)
def test_list_folders_parses_both_quoted_and_unquoted_names(raw, expected):
    """A folder name that the server quotes (the common case — GreenMail and
    most real servers quote it even for plain names like INBOX) must not be
    read back as an empty string, or every ``folder in list_folders()``
    membership check downstream would always fail."""
    from app.connectors.email_imap.client import _parse_list_response_name

    assert _parse_list_response_name(raw) == expected


def test_client_exposes_no_write_methods():
    """There is deliberately no send/delete/move/flag method on the wrapper."""
    forbidden = {"store", "expunge", "copy", "append", "send", "delete", "move", "flag"}
    public_methods = {
        name for name in dir(ReadOnlyImapClient) if not name.startswith("_")
    }
    assert forbidden.isdisjoint(public_methods)


def test_search_uids_adds_unseen_only_when_asked():
    client, fake = _client_with_fake_conn()
    client.search_uids(since=date(2026, 10, 9), unseen=True)
    criteria = fake.uid.call_args[0][2:]
    assert "UNSEEN" in criteria
    assert "SINCE" in criteria

    client.search_uids(since=date(2026, 10, 9))
    assert "UNSEEN" not in fake.uid.call_args[0][2:]


def test_unseen_alone_is_a_complete_search():
    client, fake = _client_with_fake_conn()
    client.search_uids(unseen=True)
    assert fake.uid.call_args[0][2:] == ("UNSEEN",)
