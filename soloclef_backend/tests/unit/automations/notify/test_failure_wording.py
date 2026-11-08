"""The short result email for a run that did not succeed says how it ended."""

from __future__ import annotations

from datetime import UTC, datetime

import pytest

from app.automations.notify.message import build_failure_email

pytestmark = pytest.mark.unit

_DATE = datetime(2026, 10, 9, tzinfo=UTC)


def _email(status: str, reason: str | None = "why"):
    return build_failure_email(
        to="owner@example.com",
        automation_name="Weekly digest",
        run_date=_DATE,
        status=status,
        reason=reason,
    )


def test_timed_out_email_says_it_hit_its_time_limit():
    message = _email(
        "timed_out", "Stopped after reaching its time limit of 10 minutes."
    )
    assert message.subject == "Weekly digest — 2026-10-09 (timed out)"
    assert "hit its time limit" in message.text_body
    assert "10 minutes" in message.text_body


@pytest.mark.parametrize(
    ("status", "tag"),
    [("failed", "(failed)"), ("needs_attention", "(needs attention)")],
)
def test_failed_and_needs_attention_keep_their_wording(status, tag):
    assert _email(status).subject.endswith(tag)
