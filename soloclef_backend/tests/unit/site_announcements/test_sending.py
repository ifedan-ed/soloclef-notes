"""Sending reaches the right people, each with their own read state; withdrawing takes it back."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest

from app.notifications.constants import CATEGORY_TYPES
from app.site_announcements.draft import AnnouncementDraft
from app.site_announcements.sending import (
    UnknownAnnouncementError,
    UnknownWorkspaceError,
    send_announcement,
    withdraw_announcement,
)
from tests.unit.site_announcements.fake_store import FakeStore

pytestmark = pytest.mark.unit

NOW = datetime(2026, 9, 26, 12, 0, tzinfo=UTC)
ADMIN = uuid4()
ANN, BEN, CAT = uuid4(), uuid4(), uuid4()


def _draft(**overrides) -> AnnouncementDraft:
    values = {"title": "Podcasts up to 30 minutes", "body": "Try a *long* one."}
    values.update(overrides)
    return AnnouncementDraft(**values)


async def test_everyone_gets_one_unread_copy() -> None:
    store = FakeStore(users=[ANN, BEN, CAT])
    sent = await send_announcement(store, _draft(), ADMIN, NOW)

    assert sent.recipient_count == 3
    assert sorted(map(str, (r.user_id for r in store.inbox))) == sorted(
        map(str, (ANN, BEN, CAT))
    )
    for row in store.inbox:
        assert row.read is False
        assert row.workspace_id is None  # shown in every workspace
        assert row.type in CATEGORY_TYPES["announcements"]
        assert row.title == "Podcasts up to 30 minutes"
        assert row.message == "Try a *long* one."
        assert row.notification_metadata["announcement_id"] == sent.id
    assert store.commits == 1


async def test_a_workspace_announcement_reaches_only_its_members() -> None:
    store = FakeStore(users=[ANN, BEN, CAT], members={4: [ANN, BEN]})
    sent = await send_announcement(
        store, _draft(audience="workspace", workspace_id=4), ADMIN, NOW
    )

    assert sent.recipient_count == 2
    assert store.inbox_of(CAT) == []
    assert {row.workspace_id for row in store.inbox} == {4}


async def test_an_unknown_workspace_sends_nothing() -> None:
    store = FakeStore(users=[ANN])
    with pytest.raises(UnknownWorkspaceError):
        await send_announcement(
            store, _draft(audience="workspace", workspace_id=99), ADMIN, NOW
        )
    assert store.inbox == []
    assert store.commits == 0


async def test_a_recipient_listed_twice_gets_one_copy() -> None:
    store = FakeStore(users=[ANN, ANN])
    sent = await send_announcement(store, _draft(), ADMIN, NOW)
    assert sent.recipient_count == 1
    assert len(store.inbox_of(ANN)) == 1


async def test_reading_it_is_per_person() -> None:
    store = FakeStore(users=[ANN, BEN])
    sent = await send_announcement(store, _draft(), ADMIN, NOW)

    store.inbox_of(ANN)[0].read = True

    assert store.inbox_of(BEN)[0].read is False
    assert await store.read_counts([sent.id]) == {sent.id: 1}


async def test_link_and_expiry_travel_with_each_copy() -> None:
    expires = datetime.now(UTC) + timedelta(days=2)
    store = FakeStore(users=[ANN])
    await send_announcement(
        store, _draft(link="/dashboard/1/settings", expires_at=expires), ADMIN, NOW
    )
    metadata = store.inbox_of(ANN)[0].notification_metadata
    assert metadata["link"] == "/dashboard/1/settings"
    assert metadata["expires_at"] == expires.isoformat()


async def test_withdrawing_removes_every_copy_and_keeps_the_record() -> None:
    store = FakeStore(users=[ANN, BEN])
    first = await send_announcement(store, _draft(), ADMIN, NOW)
    second = await send_announcement(store, _draft(title="Other"), ADMIN, NOW)

    withdrawn = await withdraw_announcement(store, first.id, NOW)

    assert withdrawn.withdrawn_at == NOW
    assert store.announcements[first.id] is withdrawn
    remaining = {row.notification_metadata["announcement_id"] for row in store.inbox}
    assert remaining == {second.id}


async def test_withdrawing_twice_keeps_the_first_time() -> None:
    store = FakeStore(users=[ANN])
    sent = await send_announcement(store, _draft(), ADMIN, NOW)
    await withdraw_announcement(store, sent.id, NOW)
    again = await withdraw_announcement(store, sent.id, NOW + timedelta(hours=1))
    assert again.withdrawn_at == NOW


async def test_withdrawing_an_unknown_announcement_fails() -> None:
    with pytest.raises(UnknownAnnouncementError):
        await withdraw_announcement(FakeStore(), 42, NOW)
