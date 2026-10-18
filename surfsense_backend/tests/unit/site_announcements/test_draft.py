"""What a draft announcement must look like before anyone receives it."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from pydantic import ValidationError

from app.site_announcements.draft import AnnouncementDraft

pytestmark = pytest.mark.unit


def _draft(**overrides) -> AnnouncementDraft:
    values = {"title": "Maintenance tonight", "body": "Back by **10pm**."}
    values.update(overrides)
    return AnnouncementDraft(**values)


def test_a_plain_draft_goes_to_everyone() -> None:
    draft = _draft(workspace_id=7)
    assert draft.audience == "all"
    assert draft.workspace_id is None
    assert draft.link is None


def test_title_whitespace_is_collapsed() -> None:
    assert _draft(title="  New   voices  ").title == "New voices"


@pytest.mark.parametrize("field", ["title", "body"])
def test_title_and_body_are_required(field: str) -> None:
    with pytest.raises(ValidationError):
        _draft(**{field: "   "})


def test_overlong_title_is_refused() -> None:
    with pytest.raises(ValidationError):
        _draft(title="x" * 121)


@pytest.mark.parametrize(
    "link", ["https://notes.example.com/docs", "http://example.org", "/dashboard/1"]
)
def test_web_and_in_app_links_are_kept(link: str) -> None:
    assert _draft(link=link).link == link


@pytest.mark.parametrize(
    "link", ["javascript:alert(1)", "//evil.example", "/\\evil.example", "ftp://x", "https://a b"]
)
def test_links_that_are_not_pages_are_refused(link: str) -> None:
    with pytest.raises(ValidationError):
        _draft(link=link)


def test_blank_link_means_none() -> None:
    assert _draft(link="  ").link is None


def test_expiry_must_be_in_the_future() -> None:
    with pytest.raises(ValidationError):
        _draft(expires_at=datetime.now(UTC) - timedelta(minutes=1))


def test_naive_expiry_is_read_as_utc() -> None:
    later = (datetime.now(UTC) + timedelta(days=1)).replace(tzinfo=None)
    assert _draft(expires_at=later).expires_at.tzinfo is UTC


def test_workspace_audience_needs_a_workspace() -> None:
    with pytest.raises(ValidationError):
        _draft(audience="workspace")
    assert _draft(audience="workspace", workspace_id=3).workspace_id == 3
