"""A reader who deletes an announcement from their inbox still counts as having read it.

Deleting a notification removes only the caller's copy: the announcement, and
every other recipient's copy, stay. The Sent list's "read by N of M" counts
read copies, so each read copy deleted is added to the announcement's
``cleared_read_count`` instead of disappearing from the count.
"""

from __future__ import annotations

from collections import Counter
from collections.abc import Iterable
from typing import NamedTuple

from sqlalchemy import update
from sqlalchemy.ext.asyncio import AsyncSession

from app.notifications.types import SITE_ANNOUNCEMENT_TYPE
from app.site_announcements.persistence import SiteAnnouncement


class DeletedCopy(NamedTuple):
    """What a deleted notification row said about itself."""

    type: str
    read: bool
    announcement_id: str | None


def read_announcements(deleted: Iterable[DeletedCopy]) -> Counter[int]:
    """How many read copies of each announcement were among the deleted rows."""
    counts: Counter[int] = Counter()
    for row in deleted:
        if row.type != SITE_ANNOUNCEMENT_TYPE or not row.read:
            continue
        if row.announcement_id and row.announcement_id.isdigit():
            counts[int(row.announcement_id)] += 1
    return counts


async def keep_reads_of(session: AsyncSession, deleted: Iterable[DeletedCopy]) -> None:
    for announcement_id, n in read_announcements(deleted).items():
        await session.execute(
            update(SiteAnnouncement)
            .where(SiteAnnouncement.id == announcement_id)
            .values(cleared_read_count=SiteAnnouncement.cleared_read_count + n)
        )
