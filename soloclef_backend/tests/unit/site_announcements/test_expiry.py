"""Whether an announcement still shows."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from sqlalchemy.dialects import postgresql

from app.site_announcements.expiry import announcement_status, unexpired
from app.site_announcements.persistence import SiteAnnouncement

pytestmark = pytest.mark.unit

NOW = datetime(2026, 9, 26, tzinfo=UTC)


def _announcement(**values) -> SiteAnnouncement:
    return SiteAnnouncement(title="t", body="b", audience="all", **values)


def test_live_until_it_expires() -> None:
    assert announcement_status(_announcement(), NOW) == "live"
    later = _announcement(expires_at=NOW + timedelta(hours=1))
    assert announcement_status(later, NOW) == "live"
    assert announcement_status(later, NOW + timedelta(hours=1)) == "expired"


def test_withdrawn_wins_over_expired() -> None:
    gone = _announcement(expires_at=NOW - timedelta(days=1), withdrawn_at=NOW)
    assert announcement_status(gone, NOW) == "withdrawn"


def test_the_inbox_filter_only_touches_announcements() -> None:
    sql = str(
        unexpired(NOW).compile(
            dialect=postgresql.dialect(), compile_kwargs={"literal_binds": True}
        )
    )
    assert "notifications.type != 'site_announcement'" in sql
    assert "expires_at" in sql
