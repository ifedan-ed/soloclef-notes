"""Merging one search's hits from several accounts into one honest list.

Each account answers newest-first, capped (``client.MAX_SEARCH_RESULTS``).
Laid end to end, the first account could fill the whole limit and hide every
other mailbox, so hits are ordered by their Date header across accounts
before the cut, and the caller is told when more matched than it was shown.
"""

from __future__ import annotations

from collections import Counter
from datetime import UTC, datetime
from email.utils import parsedate_to_datetime
from typing import Any

_UNDATED = datetime.min.replace(tzinfo=UTC)


def _sent_at(row: dict[str, Any]) -> datetime:
    """The Date header as an aware datetime; undated or garbled sorts last."""
    raw = row.get("date")
    if not isinstance(raw, str) or not raw.strip():
        return _UNDATED
    try:
        sent = parsedate_to_datetime(raw)
    except (TypeError, ValueError, IndexError):
        return _UNDATED
    return sent if sent.tzinfo else sent.replace(tzinfo=UTC)


def newest_first_within_limit(
    rows: list[dict[str, Any]], limit: int
) -> list[dict[str, Any]]:
    """Per-account error rows first (always kept), then the newest ``limit`` hits."""
    errors = [row for row in rows if "error" in row]
    hits = [row for row in rows if "error" not in row]
    hits.sort(key=_sent_at, reverse=True)
    return errors + hits[: max(0, limit)]


def more_than_shown(
    hits: list[dict[str, Any]], *, limit: int, per_account_cap: int
) -> bool:
    """Whether matches were left out: more than ``limit`` came back (callers
    ask for one extra), or an account filled its own cap and may hold more."""
    if len(hits) > limit:
        return True
    per_account = Counter(row.get("account_id") for row in hits)
    return any(count >= per_account_cap for count in per_account.values())
