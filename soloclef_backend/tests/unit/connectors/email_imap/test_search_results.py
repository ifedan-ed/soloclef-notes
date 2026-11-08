"""Merging per-account search hits: newest first overall, cut honestly."""

from __future__ import annotations

import pytest

from app.connectors.email_imap.search_results import (
    more_than_shown,
    newest_first_within_limit,
)

pytestmark = pytest.mark.unit


def _hit(account: str, date: str, uid: str = "1") -> dict:
    return {"account_id": account, "date": date, "uid": uid, "subject": "s"}


def test_hits_from_every_account_are_ordered_newest_first_before_the_cut():
    rows = [
        _hit("a", "Thu, 08 Oct 2026 09:00:00 +0000", "1"),
        _hit("a", "Wed, 07 Oct 2026 09:00:00 +0000", "2"),
        _hit("b", "Fri, 09 Oct 2026 07:00:00 +0000", "3"),
        _hit("b", "Fri, 09 Oct 2026 10:00:00 +0200", "4"),  # 08:00 UTC
    ]
    out = newest_first_within_limit(rows, 3)
    assert [r["uid"] for r in out] == ["4", "3", "1"]


def test_account_errors_are_always_kept_and_never_count_towards_the_limit():
    rows = [
        {"account_id": "x", "account_label": "Work", "error": "Could not connect"},
        _hit("a", "Thu, 08 Oct 2026 09:00:00 +0000", "1"),
        _hit("a", "Wed, 07 Oct 2026 09:00:00 +0000", "2"),
    ]
    out = newest_first_within_limit(rows, 1)
    assert [r.get("uid") for r in out] == [None, "1"]
    assert "error" in out[0]


def test_an_unparseable_date_sorts_last_instead_of_failing():
    rows = [
        _hit("a", "not a date", "1"),
        _hit("a", "", "2"),
        _hit("b", "Thu, 08 Oct 2026 09:00:00", "3"),  # no zone: read as UTC
    ]
    out = newest_first_within_limit(rows, 10)
    assert out[0]["uid"] == "3"
    assert {r["uid"] for r in out[1:]} == {"1", "2"}


def test_more_than_shown_when_the_extra_row_came_back():
    hits = [_hit("a", "", str(i)) for i in range(11)]
    assert more_than_shown(hits, limit=10, per_account_cap=50) is True
    assert more_than_shown(hits[:10], limit=10, per_account_cap=50) is False


def test_more_than_shown_when_one_account_hit_its_own_cap():
    """The per-account cap hides the overflow: an account that filled it may
    have more, even when the overall count fits the limit."""
    hits = [_hit("a", "", str(i)) for i in range(50)]
    assert more_than_shown(hits, limit=50, per_account_cap=50) is True
    assert more_than_shown(hits[:49], limit=50, per_account_cap=50) is False
