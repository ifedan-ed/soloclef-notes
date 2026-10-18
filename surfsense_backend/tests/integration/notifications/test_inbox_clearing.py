"""Deleting one notification and clearing an inbox view.

Clearing takes the list's own filters, so it removes exactly what that view
lists: the caller's rows only, never another account's, and an
administrator's announcement stays in everyone else's inbox. A running job's
notification is never deleted: the job is still updating it.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import User
from app.notifications.persistence import Notification
from app.notifications.service import NotificationService
from app.site_announcements.delivery import notification_rows
from app.site_announcements.persistence import SiteAnnouncement

pytestmark = pytest.mark.integration

BASE = "/api/v1/notifications"


async def _seed(
    db_session: AsyncSession,
    user: User,
    *,
    type: str = "document_processing",
    title: str = "Title",
    read: bool = False,
    workspace_id: int | None = None,
    metadata: dict | None = None,
) -> Notification:
    notification = Notification(
        user_id=user.id,
        workspace_id=workspace_id,
        type=type,
        title=title,
        message="Message",
        read=read,
        notification_metadata=metadata or {},
    )
    db_session.add(notification)
    await db_session.flush()
    return notification


async def _titles(db_session: AsyncSession, user: User) -> list[str]:
    rows = await db_session.execute(
        select(Notification.title)
        .where(Notification.user_id == user.id)
        .order_by(Notification.title)
    )
    return list(rows.scalars().all())


async def _announce(
    db_session: AsyncSession, *recipients: User, read_by: tuple[User, ...] = ()
) -> SiteAnnouncement:
    announcement = SiteAnnouncement(
        title="Podcasts up to 30 minutes",
        body="Try a long one.",
        audience="all",
        created_at=datetime.now(UTC),
    )
    db_session.add(announcement)
    await db_session.flush()
    rows = notification_rows(announcement, [r.id for r in recipients])
    for row in rows:
        row.read = any(row.user_id == r.id for r in read_by)
    announcement.recipient_count = len(rows)
    db_session.add_all(rows)
    await db_session.flush()
    return announcement


# -- one ----------------------------------------------------------------------


async def test_delete_one_removes_only_that_notification(client, db_session, db_user):
    gone = await _seed(db_session, db_user, title="gone")
    await _seed(db_session, db_user, title="kept")

    resp = await client.delete(f"{BASE}/{gone.id}")

    assert resp.status_code == 200
    assert resp.json()["success"] is True
    assert await _titles(db_session, db_user) == ["kept"]


async def test_delete_one_of_another_account_is_404_and_kept(
    client, db_session, db_user, make_user
):
    other = await make_user()
    foreign = await _seed(db_session, other, title="theirs")

    resp = await client.delete(f"{BASE}/{foreign.id}")

    assert resp.status_code == 404
    assert await _titles(db_session, other) == ["theirs"]


async def test_delete_one_twice_is_404(client, db_session, db_user):
    gone = await _seed(db_session, db_user)

    assert (await client.delete(f"{BASE}/{gone.id}")).status_code == 200
    assert (await client.delete(f"{BASE}/{gone.id}")).status_code == 404


# -- a view -------------------------------------------------------------------


async def test_clear_everything_leaves_other_accounts_alone(
    client, db_session, db_user, make_user
):
    other = await make_user()
    await _seed(db_session, db_user, title="a")
    await _seed(db_session, db_user, type="comment_reply", title="b", read=True)
    await _seed(db_session, other, title="theirs")

    resp = await client.delete(BASE)

    assert resp.status_code == 200
    assert resp.json()["deleted_count"] == 2
    assert await _titles(db_session, db_user) == []
    assert await _titles(db_session, other) == ["theirs"]


async def test_clear_a_tab_takes_only_its_category(client, db_session, db_user):
    await _seed(db_session, db_user, type="new_mention", title="mention")
    await _seed(db_session, db_user, type="comment_reply", title="reply")
    await _seed(db_session, db_user, type="connector_indexing", title="status")

    resp = await client.delete(BASE, params={"category": "comments"})

    assert resp.json()["deleted_count"] == 2
    assert await _titles(db_session, db_user) == ["status"]


async def test_clear_unread_keeps_the_read_ones(client, db_session, db_user):
    await _seed(db_session, db_user, title="unread", read=False)
    await _seed(db_session, db_user, title="read", read=True)

    resp = await client.delete(BASE, params={"filter": "unread"})

    assert resp.json()["deleted_count"] == 1
    assert await _titles(db_session, db_user) == ["read"]


async def test_clear_a_workspace_takes_its_own_and_the_global_ones(
    client, db_session, db_user, db_workspace
):
    from app.db import Workspace

    elsewhere = Workspace(name="Elsewhere", user_id=db_user.id)
    db_session.add(elsewhere)
    await db_session.flush()
    await _seed(db_session, db_user, title="here", workspace_id=db_workspace.id)
    await _seed(db_session, db_user, title="everywhere", workspace_id=None)
    await _seed(db_session, db_user, title="elsewhere", workspace_id=elsewhere.id)

    resp = await client.delete(BASE, params={"workspace_id": db_workspace.id})

    assert resp.json()["deleted_count"] == 2
    assert await _titles(db_session, db_user) == ["elsewhere"]


async def test_clear_removes_what_the_same_view_lists(client, db_session, db_user):
    """Same filters, same rows: the count cleared is the total the list showed."""
    await _seed(
        db_session,
        db_user,
        type="connector_indexing",
        title="drive failed",
        metadata={"connector_type": "GOOGLE_DRIVE_CONNECTOR", "status": "failed"},
    )
    await _seed(
        db_session,
        db_user,
        type="connector_indexing",
        title="drive ok",
        metadata={"connector_type": "GOOGLE_DRIVE_CONNECTOR", "status": "completed"},
    )
    await _seed(
        db_session,
        db_user,
        type="connector_indexing",
        title="nextcloud failed",
        metadata={"connector_type": "NEXTCLOUD_CONNECTOR", "status": "failed"},
    )
    view = {
        "category": "status",
        "source_type": "connector:GOOGLE_DRIVE_CONNECTOR",
        "filter": "errors",
    }

    listed = (await client.get(BASE, params=view)).json()
    cleared = (await client.delete(BASE, params=view)).json()

    assert [i["title"] for i in listed["items"]] == ["drive failed"]
    assert cleared["deleted_count"] == listed["total"] == 1
    assert await _titles(db_session, db_user) == ["drive ok", "nextcloud failed"]


async def test_clear_keeps_announcements_the_list_no_longer_shows(
    client, db_session, db_user
):
    past = (datetime.now(UTC) - timedelta(days=1)).isoformat()
    await _seed(
        db_session,
        db_user,
        type="site_announcement",
        title="expired",
        metadata={"announcement_id": 1, "expires_at": past},
    )
    await _seed(db_session, db_user, type="site_announcement", title="live")

    resp = await client.delete(BASE, params={"category": "announcements"})

    assert resp.json()["deleted_count"] == 1
    assert await _titles(db_session, db_user) == ["expired"]


@pytest.mark.parametrize(
    "params",
    [
        {"filter": "unred"},
        {"source_type": "drive"},
        {"category": "everything"},
        {"type": "nonsense"},
    ],
)
async def test_clear_refuses_a_filter_it_does_not_know(
    client, db_session, db_user, params
):
    """A misspelt filter must not widen the view to everything."""
    await _seed(db_session, db_user, title="kept")

    resp = await client.delete(BASE, params=params)

    assert resp.status_code == 422
    assert await _titles(db_session, db_user) == ["kept"]


# -- a running job ------------------------------------------------------------

RUNNING = {"status": "in_progress"}


async def test_clear_all_leaves_a_running_job_and_the_job_carries_on(
    client, db_session, db_user, db_workspace
):
    """Deleting the row a job still updates would fail the job (StaleDataError)."""
    jobs = NotificationService.document_processing
    job = await jobs.notify_processing_started(
        session=db_session,
        user_id=db_user.id,
        document_type="FILE",
        document_name="report.pdf",
        workspace_id=db_workspace.id,
    )
    await jobs.notify_processing_progress(db_session, job, stage="parsing")
    await _seed(db_session, db_user, title="done", metadata={"status": "completed"})
    await _seed(db_session, db_user, title="failed", metadata={"status": "failed"})
    await _seed(db_session, db_user, type="new_mention", title="mention")

    resp = await client.delete(BASE, params={"workspace_id": db_workspace.id})

    assert resp.json()["deleted_count"] == 3
    assert await _titles(db_session, db_user) == [job.title]
    # The job's next updates land on its row, as they would have without the clear.
    await jobs.notify_processing_progress(db_session, job, stage="chunking")
    await db_session.refresh(job)
    assert job.notification_metadata["processing_stage"] == "chunking"
    # Finished, it goes like any other.
    await jobs.update_notification(db_session, job, status="completed")
    assert (await client.delete(f"{BASE}/{job.id}")).status_code == 200
    assert await _titles(db_session, db_user) == []


async def test_clear_unread_leaves_a_running_job(client, db_session, db_user):
    await _seed(db_session, db_user, title="running", metadata=RUNNING)
    await _seed(db_session, db_user, title="unread")

    resp = await client.delete(BASE, params={"filter": "unread"})

    assert resp.json()["deleted_count"] == 1
    assert await _titles(db_session, db_user) == ["running"]


async def test_delete_one_running_is_409_and_kept(client, db_session, db_user):
    running = await _seed(
        db_session, db_user, type="connector_indexing", title="sync", metadata=RUNNING
    )

    resp = await client.delete(f"{BASE}/{running.id}")

    assert resp.status_code == 409
    assert await _titles(db_session, db_user) == ["sync"]


async def test_a_running_job_of_another_account_is_still_404(
    client, db_session, db_user, make_user
):
    """409 says the row exists: only ever for the caller's own."""
    other = await make_user()
    theirs = await _seed(db_session, other, title="theirs", metadata=RUNNING)

    assert (await client.delete(f"{BASE}/{theirs.id}")).status_code == 404


# -- announcements ------------------------------------------------------------


async def test_clearing_an_announcement_leaves_it_with_everyone_else(
    client, db_session, db_user, make_user
):
    other = await make_user()
    announcement = await _announce(db_session, db_user, other)

    resp = await client.delete(BASE, params={"category": "announcements"})

    assert resp.json()["deleted_count"] == 1
    assert await _titles(db_session, db_user) == []
    assert await _titles(db_session, other) == [announcement.title]
    assert await db_session.get(SiteAnnouncement, announcement.id) is not None


async def test_a_read_announcement_deleted_still_counts_as_read(
    client, db_session, db_user, make_user
):
    """The administrator's "read by N of M" does not drop when a reader tidies up."""
    other = await make_user()
    unread_reader = await make_user()
    announcement = await _announce(
        db_session, db_user, other, unread_reader, read_by=(db_user, other)
    )
    mine = await db_session.scalar(
        select(Notification.id).where(Notification.user_id == db_user.id)
    )

    assert (await client.delete(f"{BASE}/{mine}")).status_code == 200

    db_user.is_superuser = True
    await db_session.flush()
    listed = (await client.get("/api/v1/admin/announcements")).json()
    sent = next(a for a in listed["announcements"] if a["id"] == announcement.id)
    assert sent["read_count"] == 2
    assert sent["recipient_count"] == 3


async def test_an_unread_announcement_cleared_is_not_counted_as_read(
    client, db_session, db_user, make_user
):
    other = await make_user()
    announcement = await _announce(db_session, db_user, other, read_by=(other,))

    await client.delete(BASE, params={"category": "announcements"})

    db_user.is_superuser = True
    await db_session.flush()
    listed = (await client.get("/api/v1/admin/announcements")).json()
    sent = next(a for a in listed["announcements"] if a["id"] == announcement.id)
    assert sent["read_count"] == 1
    remaining = await db_session.scalar(
        select(func.count(Notification.id)).where(
            Notification.type == "site_announcement"
        )
    )
    assert remaining == 1
